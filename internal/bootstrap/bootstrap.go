// Package bootstrap brings a freshly installed den up to the point where mgmt
// owns the host layer, and then runs mgmt. It is two passes on purpose: the
// first pass puts mgmt on the host, the second converges config and can be
// repeated forever.
package bootstrap

import (
	"context"
	"fmt"
	"io"
	"time"

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

// DefaultTailscaleTag is the policy tag den advertises when it enrols. The
// tailnet policy matches CI's reach on this exact string, so changing it here
// means changing the tailnet stack too.
const DefaultTailscaleTag = "tag:den"

// Options are the knobs both passes share.
type Options struct {
	User        string
	MgmtVersion string
	ConfigRepo  string
	ConfigDir   string

	// Terminfo is the operator's own terminal description, as infocmp -x
	// emits it. Empty skips the step.
	Terminfo string

	// TailscaleKey is den's OAuth client secret, which tailscale up accepts as
	// an auth key. Empty skips the join, which is what happens when the
	// operator has no gcloud credentials.
	TailscaleKey string
	TailscaleTag string

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
		{Name: "check the install", Root: true, Script: checkInstall},
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
	return append(steps, Step{
		Name:   "remove the installer's old root tree",
		Root:   true,
		Script: removeOldTree,
	})
}

// ApplyRoot is the first half of the second pass: fetch the config repo and
// converge the host layer. Running it again is how den reconciles by hand.
func ApplyRoot(o Options) []Step {
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

// ApplyUser converges the user layer. It runs after the join, the key seal
// and any reboot, because its quadlets need what those bring: the GPU's CDI
// spec only exists once the NVIDIA module has loaded.
func ApplyUser(o Options) []Step {
	noop := ""
	if o.Noop {
		noop = " --noop"
	}
	return []Step{{
		Name:   "converge the user layer",
		Script: fmt.Sprintf(runUserMgmt, o.ConfigDir, noop),
	}}
}

// JoinTailnet enrols den with its OAuth client secret.
func JoinTailnet(o Options) Step {
	return Step{
		Name:   "join the tailnet",
		Root:   true,
		Script: fmt.Sprintf(joinTailnet, o.TailscaleTag, o.TailscaleKey),
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
		// Timed because the steps are opaque while they run: mgmt alone can
		// sit silent for minutes, and without this there is nothing to blame.
		start := time.Now()
		if err := run(ctx, s.Script); err != nil {
			return fmt.Errorf("%s: %w", s.Name, err)
		}
		if s.Wait {
			if err := h.Wait(ctx); err != nil {
				return fmt.Errorf("%s: %w", s.Name, err)
			}
		}
		if _, err := fmt.Fprintf(
			log,
			"done in %s\n",
			time.Since(start).Round(time.Second),
		); err != nil {
			return err
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

// checkInstall stops bootstrap if the install did not take the shape den
// expects. It runs before anything is installed, so a bad install is found
// while it is still cheap to redo. The data drives are listed, not checked:
// only the operator knows what they held before.
const checkInstall = `set -euo pipefail
fail=0
check() {
  if bash -c "$2" >/dev/null 2>&1; then
    printf 'ok    %s\n' "$1"
  else
    printf 'FAIL  %s\n' "$1"
    fail=1
  fi
}
check "/ is the @ subvolume" 'findmnt -no OPTIONS / | grep -q "subvol=/@"'
check "swap is /swap/swapfile" 'swapon --noheadings --show=NAME | grep -qx /swap/swapfile'
check "the timezone is America/Los_Angeles" 'test "$(timedatectl show -p Timezone --value)" = America/Los_Angeles'
check "NTP is on" 'test "$(timedatectl show -p NTP --value)" = yes'
check "den-tpm-enroll finished" '! systemctl is-enabled --quiet den-tpm-enroll.service && test ! -e /etc/den/luks.key'
check "the TPM unlocks the root volume" 'grep -q tpm2-device=auto /etc/crypttab'
check "no unit failed" 'test -z "$(systemctl --failed --plain --no-legend)"'
if [ "$fail" -ne 0 ]; then
  systemctl --failed --plain --no-legend || true
  journalctl -b -u den-tpm-enroll.service --no-pager | tail -n 20 || true
fi
echo
lsblk -e 7 -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINT
exit "$fail"
`

// removeOldTree deletes the pre-subvolume tree that the installer leaves at
// the top of the filesystem as a fallback. Bootstrap only reaches this step
// once den has booted from @, so the fallback is no longer needed. Every
// subvolume den makes starts with @, so a later run cannot touch them.
const removeOldTree = `set -euo pipefail
findmnt -no OPTIONS / | grep -q 'subvol=/@'
top=$(mktemp -d)
mount -o subvolid=5 /dev/mapper/den-root "$top"
trap 'umount "$top"; rmdir "$top"' EXIT
if [ -z "$(find "$top" -mindepth 1 -maxdepth 1 ! -name '@*' -print -quit)" ]; then
  echo "nothing to remove"
  exit 0
fi
find "$top" -mindepth 1 -maxdepth 1 ! -name '@*' -printf 'removing %f\n' -exec rm -rf {} +
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
// The asset hangs off a den release, but not necessarily the newest one: the
// build takes minutes, so a release is newest for a while before its binary
// lands. Walk back until a release that has it, rather than 404ing on timing.
const installMgmt = `set -euo pipefail
want=%s
if [ "$(mgmt --version 2>/dev/null | grep -o '[0-9][0-9.]*' | head -1 || true)" = "$want" ]; then
  echo "mgmt $want already installed"
  exit 0
fi
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
bin=mgmt-linux-amd64-${want}
found=
for tag in $(curl -fsSL https://api.github.com/repos/dmikalova/den/releases?per_page=20 |
  grep -o '"tag_name": *"[^"]*"' | sed -E 's/.*"([^"]+)"$/\1/'); do
  base=https://github.com/dmikalova/den/releases/download/$tag
  if curl -fsSL -o "$tmp/$bin" "$base/$bin" && curl -fsSL -o "$tmp/sums" "$base/mgmt-SHA256SUMS"; then
    found=$tag
    break
  fi
done
if [ -z "$found" ]; then
  echo "no den release carries $bin" >&2
  exit 1
fi
echo "mgmt $want from den $found"
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
//
// Autoedges are skipped because the stage takes ~2 minutes on this config and
// the converger calls that quiet time "converged", so mgmt exited before any
// resource ran. Upstream disables it for the same reason (purpleidea/mgmt#840,
// #739). This only drops the inferred edges; the few explicit Depends in
// main.mcl still apply.
//
// The timeout is load bearing. --converged-exit only fires once the graph is
// actually converged, so a resource that keeps failing means mgmt never exits.
// It also holds etcd's port 2380, which makes every later run, including the
// scheduled reconcile, die on "address already in use" until someone kills it
// by hand. INT first because mgmt tears down etcd cleanly on it.
//
// It runs as a transient unit rather than as a child of the ssh session, which
// is how den-reconcile runs it. Two plain-ssh runs stalled on the package step
// for 48 minutes while the same converge under systemd finished in 2.5, so the
// session is a suspect, and a dropped session no longer takes mgmt with it.
// The journal keeps every line: reattach with journalctl -fu den-apply-root.
//
// The watch samples the package machinery every 30 seconds into a file, so a
// hang that does happen leaves a record of what was holding what.
//
// It takes den-reconcile's lock, because two mgmt runs cannot share etcd's
// port and the reconcile timer does not know a manual apply is in flight.
const runMgmt = `set -euo pipefail
unit=den-apply-root
runner=/run/den-apply-root.sh
install -d -m 0700 /var/log/den
watch=/var/log/den/apply-$(date +%%Y%%m%%dT%%H%%M%%S).watch

cat > "$runner" <<'DEN_APPLY_EOF'
set -euo pipefail
exec 9>/run/den-reconcile.lock
flock -w 900 9 || { echo "a reconcile held the lock for 15m, giving up" >&2; exit 1; }
sample() {
  while :; do
    {
      echo "== $(date -Is)"
      ps -eo pid,ppid,etimes,stat,wchan:30,args |
        grep -E '[a]pt|[d]pkg|[p]ackagekit|[u]nattended|[d]ebconf|[m]gmt run' || true
      fuser -v /var/lib/dpkg/lock-frontend /var/lib/dpkg/lock /var/lib/apt/lists/lock 2>&1 || true
      systemctl list-jobs --no-pager --no-legend || true
    } >>"$WATCH" 2>&1
    sleep 30
  done
}
sample &
exec timeout --signal=INT --kill-after=30s 30m \
  mgmt run --no-autoedges --converger-timeout 30 --converged-exit$NOOP lang ./metadata.yaml
DEN_APPLY_EOF

systemctl reset-failed "$unit" 2>/dev/null || true
journalctl -f -n0 -o cat -u "$unit" &
follow=$!
echo "converging as $unit; package watch in $watch"
rc=0
systemd-run --unit="$unit" --wait --collect --quiet --service-type=exec \
  --working-directory=%s/mgmt -E WATCH="$watch" -E NOOP="%s" \
  /bin/bash "$runner" || rc=$?
sleep 1
kill "$follow" 2>/dev/null || true
exit "$rc"
`

// runUserMgmt converges the user layer. It is not a root step: its resources
// are the login user's files and the login user's units, and root making them
// is how a rootless service ends up unable to start.
//
// It gets its own state prefix because two mgmt runs sharing one would fight
// over etcd, and the user cannot write the default prefix anyway.
const runUserMgmt = `set -euo pipefail
cd %s/mgmt/user
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
timeout --signal=INT --kill-after=30s 30m \
  mgmt run --no-autoedges --converger-timeout 30 --converged-exit%s \
  --prefix "$HOME/.local/state/mgmt" lang ./metadata.yaml
`

// joinTailnet enrols den once, after the converge step that installs the
// tailscale package. Already being up is the guard, so this is a no-op on
// every run after the first.
//
// --ssh is what lets CI in at all, and the tag is what the tailnet policy
// matches on. Neither is optional.
//
// The key goes via file: rather than inline, so it is never an argv entry that
// any local user could read out of /proc. /run is tmpfs, so the file it passes
// through never reaches a disk.
const joinTailnet = `set -euo pipefail
if ! command -v tailscale >/dev/null; then
  echo "tailscale is not installed yet; converge without --noop first"
  exit 0
fi
if tailscale status --json 2>/dev/null | grep -q '"BackendState": *"Running"'; then
  echo "already on the tailnet"
  exit 0
fi
umask 077
key=$(mktemp /run/den-authkey.XXXXXX)
trap 'rm -f "$key"' EXIT
printf '%%s' '%[2]s' > "$key"
tailscale up --ssh --advertise-tags=%[1]s --auth-key="file:$key"
tailscale status --peers=false
`

// TailnetState prints tailscaled's own view of itself, or {} before the
// package is installed.
const TailnetState = `if command -v tailscale >/dev/null; then
  tailscale status --json --peers=false 2>/dev/null || echo '{}'
else
  echo '{}'
fi
`

// RebootReason prints why den needs a reboot, or nothing. The NVIDIA check is
// its own line because installing the driver does not always ask for one, and
// without the module loaded there is no CDI spec for the GPU quadlets.
const RebootReason = `if [ -e /run/reboot-required ]; then
  echo "a package asked for it"
elif modinfo nvidia >/dev/null 2>&1 && [ ! -e /proc/driver/nvidia/version ]; then
  echo "the NVIDIA module is installed but not loaded"
fi
`

// BootID changes on every boot, which is how a reboot is told apart from ssh
// answering before the host has gone down.
const BootID = `cat /proc/sys/kernel/random/boot_id
`
