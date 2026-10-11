// Package bootstrap brings a freshly installed den up to the point where mgmt
// owns the host layer, and then runs mgmt. It is two passes on purpose: the
// first pass patches and reboots, the second converges config and can be
// repeated forever.
package bootstrap

import (
	"context"
	"fmt"
	"io"

	"github.com/dmikalova/den/internal/remote"
)

// DefaultMgmtVersion is the mgmt release den installs. The binary is checked
// against the signed sums file published beside it, so this is pinned rather
// than tracking latest.
const DefaultMgmtVersion = "1.1.0"

// DefaultConfigRepo is the git remote holding the mcl source.
const DefaultConfigRepo = "https://github.com/dmikalova/den.git"

// DefaultConfigDir is where that remote is cloned on den.
const DefaultConfigDir = "/srv/den-config"

// Options are the knobs both passes share.
type Options struct {
	User        string
	MgmtVersion string
	ConfigRepo  string
	ConfigDir   string

	// Terminfo is the operator's own terminal description, as infocmp -x
	// emits it. Empty skips the step.
	Terminfo string

	// Noop runs mgmt in report-only mode.
	Noop bool
}

// Step is one script, run either as the login user or under sudo.
type Step struct {
	Name   string
	Root   bool
	Script string

	// Wait holds the run until ssh answers again, for steps that reboot.
	Wait bool
}

// Bootstrap is the first pass: identify the host, patch it, install mgmt and
// tailscale, reboot if the kernel moved.
func Bootstrap(o Options) []Step {
	steps := []Step{
		{Name: "identify the host", Script: identify},
		{Name: "patch and install base packages", Root: true, Script: patch},
		{
			Name:   "install mgmt " + o.MgmtVersion,
			Root:   true,
			Script: fmt.Sprintf(installMgmt, o.MgmtVersion),
		},
	}
	if o.Terminfo != "" {
		steps = append(steps, Step{
			Name:   "teach den about this terminal",
			Root:   true,
			Script: fmt.Sprintf(installTerminfo, o.Terminfo),
		})
	}
	return append(steps,
		Step{Name: "install tailscale", Root: true, Script: installTailscale},
		Step{Name: "reboot if required", Root: true, Script: rebootIfNeeded, Wait: true},
	)
}

// Apply is the second pass: fetch the config repo and converge the host layer.
// Running it again is how den reconciles by hand.
func Apply(o Options) []Step {
	noop := ""
	if o.Noop {
		noop = " --noop"
	}
	return []Step{
		{
			Name:   "make the config directory",
			Root:   true,
			Script: fmt.Sprintf(makeConfigDir, o.User, o.User, o.ConfigDir),
		},
		{
			Name:   "fetch the config repo",
			Script: fmt.Sprintf(fetchConfig, o.ConfigDir, o.ConfigRepo),
		},
		{
			Name:   "converge the host layer",
			Root:   true,
			Script: fmt.Sprintf(runMgmt, o.ConfigDir, noop),
		},
	}
}

// Run executes steps in order and stops at the first failure.
func Run(ctx context.Context, h remote.Host, steps []Step, log io.Writer) error {
	for i, s := range steps {
		if _, err := fmt.Fprintf(log, "\n[%d/%d] %s\n", i+1, len(steps), s.Name); err != nil {
			return err
		}
		run := h.Run
		if s.Root {
			run = h.RunRoot
		}
		if err := run(ctx, s.Script); err != nil {
			return fmt.Errorf("%s: %w", s.Name, err)
		}
		if s.Wait {
			if err := h.Wait(ctx); err != nil {
				return fmt.Errorf("%s: %w", s.Name, err)
			}
		}
	}
	return nil
}

const identify = `set -euo pipefail
. /etc/os-release
printf 'host    %s\n' "$(hostname)"
printf 'system  %s\n' "$PRETTY_NAME"
printf 'kernel  %s\n' "$(uname -r)"
arch=$(dpkg --print-architecture)
printf 'arch    %s\n' "$arch"
test "$arch" = amd64
`

const patch = `set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get -y full-upgrade
apt-get -y autoremove --purge
apt-get install -y ca-certificates curl git
`

// installMgmt checks the binary against the sums file published with the
// release. den does not pipe an install script into a shell. The sums file is
// PGP clearsigned and the signature is not yet checked, only the digest.
const installMgmt = `set -euo pipefail
want=%s
if [ "$(mgmt --version 2>/dev/null | grep -o '[0-9][0-9.]*' | head -1 || true)" = "$want" ]; then
  echo "mgmt $want already installed"
  exit 0
fi
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
bin=mgmt-linux-amd64-${want}
base=https://github.com/purpleidea/mgmt/releases/download/${want}
curl -fsSL -o "$tmp/$bin" "$base/$bin"
curl -fsSL -o "$tmp/sums" "$base/SHA256SUMS.asc"
( cd "$tmp" && grep "  $bin\$" sums | sha256sum -c - )
install -m 0755 "$tmp/$bin" /usr/local/bin/mgmt
mgmt --version
`

// installTerminfo compiles the operator's terminal description system wide, so
// that a sudo session gets it too. Without this, anything that pages output
// warns that the terminal is not fully functional.
const installTerminfo = `set -euo pipefail
if ! command -v tic >/dev/null; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get install -y ncurses-bin
fi
tic -x -o /usr/share/terminfo - <<'TERMINFO'
%s
TERMINFO
echo "terminfo compiled into /usr/share/terminfo"
`

// installTailscale adds the upstream repo rather than curl-pipe-sh, and
// stops if tailscale has not published packages for this release yet.
const installTailscale = `set -euo pipefail
if command -v tailscale >/dev/null; then
  tailscale version
  exit 0
fi
. /etc/os-release
base=https://pkgs.tailscale.com/stable/ubuntu
if ! curl -fsI "$base/${VERSION_CODENAME}.noarmor.gpg" >/dev/null; then
  echo "tailscale publishes no packages for ubuntu ${VERSION_CODENAME} yet" >&2
  exit 1
fi
curl -fsSL "$base/${VERSION_CODENAME}.noarmor.gpg" \
  -o /usr/share/keyrings/tailscale-archive-keyring.gpg
curl -fsSL "$base/${VERSION_CODENAME}.tailscale-keyring.list" \
  -o /etc/apt/sources.list.d/tailscale.list
apt-get update
apt-get install -y tailscale
echo "enrol it with: sudo tailscale up --ssh"
`

// rebootIfNeeded detaches the reboot so ssh closes cleanly instead of the
// connection dying mid-command and looking like a failure.
const rebootIfNeeded = `set -euo pipefail
if [ -f /var/run/reboot-required ]; then
  echo "rebooting"
  systemd-run --on-active=2 systemctl reboot >/dev/null
else
  echo "no reboot required"
fi
`

const makeConfigDir = `set -euo pipefail
install -d -o %s -g %s -m 0755 %s
`

const fetchConfig = `set -euo pipefail
dir=%s
if [ -d "$dir/.git" ]; then
  git -C "$dir" pull --ff-only
else
  git clone --depth 1 %s "$dir"
fi
git -C "$dir" --no-pager log -1 --oneline
`

// runMgmt converges once and exits rather than staying up as a daemon. den's
// root layer does not drift on its own, so continuous watching buys nothing
// and a long lived agent is one more thing to reason about.
const runMgmt = `set -euo pipefail
cd %s/mgmt
mgmt run --converger-timeout 5 --converged-exit%s lang main.mcl
`
