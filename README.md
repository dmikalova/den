# den

Configuration and operational tooling for `den`, a single bare-metal home
server: NAS, music library, local inference host and photo archive.

The design lives in Notion. The procedures live in `docs/`. The vocabulary
lives in [CONTEXT.md](CONTEXT.md).

## Install

```bash
go install github.com/dmikalova/den/v3/cmd/den@latest && den version
go install ./cmd/den/ && den version
```

## Usage

Build an unattended Ubuntu Server install image and write it to a USB drive:

```bash
den usb build
```

The build downloads the Ubuntu ISO and verifies it against Canonical's signing
key. Pass `--iso <path>` to use a copy you already have. It then lists the
external disks and asks which one to write.

Put the stick in den, and boot den from it one time:

```bash
den usb boot
```

See [docs/install.md](docs/install.md) for the full procedure.

Build a memtest86+ stick, to test the memory before the box holds data:

```bash
den usb memtest
```

See [docs/memtest.md](docs/memtest.md) for the full procedure.

Configure the host once the install has finished:

```bash
den bootstrap
```

`bootstrap` unlocks the first boot, patches the fresh install, puts mgmt on
it, and runs `apply`. It then proves the install and erases the installer
stick. `den apply` converges the root layer described in `mgmt/`, installs
Tailscale and joins the tailnet, and is safe to repeat.

See [docs/install.md](docs/install.md) for the full procedure.
