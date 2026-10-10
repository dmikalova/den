# Vocabulary

The words this repository uses, and the ones it avoids. Use these in code,
comments and commit messages.

## The host

**den** — the machine. One bare-metal host. It is `den` in MagicDNS and
`den` in every config file.
_Avoid:_ the NAS, the server, the box.

**host layer** — what runs directly on den: chezmoi, Tailscale, Podman,
mergerfs, SnapRAID, snapper, smartd, Kopia, Netdata, Samba, Caddy, the NVIDIA
driver, and the `den` binary. Everything else is a quadlet.

**quadlet** — a rootless Podman container declared as a systemd unit.
_Avoid:_ container, service, app, when the quadlet is meant.

## Installing

**autoinstall** — Subiquity's unattended install, driven by a cloud-init
`user-data` document. The schema version is 1.
_Avoid:_ preseed, kickstart, unattended install.

**seed** — the `user-data` and `meta-data` pair that cloud-init reads. A seed
is discovered either from a volume labelled `CIDATA` or from a path given on
the kernel command line.

**image** — the remastered ISO that `den usb build` produces. It carries the
seed and a patched GRUB configuration.
_Avoid:_ ISO, when the remastered one is meant. The input is the **source
ISO**.

**target disk** — the NVMe that Ubuntu installs onto. The autoinstall pins it
by path so the hard drives are never candidates.
_Avoid:_ boot drive, system disk.

## Storage

**array** — the four hard drives together: one parity disk and three data
disks, pooled by mergerfs at `/mnt/storage`.

**parity** — the SnapRAID parity disk. It is not a RAID member and it is not
a backup.

**burn-in** — the destructive write-verify pass and SMART long test every
drive passes before it holds data. Gate 1.

**restore drill** — pulling a file back from the offsite repository and
diffing it. Gate 2.
_Avoid:_ backup test.

## Operating

**notify** — one call that posts a Discord embed and pings the matching
healthcheck. Every scheduled job ends in a notify.

**healthcheck** — the dead-man's-switch endpoint on Cloud Run. Jobs ping on
success. Silence raises the alarm.
_Avoid:_ heartbeat, ping, monitor.

**reconcile** — the daily `chezmoi update` timer that catches drift. Its job
is detection, not speed.
_Avoid:_ sync, when reconcile is meant. **sync** is SnapRAID's parity update.
