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

// Bootstrap is the first pass. It holds only what mgmt cannot do: assert the
// host is what den expects, install the handful of packages needed to reach a
// git remote, and put the mgmt binary on disk. Everything reconcilable belongs
// in mgmt/main.mcl and lands on the first Apply.
func Bootstrap(o Options) []Step {
	steps := []Step{
		{Name: "identify the host", Script: identify},
		{Name: "install base packages", Root: true, Script: patch},
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
	return steps
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

// patch installs only what the rest of bootstrap needs: curl fetches the mgmt
// binary, git clones the config repo, ca-certificates makes both verify, and
// the two library packages are what mgmt itself is linked against. The
// full-upgrade that used to live here is deliberately absent. It is slow, it
// can pull a kernel, and nothing downstream depends on it.
const patch = `set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y ca-certificates curl git
# Recommends off: mgmt wants the client libraries, not a hypervisor.
apt-get install -y --no-install-recommends libaugeas0 libvirt0
`

// installMgmt checks the binary against the sums file published with the
// release. den does not pipe an install script into a shell.
//
// The release is den's own, not upstream's: upstream builds one binary on
// Fedora and it needs libxml2.so.2, which Ubuntu 26.04 neither ships nor
// provides a compat package for. .github/workflows/mgmt.yaml rebuilds the same
// tag against 26.04. Recheck upstream on every bump and go back to theirs when
// it runs here.
//
// The asset hangs off den's newest release, so cutting a den release without
// rerunning mgmt.yaml leaves this 404ing.
const installMgmt = `set -euo pipefail
want=%s
if [ "$(mgmt --version 2>/dev/null | grep -o '[0-9][0-9.]*' | head -1 || true)" = "$want" ]; then
  echo "mgmt $want already installed"
  exit 0
fi
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
bin=mgmt-linux-amd64-${want}
base=https://github.com/dmikalova/den/releases/latest/download
curl -fsSL -o "$tmp/$bin" "$base/$bin"
curl -fsSL -o "$tmp/sums" "$base/mgmt-SHA256SUMS"
( cd "$tmp" && grep "  $bin\$" sums | sha256sum -c - )
install -m 0755 "$tmp/$bin" /usr/local/bin/mgmt
mgmt --version
`

// installTerminfo compiles the operator's terminal description system wide, so
// that a sudo session gets it too. Without this, anything that pages output
// warns that the terminal is not fully functional. ncurses-term 6.6 ships a
// ghostty entry but no xterm-ghostty alias, which is the TERM Ghostty sends,
// so this is still load bearing.
//
// TODO: this is config and belongs in mgmt, which would also stop den's own
// terminal leaking into the host's state. Two things block it: mgmt's exec cwd
// is unverified, so a committed terminfo file cannot be referenced relatively,
// and hardcoding /srv/den-config would defeat --config-dir. Settle it after the
// first apply shows how exec actually behaves.
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
//
// The entrypoint is metadata.yaml, not main.mcl: a naked mcl file cannot see
// the files/ directory that deploy.readfile reads from. The ./ is required;
// mgmt reads a bare name as raw code.
const runMgmt = `set -euo pipefail
cd %s/mgmt
mgmt run --converger-timeout 5 --converged-exit%s lang ./metadata.yaml
`
