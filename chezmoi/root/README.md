# Root tree

The chezmoi source for files den owns as root. The destination is `/`, so a
path here is the path on den with the leading slash removed.

```text
etc/systemd/system/      quadlet units and timers
etc/snapraid.conf
etc/default/
usr/local/sbin/
```

`den apply` runs this tree under sudo:

```bash
sudo chezmoi apply --source /srv/den-config/chezmoi/root --destination /
```

## Rules

- Nothing here may contain a secret. Templates pull from Bitwarden at apply
  time.
- A file here is a file den reconciles. If a package owns the file and the
  default is right, leave it out.
- `README.md` is listed in `.chezmoiignore` so it never lands on the host.
