# den

Configuration and operational tooling for `den`, a single bare-metal home
server: NAS, music library, local inference host and photo archive.

The design lives in Notion. The procedures live in `docs/`. The vocabulary
lives in [CONTEXT.md](CONTEXT.md).

## Install

```bash
go install github.com/dmikalova/den/cmd/den@latest && den version
go install ./cmd/den/ && den version
```

## Usage

Build an unattended Ubuntu Server install image and write it to a USB drive:

```bash
den usb build --iso ~/Downloads/ubuntu-26.04.1-live-server-amd64.iso
diskutil list external
sudo den usb write --image ~/Downloads/den-autoinstall.iso --device /dev/diskX
```

See [docs/autoinstall.md](docs/autoinstall.md) for the full procedure.
