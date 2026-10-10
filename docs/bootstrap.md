# Bootstrap

Two passes take den from a finished install to a configured host. They are
separate because the first pass reboots and needs a human at the keyboard for
sudo, and the second pass is safe to repeat forever.

| Pass            | What it does                                  | Repeatable |
| --------------- | --------------------------------------------- | ---------- |
| `den bootstrap` | Patch, install chezmoi and tailscale, reboot  | Once       |
| `den apply`     | Fetch the config repo and run chezmoi         | Always     |

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

It waits for ssh, prints what it found, then patches the system, installs the
pinned chezmoi release and tailscale, and reboots if the kernel moved. Steps
that need root run under `sudo` with a terminal attached, so sudo prompts for
your password.

chezmoi is installed from the GitHub release `.deb` and checked against the
checksums file published beside it. den does not pipe an install script into a
shell.

Tailscale is installed but not enrolled. Finish that by hand once:

```bash
ssh den sudo tailscale up --ssh
```

## Second pass

```bash
den apply
```

It clones `/srv/den-config` from GitHub, then runs chezmoi twice: the home
tree as the login user, and the root tree under sudo. Run it again whenever
the config repo moves. The `reconcile` timer on den does the same thing on a
schedule.

## Seeing what would happen

```bash
den bootstrap --dry-run
den apply --dry-run
```

Every script is printed and nothing is run.

## The two trees

The chezmoi source lives in this repository under `chezmoi/`:

- `chezmoi/root` applies to `/`, as root. Quadlets, systemd units, `/etc`.
- `chezmoi/home` applies to the login account's home directory.

Two trees rather than one because the root tree needs sudo and the home tree
does not, and because chezmoi keeps its state next to the user that runs it.
