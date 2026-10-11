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
| `internal/fetch`       | Downloading a file over HTTP and checking its sum        |
| `internal/memtest`     | Fetching the memtest86+ boot image                       |
| `internal/remote`      | Running a script on den over ssh                         |
| `internal/usbdisk`     | Finding a removable disk and writing an image to it      |

## Rules

- **The autoinstall never touches the hard drives.** The storage stanza pins
  the install to the NVMe by path. Any change to that stanza is a review stop.
- **Secrets never enter the repository.** The USB builder takes the password
  hash and the SSH public key at build time.
  `internal/autoinstall/user-data.tmpl` holds placeholders only.
- **No self-hosted service is a bootstrap dependency of the host that runs
  it.** Config comes from GitHub. The secrets root is Bitwarden.
- **Nothing is trusted until tested.** Drives burn in before they hold data.
  Backups restore-test before data lands.

## Writing to a disk

`den usb write` refuses an internal disk and requires `--yes`. Do not add a
flag that removes that check.

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
- `den apply --dry-run` prints the shell and contacts nothing.
  `den apply --noop` runs mgmt and reports what it would change. They are not
  the same check.
