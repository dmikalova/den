# Home tree

The chezmoi source for the login account on den. The destination is that
account's home directory.

This tree is deliberately thin. den is a server, not a workstation: the shell
config, the editor and the desktop tooling live in the `dotfiles` repository
and are not installed here.

```text
dot_config/containers/     rootless podman settings
dot_config/kopia/
```

`den apply` runs this tree as the login user:

```bash
chezmoi apply --source /srv/den-config/chezmoi/home --destination "$HOME"
```

## Rules

- Nothing here may contain a secret.
- Quadlets that must run rootless still live in the root tree when systemd
  needs them at `/etc/containers/systemd/users`.
- `README.md` is listed in `.chezmoiignore` so it never lands on the host.
