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

	"github.com/dmikalova/den/v3/internal/storage"
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
	checks := mounts()
	// A bind mount's FSROOT is the subvolume plus the directory it binds.
	checks = append(checks, Check{
		"containers on " + storage.ContainerStore,
		`[ "$(findmnt -no FSROOT ` + store + `)" = /@cache/containers ]`,
	})
	return append(checks, []Check{
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

		// A timer that is enabled says a backup is scheduled. A repository
		// that answers says there is something to restore from, which is the
		// part that is easy to be wrong about.
		{"backup timer enabled", `systemctl is-enabled kopia-backup.timer`},
		{"restore drill timer enabled", `systemctl is-enabled kopia-restore-drill.timer`},
		// The state directory is 0700 root, so this is the one check that
		// cannot be made as the login user.
		{"backup repository connected", `sudo -n test -s /var/lib/kopia/repository.config`},

		// The ping is the only signal that a job ran at all, and both ways it
		// can break are silent. A den older than the ping subcommand makes
		// every ExecStartPost= a no-op, and an empty directory means the
		// secret never carried a URL. Neither shows up as a failed unit.
		{"den can ping", `den 2>&1 | grep -q '^  den ping '`},
		{
			"healthcheck urls present",
			`sudo -n sh -c 'ls -A /etc/den/healthcheck 2>/dev/null | grep -q .'`,
		},

		// Active is not enough: an smbd that listens on loopback alone is
		// active and unreachable, which is how bind interfaces only with
		// tailscale0 failed.
		{"samba running", `systemctl is-active smbd`},
		{
			"samba listening beyond loopback",
			`ss -Htln 'sport = :445' | grep -qvE '127\.0\.0\.1|\[::1\]'`,
		},

		{"proxy running", `systemctl is-active caddy`},
		{"proxy answering", `curl -fsS -o /dev/null http://127.0.0.1/`},

		// The agent is a system unit and collects; the hub is a rootless
		// quadlet and displays. Both have to be up for metrics to exist.
		{"metrics agent running", `systemctl is-active beszel-agent`},
		{"metrics answering", `curl -fsS -o /dev/null http://127.0.0.1:8090/`},

		// Two instances of the agent orchestrator, personal and work.
		{"paseo answering", `curl -fsS -o /dev/null http://127.0.0.1:6767/api/health`},
		{"paseo-work answering", `curl -fsS -o /dev/null http://127.0.0.1:6768/api/health`},

		// The vpn pod. A healthy tunnel is not enough: the point is that the
		// apps leave through it, so each one is asked for its address and it
		// must not be den's. See docs/vpn.md.
		{
			"vpn tunnel healthy",
			`[ "$(podman inspect gluetun --format '{{.State.Health.Status}}')" = healthy ]`,
		},
		{"qbittorrent answering", `curl -sS -o /dev/null http://127.0.0.1:8080/`},
		{"slskd answering", `curl -fsS -o /dev/null http://127.0.0.1:5030/health`},
		{"qbittorrent egress is the vpn", egressNotHome("qbittorrent")},
		{"slskd egress is the vpn", egressNotHome("slskd")},
	}...)
}

// egressNotHome asks a container and den for their public addresses. Both
// must answer, and the answers must differ. An empty answer fails, so a dead
// tunnel is a failure and not a pass.
func egressNotHome(container string) string {
	const ip = "https://api.ipify.org"
	return `home=$(curl -fsS -m 10 ` + ip + `) && ` +
		`vpn=$(podman exec ` + container + ` wget -T 10 -qO- ` + ip + `) && ` +
		`[ -n "$home" ] && [ -n "$vpn" ] && [ "$vpn" != "$home" ]`
}

// mounts checks that each area in the storage layout is mounted from its
// subvolume. FSROOT is the subvolume path, so this is both "is mounted" and
// "is the right subvolume" in one question.
func mounts() []Check {
	var checks []Check
	for _, a := range storage.Areas {
		if a.Subvolume == "" {
			continue
		}
		checks = append(checks, Check{
			a.Path + " on " + a.Subvolume,
			`[ "$(findmnt -no FSROOT ` + a.Path + `)" = /` + a.Subvolume + ` ]`,
		})
	}
	return checks
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
