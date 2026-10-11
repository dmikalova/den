// Package notify delivers den's operational alerts. Discord carries the detail
// a human reads; a healthcheck ping is the dead-man's switch that fires when
// den goes silent, which is the failure a monitor running on den cannot report.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Discord's documented embed limits. Exceeding any one of them fails the whole
// request, so every string is cut to fit before it is sent.
const (
	maxTitle       = 256
	maxDescription = 4096
	maxFieldName   = 256
	maxFieldValue  = 1024
	maxFields      = 25
)

// timeout bounds a single HTTP attempt. An alert that has not landed in ten
// seconds is not going to.
const timeout = 10 * time.Second

// attempts is the total number of tries per send. Discord rate-limits webhooks
// at a few requests per second, and a cascade of unit failures will hit that.
const attempts = 3

// Level selects the embed colour.
type Level string

// The levels den reports at. Anything den sends is already worth a human's
// attention, so there is no debug level.
const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Valid reports whether l is a level this package understands.
func (l Level) Valid() bool {
	switch l {
	case LevelInfo, LevelWarn, LevelError:
		return true
	default:
		return false
	}
}

// color is the decimal RGB Discord renders down the side of the embed.
func (l Level) color() int {
	switch l {
	case LevelError:
		return 0xE74C3C
	case LevelWarn:
		return 0xF1C40F
	default:
		return 0x2ECC71
	}
}

// Field is one labelled value in the embed.
type Field struct {
	Name   string
	Value  string
	Inline bool
}

// Message is one alert.
type Message struct {
	Title  string
	Body   string
	Level  Level
	Host   string
	Fields []Field
}

type payload struct {
	Embeds          []embed         `json:"embeds"`
	AllowedMentions allowedMentions `json:"allowed_mentions"`
}

// allowedMentions with an empty Parse stops Discord acting on an @everyone that
// appears in captured log output. Alert bodies are untrusted text.
type allowedMentions struct {
	Parse []string `json:"parse"`
}

type embed struct {
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Color       int          `json:"color"`
	Timestamp   string       `json:"timestamp"`
	Fields      []embedField `json:"fields,omitempty"`
	Footer      *embedFooter `json:"footer,omitempty"`
}

type embedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type embedFooter struct {
	Text string `json:"text"`
}

// Send posts m to a Discord webhook.
//
// The webhook URL is a bearer credential: anyone holding it can post to the
// channel. It is never returned in an error, because these errors are written
// to the journal.
func Send(ctx context.Context, webhook string, m Message) error {
	if strings.TrimSpace(webhook) == "" {
		return errors.New("no Discord webhook configured")
	}
	if !strings.HasPrefix(webhook, "https://") {
		return errors.New("the Discord webhook must be an https URL")
	}
	if !m.Level.Valid() {
		m.Level = LevelInfo
	}

	body, err := json.Marshal(build(m))
	if err != nil {
		return fmt.Errorf("encoding the Discord payload: %w", err)
	}
	return post(ctx, webhook, "application/json", body)
}

// Ping notifies the external dead-man's switch that a job finished. A failing
// ping must not mask the alert that prompted it, so callers report it
// separately.
func Ping(ctx context.Context, url string) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("no healthcheck URL configured")
	}
	if !strings.HasPrefix(url, "https://") {
		return errors.New("the healthcheck URL must be an https URL")
	}
	return post(ctx, url, "text/plain", nil)
}

func build(m Message) payload {
	title := truncate(m.Title, maxTitle)
	if title == "" {
		title = "den"
	}

	e := embed{
		Title:       title,
		Description: truncate(m.Body, maxDescription),
		Color:       m.Level.color(),
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}
	if m.Host != "" {
		e.Footer = &embedFooter{Text: m.Host}
	}
	for i, f := range m.Fields {
		if i == maxFields {
			break
		}
		// Discord rejects a field whose value is empty, so give it a dash.
		value := truncate(f.Value, maxFieldValue)
		if value == "" {
			value = "-"
		}
		e.Fields = append(e.Fields, embedField{
			Name:   truncate(f.Name, maxFieldName),
			Value:  value,
			Inline: f.Inline,
		})
	}

	return payload{Embeds: []embed{e}, AllowedMentions: allowedMentions{Parse: []string{}}}
}

// post sends body to url, retrying while the far end says the request could
// have succeeded later. url is treated as secret throughout.
func post(ctx context.Context, url, contentType string, body []byte) error {
	var last error
	for attempt := range attempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}

		retryable, err := attemptPost(ctx, url, contentType, body)
		if err == nil {
			return nil
		}
		last = err
		if !retryable {
			return err
		}
	}
	return last
}

func attemptPost(ctx context.Context, url, contentType string, body []byte) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, scrub(err, url)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return true, scrub(err, url)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return false, nil
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode >= 500:
		return true, fmt.Errorf("the webhook host returned %s", resp.Status)
	default:
		return false, fmt.Errorf("the webhook host returned %s", resp.Status)
	}
}

func backoff(attempt int) time.Duration {
	return time.Duration(attempt) * time.Second
}

// scrub removes the credential from an error. net/http wraps the full URL into
// every transport error, and the webhook path carries its token.
func scrub(err error, secret string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if secret != "" {
		msg = strings.ReplaceAll(msg, secret, "<webhook>")
	}
	return errors.New(msg)
}

// truncate cuts s to at most n characters, counting the way Discord counts.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	const ellipsis = "..."
	if n <= len(ellipsis) {
		return string(r[:n])
	}
	return string(r[:n-len(ellipsis)]) + ellipsis
}
