package check

import (
	"strings"
	"testing"
)

func TestScriptCoversEveryCheck(t *testing.T) {
	script := Script("dmikalova")
	for _, c := range List("dmikalova") {
		if !strings.Contains(script, c.Test) {
			t.Errorf("check %q missing its test from the script", c.Name)
		}
	}
}

// A name that reached the script unquoted would silently truncate the report.
func TestNamesAreShellSafe(t *testing.T) {
	for _, c := range List("dmikalova") {
		if strings.ContainsAny(c.Name, "'\"\\\t\n") {
			t.Errorf("check name %q contains a character that breaks the printf", c.Name)
		}
	}
}

func TestListFollowsTheUser(t *testing.T) {
	for _, c := range List("someone") {
		if strings.Contains(c.Test, "dmikalova") {
			t.Errorf("check %q hardcodes the operator account: %s", c.Name, c.Test)
		}
	}
}

func TestParse(t *testing.T) {
	out := "ok\troot on @\nfail\tswap active\nLast login: today\nok\tlinger enabled\n"
	got := Parse(out)
	want := []Result{
		{"root on @", true},
		{"swap active", false},
		{"linger enabled", true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("result %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestFailed(t *testing.T) {
	results := []Result{{"a", true}, {"b", false}, {"c", true}}
	bad := Failed(results)
	if len(bad) != 1 || bad[0].Name != "b" {
		t.Errorf("got %v, want just b", bad)
	}
}
