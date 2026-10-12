# The root layer

`den apply` runs
`mgmt run --no-autoedges --converger-timeout 30 --converged-exit lang ./metadata.yaml`
on den, as root, from a clone of this repo at `/srv/den-config`.

`--no-autoedges` is mandatory. Auto-edge computation outlasts the converger's
quiescence window, so without the flag mgmt exits before applying anything and
still reports success.

`main.mcl` is the root layer. `metadata.yaml` is the entrypoint only because a
naked `.mcl` file cannot see `files/`.

## Why file contents live in files/

Config bodies and shell scripts are real files under `files/`, pulled in with
`deploy.readfile`. mcl interpolates `$name` and `${name}` inside strings, so an
inline body has to escape every dollar. That is not only tedious: the
`${distro_id}` in the unattended-upgrades policy is meant for apt, and had mcl
resolved it the result would have been a silently wrong policy. Real files also
let shellcheck read the scripts.

`files/` mirrors the destination path, so `files/etc/sudoers.d/den` lands at
`/etc/sudoers.d/den`. The exception is `files/scripts/`, which holds `exec`
bodies that are piped to bash and never written to disk.

## Why mgmt and not a dotfile manager

About half of den's root layer is not files. Packages, service enablement,
`loginctl enable-linger`, the NVIDIA CDI spec, `tailscale up`: a file renderer
cannot express any of those, and the failures are quiet. A missing
`enable-linger` does not error, the containers are simply gone after a reboot.

## Why one shot and not a daemon

mgmt's headline feature is continuous event driven convergence. den's root
layer does not drift on its own; it changes when someone edits this directory.
Running as a daemon would buy nothing and add a long lived agent to reason
about. `--converged-exit` converges once and leaves.

## What belongs here and what belongs in the install

Anything that shapes the disk belongs to `internal/autoinstall`, because it
cannot be changed later without unwinding the layout. Everything else belongs
here, because here it can be changed by editing a file and running `den apply`.

Swap is the clearest example of the line. The disk swapfile needs its own
subvolume created before the tree moves into `@`, so it is an install
concern. zram is a package and a config file, so it is a mgmt concern. See
[docs/autoinstall.md](../docs/autoinstall.md).

## Rules

- No secrets. Secrets come from Bitwarden at run time, never from this repo.
- Root only. Anything the login user can own belongs in their home, under
  their own systemd, in [user/](user/), not here.
- Every `exec` needs a `creates` or equivalent, or the run is not idempotent.

## The user layer

[user/](user/) is a second mgmt tree, converged as `dmikalova` straight after
this one. It holds the quadlets: the services den actually serves.

It is a separate run rather than a section of `main.mcl` because the two differ
in who they run as, and that is not something one graph can express. A file
this layer writes has to be owned by the login user, and a unit it starts has
to reach that user's systemd. Root making either of those is the reliable way
to end up with a rootless service that cannot start.

It is still mgmt, so there is one tool to learn, not two. What differs is the
invocation: its own `--prefix` under `~/.local/state/mgmt`, because two runs
sharing one state directory fight over etcd.

Two things about that environment are worth knowing before writing a script
here, because both fail late and quietly:

- mgmt gives `exec` a bare environment. `HOME` is unset, so `set -u` fails the
  script on the first line that uses it, which is usually the sentinel at the
  end, after the expensive work has already run.
- `systemctl --user` needs `XDG_RUNTIME_DIR`. Without it there is no bus.

Directory resources here take no `mode`. Setting one makes the resource flap:
mgmt chmods, sees its own change, and chmods again for the life of the run.

## Seeing what would change

```bash
den apply --noop
```

That runs mgmt for real and reports what it would do. `den apply --dry-run`
is different: it prints the shell den would run and contacts nothing.

Use `--noop` before any change to the firewall. The `ufw` policy denies
inbound by default, so a mistake there costs a trip to the physical console.
The LAN rule for port 22 is what keeps `den apply` itself working, because
bootstrap reaches den over the LAN and not over the tailnet.

## Reconciling

`main.mcl` installs a timer that runs `den-reconcile` daily: pull this
repository, converge, exit. That is the drift catcher, and it is the only
delivery path. Nothing outside the house pushes to den.

CI shortens the wait rather than replacing it. The `apply` workflow joins a
runner to the tailnet, runs `den-reconcile` over Tailscale SSH, and the runner
leaves. See [../docs/bootstrap.md](../docs/bootstrap.md).
