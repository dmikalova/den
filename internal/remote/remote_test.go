package remote

import "testing"

func TestAs(t *testing.T) {
	cases := map[string]string{
		"dmikalova@den.local": "root@den.local",
		"den":                 "root@den",
		"a@b@den":             "root@den",
	}
	for in, want := range cases {
		if got := (Host{Addr: in}).As("root").Addr; got != want {
			t.Errorf("As(%q) = %q, want %q", in, got, want)
		}
	}
}
