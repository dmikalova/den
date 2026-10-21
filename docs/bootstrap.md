# Bootstrap

Two passes take den from a finished install to a configured host. They are
separate because the first pass only puts mgmt on the host, and the second pass
is safe to repeat forever.

| Pass            | What it does                                             | Repeatable |
| --------------- | -------------------------------------------------------- | ---------- |
| `den bootstrap` | Install base packages, mgmt and your terminfo            | Once       |
| `den apply`     | Converge the config repo, join the tailnet, seal the key | Always     |

Both shell out to the `ssh` binary. Your `~/.ssh/config`, your agent and your
`known_hosts` apply, and den never does its own host key checking.

## Before the first pass

Add a Host block so a change of address only touches one file:

```sshconfig
Host den
  HostName 192.168.0.200
  User dmikalova
```

A reinstall gives den a new host key. Clear the old one first:

```bash
ssh-keygen -R den
```

## First pass

```bash
den bootstrap
```

It waits for ssh, prints what it found, then patches the system and installs
the pinned mgmt release. Steps that need root run under `sudo` with a terminal
attached, so sudo prompts for your password.

mgmt is installed from the GitHub release binary and checked against the sums
file published beside it. den does not pipe an install script into a shell.

One more step runs here: den compiles your own terminal description onto the
server. Without it anything that pages output warns that the terminal is not
fully functional, because the server has never heard of your terminal. den
reads the description from whatever `$TERM` names locally, so this works for
any terminal and carries no hardcoded list.

Tailscale is not installed yet. mgmt installs it during `den apply`, and
`den apply` then enrols the node itself with the OAuth client from the
`tailscale-den` secret and `tag:den`. Do not run `tailscale up` by hand here:
the binary is not on the host.

The tag matters twice. A tagged node's key never expires, so den is not
quietly evicted from the tailnet after six months, and the ACL rule that lets
CI in is written against the tag rather than against one machine.

## Second pass

```bash
den apply
```

It runs these in order:

1. Clone or pull `/srv/den-config` from GitHub.
2. Converge `mgmt/main.mcl`, the root layer.
3. Join the tailnet, if den is not on it yet.
4. Seal den's service account key to the TPM.
5. Reboot, if a package asked for one or the NVIDIA module is not loaded,
   and wait for den to come back. `--no-reboot` skips this.
6. Converge `mgmt/user/main.mcl`, the user layer. It runs last because its
   GPU quadlet needs the CDI spec that only exists after the module loads.

Run it again whenever the config repo moves. The `reconcile` timer on den
converges both layers on a schedule, but never reboots.

The root layer converges as the transient unit `den-apply-root`, not as a
child of the ssh session, and output streams back from the journal. If the
session drops, the converge carries on: reattach with
`journalctl -fu den-apply-root`. Every 30 seconds it also writes what apt,
dpkg, PackageKit and unattended-upgrades are doing, and who holds the dpkg
locks, to `/var/log/den/apply-<time>.watch`. Read that file first if the
package step hangs.

## Letting CI reach den

den is behind a home NAT and nothing is forwarded to it, so a hosted runner
cannot push to it. Instead the runner joins the tailnet for the length of a
job and asks den to reconcile early. Delivery stays a pull either way; the
workflow only shortens the wait.

The tailnet policy, CI's federated identity and den's OAuth client all live in
the infrastructure repo, in `tailscale/tailnet`. The policy declares `tag:ci`
and `tag:den`, lets `tag:ci` reach `tag:den` over SSH as root, and nothing
else. Change it there, not in the admin console.

den's OAuth client never expires, so an enrolment months from now still works.
Its secret is the auth key, and the client is limited to `tag:den`.

## Reinstalling

The new install is a new tailnet node. Its node key was sealed to the old
root and is gone with it. `den apply` sees a tailscaled with no identity,
deletes the disconnected `tag:den` node with the same hostname, then joins, so
the new install takes the name `den` rather than `den-1`.

After `den apply` joins, apply `tailscale/tailnet` again, then the DNS stack
that reads its den address outputs, so `*.den.mklv.tech` resolves to the new
tailnet address.

## Seeing what would happen

```bash
den bootstrap --dry-run
den apply --dry-run
```

Every script is printed and nothing is run. den contacts nothing.

```bash
den apply --noop
```

That is different: it runs mgmt for real and reports what it would change.
Use `--dry-run` to check den, and `--noop` to check the config.

## The root layer

The mcl source lives in this repository under `mgmt/`. It covers packages,
mounts, services and the handful of one shot root enablements that rootless
Podman depends on. See [mgmt/README.md](../mgmt/README.md) for why that is a
config management tool rather than a dotfile manager, and why it runs once
rather than as a daemon.
