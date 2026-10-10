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
