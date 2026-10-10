// Package bootstrap brings a freshly installed den up to the point where
// chezmoi owns the host layer, and then runs chezmoi. It is two passes on
// purpose: the first pass patches and reboots, the second applies config and
// can be repeated forever.
package bootstrap

import (
	"context"
	"fmt"
	"io"

	"github.com/dmikalova/den/internal/remote"
)

// DefaultChezmoiVersion is the chezmoi release den installs. The .deb is
// checked against the checksums file published beside it, so this is pinned
// rather than tracking latest.
const DefaultChezmoiVersion = "2.73.0"

// DefaultConfigRepo is the git remote holding the chezmoi source trees.
const DefaultConfigRepo = "https://github.com/dmikalova/den.git"

// DefaultConfigDir is where that remote is cloned on den.
const DefaultConfigDir = "/srv/den-config"

// Options are the knobs both passes share.
type Options struct {
	User           string
	ChezmoiVersion string
	ConfigRepo     string
	ConfigDir      string
}

// Step is one script, run either as the login user or under sudo.
type Step struct {
	Name   string
	Root   bool
	Script string

	// Wait holds the run until ssh answers again, for steps that reboot.
	Wait bool
}

// Bootstrap is the first pass: identify the host, patch it, install chezmoi
// and tailscale, reboot if the kernel moved.
func Bootstrap(o Options) []Step {
	return []Step{
		{Name: "identify the host", Script: identify},
		{Name: "patch and install base packages", Root: true, Script: patch},
		{
			Name:   "install chezmoi " + o.ChezmoiVersion,
			Root:   true,
			Script: fmt.Sprintf(installChezmoi, o.ChezmoiVersion),
		},
		{Name: "install tailscale", Root: true, Script: installTailscale},
		{Name: "reboot if required", Root: true, Script: rebootIfNeeded, Wait: true},
	}
}

// Apply is the second pass: fetch the config repo and run chezmoi over both
// trees. Running it again is how den reconciles by hand.
func Apply(o Options) []Step {
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
			Name:   "apply the home tree",
			Script: fmt.Sprintf(applyHome, o.ConfigDir),
		},
		{
			Name:   "apply the root tree",
			Root:   true,
			Script: fmt.Sprintf(applyRoot, o.ConfigDir),
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

// installChezmoi verifies the .deb against the checksums file published with
// the release. den does not pipe an install script into a shell.
const installChezmoi = `set -euo pipefail
want=%s
have=$(chezmoi --version 2>/dev/null | grep -o 'v[0-9.]*' | head -1 | tr -d v || true)
if [ "$have" = "$want" ]; then
  echo "chezmoi $want already installed"
  exit 0
fi
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
deb=chezmoi_${want}_linux_amd64.deb
base=https://github.com/twpayne/chezmoi/releases/download/v${want}
curl -fsSL -o "$tmp/$deb" "$base/$deb"
curl -fsSL -o "$tmp/sums" "$base/chezmoi_${want}_checksums.txt"
( cd "$tmp" && grep "  $deb\$" sums | sha256sum -c - )
apt-get install -y "$tmp/$deb"
chezmoi --version
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

const applyHome = `set -euo pipefail
chezmoi apply --source %s/chezmoi/home --destination "$HOME"
`

const applyRoot = `set -euo pipefail
chezmoi apply --source %s/chezmoi/root --destination /
`
