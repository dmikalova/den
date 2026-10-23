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
den usb build
diskutil list external
sudo den usb write --image ~/Downloads/den-autoinstall.iso --device /dev/diskX
```

The build downloads the Ubuntu ISO and verifies it against Canonical's signing
key. Pass `--iso <path>` to use a copy you already have.

See [docs/install.md](docs/install.md) for the full procedure.

Build a memtest86+ stick, to test the memory before the box holds data:

```bash
den usb memtest
sudo den usb write --image ~/Downloads/memtest86plus-8.10.iso --device /dev/diskX
```

See [docs/memtest.md](docs/memtest.md) for the full procedure.

Configure the host once the install has finished:

```bash
den bootstrap
den apply
```

`bootstrap` patches the fresh install and puts mgmt on it. `apply` converges
the root layer described in `mgmt/`, installs Tailscale and joins the tailnet,
and is safe to repeat.

See [docs/install.md](docs/install.md) for the full procedure.
