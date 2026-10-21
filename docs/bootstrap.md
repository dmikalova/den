# Bootstrap

Two passes take den from a finished install to a configured host. They are
separate because the first pass reboots and needs a human at the keyboard for
sudo, and the second pass is safe to repeat forever.

| Pass            | What it does                              | Repeatable |
| --------------- | ----------------------------------------- | ---------- |
| `den bootstrap` | Patch, install mgmt and tailscale, reboot | Once       |
| `den apply`     | Fetch the config repo and converge it     | Always     |

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
`den apply` then enrols the node itself with the auth key from the
`tailscale-den` secret and `tag:den`. Do not run `tailscale up` by hand here:
the binary is not on the host.

The tag matters twice. A tagged node's key never expires, so den is not
quietly evicted from the tailnet after six months, and the ACL rule that lets
CI in is written against the tag rather than against one machine.

## Second pass

```bash
den apply
```

It clones `/srv/den-config` from GitHub, then converges `mgmt/main.mcl` under
sudo. Run it again whenever the config repo moves. The `reconcile` timer on
den does the same thing on a schedule.

## Letting CI reach den

den is behind a home NAT and nothing is forwarded to it, so a hosted runner
cannot push to it. Instead the runner joins the tailnet for the length of a
job and asks den to reconcile early. Delivery stays a pull either way; the
workflow only shortens the wait.

Do this once, in the Tailscale admin console:

1. Declare the tags under Access controls, or nothing may claim them:

   ```json
   "tagOwners": {
     "tag:ci":     ["autogroup:admin"],
     "tag:server": ["autogroup:admin"]
   }
   ```

2. Let CI open an SSH session, and keep your own access separate:

   ```json
   "ssh": [
     {
       "action": "accept",
       "src":    ["tag:ci"],
       "dst":    ["tag:server"],
       "users":  ["root"]
     },
     {
       "action": "check",
       "src":    ["autogroup:member"],
       "dst":    ["tag:server"],
       "users":  ["autogroup:nonroot", "root"]
     }
   ]
   ```

   `accept` is unavoidable for CI, which has no browser to answer a `check`
   with. Narrow it by keeping `tag:ci` to this one destination.

3. Generate an OAuth client under Settings, with the `auth_keys` write scope
   and the `tag:ci` tag. The secret is shown once. Put the two halves in the
   repository as `TS_OAUTH_CLIENT_ID` and `TS_OAUTH_SECRET`.

An OAuth client is used rather than an auth key because auth keys expire after
at most 90 days, and a credential that rots silently fails long after anyone
remembers it exists. The client mints a short lived key per job instead.

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
