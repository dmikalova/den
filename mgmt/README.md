# The root layer

`den apply` runs
`mgmt run --converger-timeout 30 --converged-exit lang ./metadata.yaml` on den,
as root, from a clone of this repo at `/srv/den-config`.

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
  their own systemd, not here.
- Every `exec` needs a `creates` or equivalent, or the run is not idempotent.

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
