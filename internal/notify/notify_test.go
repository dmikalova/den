package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSendRejectsAPlaintextWebhook(t *testing.T) {
	err := Send(context.Background(), "http://example.invalid/hook", Message{Body: "hi"})
	if err == nil {
		t.Fatal("an http webhook was accepted; the credential would go out in clear")
	}
}

func TestSendRejectsAnEmptyWebhook(t *testing.T) {
	if err := Send(context.Background(), "   ", Message{Body: "hi"}); err == nil {
		t.Fatal("an empty webhook was accepted")
	}
}

func TestScrubRemovesTheWebhookFromAnError(t *testing.T) {
	const hook = "https://discord.com/api/webhooks/123/supersecrettoken"
	err := scrub(&urlish{hook}, hook)
	if strings.Contains(err.Error(), "supersecrettoken") {
		t.Fatalf("the webhook token survived into an error: %q", err)
	}
	if !strings.Contains(err.Error(), "<webhook>") {
		t.Errorf("the redaction marker is missing: %q", err)
	}
}

type urlish struct{ u string }

func (e *urlish) Error() string { return "Post \"" + e.u + "\": connection refused" }

func TestBuildBlocksMentionsFromLogOutput(t *testing.T) {
	p := build(Message{Body: "@everyone the disk is on fire"})
	if p.AllowedMentions.Parse == nil {
		t.Fatal("allowed_mentions.parse is nil, which lets Discord ping @everyone")
	}
	if len(p.AllowedMentions.Parse) != 0 {
		t.Errorf("allowed_mentions.parse is %v, want empty", p.AllowedMentions.Parse)
	}

	// An explicit empty array must survive encoding; omitting the key would
	// restore Discord's default of honouring every mention in the body.
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"allowed_mentions":{"parse":[]}`)) {
		t.Errorf("encoded payload does not pin allowed_mentions: %s", raw)
	}
}

func TestBuildFitsDiscordLimits(t *testing.T) {
	long := strings.Repeat("x", 9000)
	fields := make([]Field, 40)
	for i := range fields {
		fields[i] = Field{Name: long, Value: long}
	}

	e := build(Message{Title: long, Body: long, Fields: fields}).Embeds[0]

	if n := len([]rune(e.Title)); n > maxTitle {
		t.Errorf("title is %d runes, limit is %d", n, maxTitle)
	}
	if n := len([]rune(e.Description)); n > maxDescription {
		t.Errorf("description is %d runes, limit is %d", n, maxDescription)
	}
	if len(e.Fields) > maxFields {
		t.Errorf("got %d fields, limit is %d", len(e.Fields), maxFields)
	}
	for i, f := range e.Fields {
		if n := len([]rune(f.Name)); n > maxFieldName {
			t.Errorf("field %d name is %d runes, limit is %d", i, n, maxFieldName)
		}
		if n := len([]rune(f.Value)); n > maxFieldValue {
			t.Errorf("field %d value is %d runes, limit is %d", i, n, maxFieldValue)
		}
	}
}

func TestBuildGivesAnEmptyFieldAPlaceholder(t *testing.T) {
	e := build(Message{Fields: []Field{{Name: "exit", Value: ""}}}).Embeds[0]
	if e.Fields[0].Value == "" {
		t.Error("an empty field value was kept; Discord rejects the whole request")
	}
}

func TestPostRetriesWhileTheFarEndMightRecover(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if err := post(context.Background(), srv.URL, "application/json", nil); err != nil {
		t.Fatalf("post: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("made %d requests, want 2", got)
	}
}

func TestPostDoesNotRetryAClientError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err := post(context.Background(), srv.URL, "application/json", nil); err == nil {
		t.Fatal("a 404 was reported as success")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("made %d requests, want 1: a deleted webhook must not be retried", got)
	}
}

func TestPostSendsTheEncodedEmbed(t *testing.T) {
	got := make(chan payload, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p payload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Errorf("decode: %v", err)
		}
		got <- p
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	body, err := json.Marshal(build(Message{
		Title: "den-reconcile failed",
		Body:  "unit entered failed state",
		Level: LevelError,
		Host:  "den",
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := post(context.Background(), srv.URL, "application/json", body); err != nil {
		t.Fatalf("post: %v", err)
	}

	p := <-got
	if len(p.Embeds) != 1 {
		t.Fatalf("got %d embeds, want 1", len(p.Embeds))
	}
	e := p.Embeds[0]
	if e.Title != "den-reconcile failed" {
		t.Errorf("title is %q", e.Title)
	}
	if e.Color != LevelError.color() {
		t.Errorf("colour is %d, want the error colour %d", e.Color, LevelError.color())
	}
	if e.Footer == nil || e.Footer.Text != "den" {
		t.Errorf("footer does not name the host: %+v", e.Footer)
	}
	if e.Timestamp == "" {
		t.Error("the embed carries no timestamp")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("a short string was altered: %q", got)
	}
	if got := truncate("hello", 4); got != "h..." {
		t.Errorf("got %q, want %q", got, "h...")
	}
	if got := truncate("héllo wörld", 6); len([]rune(got)) != 6 {
		t.Errorf("multi-byte text was cut to %d runes, want 6: %q", len([]rune(got)), got)
	}
}

func TestLevelValid(t *testing.T) {
	for _, l := range []Level{LevelInfo, LevelWarn, LevelError} {
		if !l.Valid() {
			t.Errorf("%q is rejected", l)
		}
	}
	if Level("catastrophe").Valid() {
		t.Error("an unknown level was accepted")
	}
}
