// Package check holds den's smoke checks: a short list of assertions about a
// converged host.
//
// These do not replace mgmt. mgmt decides what the host should be and makes it
// so, and anything mgmt manages is already guaranteed by the next converge.
// The checks exist for the two things a converge cannot tell you: whether the
// result actually works, and whether the host has drifted since.
//
// The list is deliberately short. Every check added here is a check that can
// break for reasons that do not matter, and a suite nobody trusts is worse
// than no suite.
package check

import (
	"fmt"
	"strings"
)

// Check is one assertion. Test is shell that exits zero when the host is in
// the wanted state; its output is discarded.
type Check struct {
	Name string
	Test string
}

// Result pairs a check with how it went.
type Result struct {
	Name string
	OK   bool
}

// List returns the smoke checks for a host whose login account is user.
func List(user string) []Check {
	store := fmt.Sprintf("/home/%s/.local/share/containers", user)
	return []Check{
		// FSROOT is the subvolume path, so this is both "is mounted" and "is
		// the right subvolume" in one question.
		{"root on @", `[ "$(findmnt -no FSROOT /)" = /@ ]`},
		{"home on @home", `[ "$(findmnt -no FSROOT /home)" = /@home ]`},
		{"containers on @containers", `[ "$(findmnt -no FSROOT ` + store + `)" = /@containers ]`},
		{"appdata on @appdata", `[ "$(findmnt -no FSROOT /srv/appdata)" = /@appdata ]`},
		{"dumps on @dumps", `[ "$(findmnt -no FSROOT /srv/dumps)" = /@dumps ]`},
		{"models on @models", `[ "$(findmnt -no FSROOT /srv/models)" = /@models ]`},

		// Installed is not the same as working: rootless podman fails on a
		// missing subuid range or a store the user cannot write.
		{"podman rootless", `[ "$(podman info --format '{{.Host.Security.Rootless}}')" = true ]`},
		{"linger enabled", `[ -e /var/lib/systemd/linger/` + user + ` ]`},
		{
			"unprivileged ports from 80",
			`[ "$(sysctl -n net.ipv4.ip_unprivileged_port_start)" = 80 ]`,
		},

		{"tailscale running", `tailscale status`},
		{"reconcile timer enabled", `systemctl is-enabled den-reconcile.timer`},
		{"unattended upgrades active", `systemctl is-active unattended-upgrades`},
		{"swap active", `swapon --show --noheadings | grep -q .`},
	}
}

// Script renders the checks as one shell program. It is one script rather than
// one command per check so the whole run costs a single ssh round trip.
//
// No set -e: a failing check is the point, not a reason to stop.
func Script(user string) string {
	var b strings.Builder
	for _, c := range List(user) {
		fmt.Fprintf(
			&b,
			"if %s >/dev/null 2>&1; then printf 'ok\\t%s\\n'; else printf 'fail\\t%s\\n'; fi\n",
			c.Test,
			c.Name,
			c.Name,
		)
	}
	return b.String()
}

// Parse reads what Script printed. Unrecognised lines are ignored: a login
// banner or a sudo notice would otherwise read as a failure.
func Parse(out string) []Result {
	var results []Result
	for line := range strings.SplitSeq(out, "\n") {
		name, ok := strings.CutPrefix(line, "ok\t")
		if ok {
			results = append(results, Result{Name: strings.TrimSpace(name), OK: true})
			continue
		}
		if name, isFail := strings.CutPrefix(line, "fail\t"); isFail {
			results = append(results, Result{Name: strings.TrimSpace(name), OK: false})
		}
	}
	return results
}

// Failed returns the checks that did not pass.
func Failed(results []Result) []Result {
	var bad []Result
	for _, r := range results {
		if !r.OK {
			bad = append(bad, r)
		}
	}
	return bad
}
