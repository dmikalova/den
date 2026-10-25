# den

Conventions for this repository.

## Validation

Run `mage ci:fix && mage ci:check` before calling work done. It must print
`ALL GREEN`.

## Package layout

| Package                | Role                                                     |
| ---------------------- | -------------------------------------------------------- |
| `cmd/den`              | The command line: `usb`, and the operational subcommands |
| `internal/autoinstall` | The Subiquity seed: user-data, GRUB patch, ISO remaster  |
| `internal/bootstrap`   | The two passes that configure a freshly installed den    |
| `internal/check`       | The assertions behind `den check`                        |
| `internal/fetch`       | Downloading a file over HTTP and checking its sum        |
| `internal/gcp`         | Reading Secret Manager with a service account key        |
| `internal/memtest`     | Fetching the memtest86+ boot image                       |
| `internal/notify`      | Posting a Discord embed and pinging a healthcheck        |
| `internal/progress`    | Progress reporting for the long-running commands         |
| `internal/remote`      | Running a script on den over ssh                         |
| `internal/secrets`     | The key names den reads out of the GCP secrets           |
| `internal/storage`     | The storage layout that the tests and `den check` use    |
| `internal/tailnet`     | Clearing a reinstall's stale node from the tailnet       |
| `internal/usbdisk`     | Finding a removable disk and writing an image to it      |

## Rules

- **The autoinstall never touches the hard drives.** The storage stanza pins
  the install to the NVMe by path. Any change to that stanza is a review stop.
- **Secrets never enter the repository.** The USB builder takes the password
  hash and the SSH public key at build time.
  `internal/autoinstall/user-data.tmpl` holds placeholders only.
- **den holds one credential and fetches the rest.** A service account key,
  sealed to the host TPM by `den secrets seal`, is the only secret ever
  delivered to den. Everything else comes from the `den-host` secret, which
  `den-secrets.service` explodes into `/run/den/secrets`. A unit names what it
  needs with `LoadCredential=`, so the unit file is the allowlist. Do not add
  a path that reads a secret some other way.
- **Data goes in a storage area, never in a new subvolume.** Read
  [docs/storage.md](docs/storage.md) before you add a quadlet or a path that
  holds data. Each quadlet `Volume=` source is under `/srv/appdata`,
  `/srv/db`, `/srv/db-dumps`, `/srv/cache` or `/mnt/storage`. No named
  volumes. `internal/storage` tests this.
- **No self-hosted service is a bootstrap dependency of the host that runs
  it.** Config comes from GitHub. The secrets root is Bitwarden.
- **Nothing is trusted until tested.** Drives burn in before they hold data.
  Backups restore-test before data lands.

## Writing to a disk

`den usb write` refuses an internal disk and requires `--yes`. Do not add a
flag that removes that check. The write prompt in `den usb build` offers only
disks that pass the same check, and `den usb write` checks again.

`den bootstrap` erases the installer stick on den. It matches a USB disk with
an ISO 9660 `Ubuntu-Server` label, and only one. Do not loosen that match.

## Reaching den

`internal/remote` shells out to the `ssh` binary so the operator's ssh config,
agent and known_hosts apply. Do not replace it with an ssh library: that moves
host key verification into den, and the usual next step is disabling it.

## Configuring den

`mgmt/main.mcl` is the whole root layer. `den apply` converges it once and
exits; mgmt is not run as a daemon, because the root layer only changes when
someone edits the file.

- Root only. Anything the login user can own belongs in their home, under
  their own systemd. den's services are rootless Podman quadlets.
- Every `exec` resource needs a `creates` or equivalent guard, or the run
  stops being idempotent.
- Every `exec` resource sets `Meta:retry => 3` and `Meta:delay => 15000`.
  mgmt's default is to retry a failing resource forever, which holds the
  reconcile open until `TimeoutStartSec` kills it half an hour later and no
  one learns which resource failed.
- `den apply --dry-run` prints the shell and contacts nothing.
  `den apply --noop` runs mgmt and reports what it would change. They are not
  the same check.
