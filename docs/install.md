# Install

This procedure takes den from a blank NVMe to a host that passes `den check`.
Use it for the first install and for every reinstall. It never touches the
four data drives.

## Quickstart

Run these in order on the Mac. Each step is explained in [Steps](#steps).

```sh
# 0. Reinstall only: take a last backup and parity sync
ssh dmikalova@den.local sudo systemctl start --wait kopia-backup.service snapraid-sync.service

# 1. Build the image
go install github.com/dmikalova/den/cmd/den@latest
den usb build

# 2. Write the USB stick
diskutil list external
sudo den usb write --image ~/Downloads/den-autoinstall.iso --device /dev/diskN --yes

# 3. Boot den from the stick. Wait for it to reboot.
# 4. Type the LUKS passphrase on den's console, once.

# 5. Check the install
ssh dmikalova@den.local
lsblk -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINT
systemctl --failed
findmnt -no SOURCE,OPTIONS / && swapon --show
timedatectl && date
systemctl status den-tpm-enroll.service
sudo reboot            # must come back with no passphrase prompt

# 6. Configure den
den bootstrap
den apply

# 7. Check den
ssh dmikalova@den.local sudo den notify --title install "den is up"
ssh dmikalova@den.local sudo systemctl start --wait kopia-backup.service
den check

# 8. Erase the USB stick
sudo diskutil zeroDisk /dev/diskN
```

## Steps

### 0. Take a last backup

Reinstall only. The NVMe holds `/srv/appdata`, which kopia backs up but
nothing restores automatically. A last run keeps the loss to zero. The parity
sync makes the array consistent before den goes offline.

### 1. Build the image

`den usb build` downloads the Ubuntu Server ISO into `~/Downloads`. It checks
the ISO against Canonical's signed `SHA256SUMS`, and stops if the signature
does not verify. It then writes `~/Downloads/den-autoinstall.iso`.

The build needs `xorriso`, `gpg` and `gcloud`. If `gpg` does not have the
Ubuntu signing key, add it:

```sh
gpg --keyserver keyserver.ubuntu.com \
  --recv-keys 843938DF228D22F7B3742BC0D94AA3F0EFE21092
```

The build reads the `den-install` secret in GCP Secret Manager. Only a project
admin can read it, so den itself never can.

| Key                    | Use                                              |
| ---------------------- | ------------------------------------------------ |
| `PASSWORD`             | Console and sudo password. Short enough to type. |
| `LUKS_PASSPHRASE`      | Unlocks the root volume. Long and random.        |
| `SSH_HOST_ED25519_KEY` | den's SSH host key, the same on every install    |
| `GCP_KEY`              | den's service account key. `den apply` uses it.  |

**Caution: keep `PASSWORD` and `LUKS_PASSPHRASE` different.** The LUKS
passphrase is written in clear on the stick.

Pass `--iso <path>` to use an ISO that you already have. Pass
`--password-file` or `--luks-passphrase-file` to build without GCP.

### 2. Write the USB stick

**Caution: the write erases the whole disk.** `den usb write` refuses an
internal disk, a disk that is not removable, and a partition. It cannot tell
one USB stick from another. Read the printed name and size before you pass
`--yes`.

The write reads the stick back and compares its SHA256 with the image.

### 3. Install

Boot den from the stick. The installer asks no questions. It installs to
`/dev/nvme0n1`, which it selects by path, so the data drives are never
candidates. It then reboots.

### 4. Unlock once

The first boot asks for the LUKS passphrase on the console. From the second
boot, the TPM unlocks the disk.

### 5. Check the install

den answers to `den.local` over mDNS. You do not need an SSH config entry.
If mDNS does not answer, get the address from the console with
`ip -4 -br addr`.

SSH does not ask about the host key, because every install has the same key:
`SHA256:iSvJqB6Q5lDwLQ30xz9bZhsKbvoYJA38rS5jz3jurcU`. **If SSH warns that the
host key changed, stop.** The install did not take the key. Find out why
before you accept anything.

Make sure that:

- `lsblk` shows the four data drives as they were: empty on a first install,
  with their filesystems on a reinstall.
- `systemctl --failed` shows no units.
- `findmnt` shows `subvol=/@`, and `swapon` shows `/swap/swapfile`.
- `timedatectl` shows `America/Los_Angeles` with NTP active, and `date` shows
  a 24 hour time.
- `den-tpm-enroll.service` succeeded.
- den reboots with no passphrase prompt.

`den-tpm-enroll.service` runs once. It binds a LUKS key slot to TPM PCR 7 and
shreds the passphrase that the installer left in `/etc/den/luks.key`. If there
is no usable TPM, it logs that and leaves the passphrase as the only unlock.
If it fails for an unexpected reason, it keeps the key file and tries again
on the next boot.

**Keep the LUKS passphrase in Bitwarden.** A firmware update changes PCR 7,
and then den asks for the passphrase again.

The installer leaves the pre-subvolume tree at the top level of the
filesystem as a fallback. After the reboot works, delete it:

```sh
sudo mkdir -p /mnt/top
sudo mount -o subvolid=5 /dev/mapper/den-root /mnt/top
ls /mnt/top                     # expect @, @swap, and the old tree
sudo find /mnt/top -mindepth 1 -maxdepth 1 ! -name @ ! -name @swap -exec rm -rf {} +
sudo umount /mnt/top && sudo rmdir /mnt/top
```

### 6. Configure den

`den bootstrap` runs once. It patches the system, installs the pinned mgmt
release and checks it against the published sums, and copies your terminal
description to den. Steps that need root ask for the sudo password.

`den apply` is safe to run at any time. In order, it:

1. Clones or pulls `/srv/den-config` from GitHub. Push before you apply.
2. Converges the root layer, `mgmt/main.mcl`.
3. Joins the tailnet with the `tailscale-den` OAuth client and `tag:den`.
4. Seals `GCP_KEY` to the TPM, so den can fetch its own secrets.
5. Reboots if a package or the NVIDIA module needs it, and waits.
6. Converges the user layer, `mgmt/user/main.mcl`.

**Do not run `tailscale up` by hand.** Tailscale is not installed until step
2.

On a reinstall, step 3 deletes the old, disconnected `den` node, so the new
node takes the name `den` and not `den-1`. It then gives the new node the old
node's IPv4 address, so the `*.den.mklv.tech` records stay correct. If that
step warns, apply `den/dns` in the infrastructure repo.

The root converge runs as the transient unit `den-apply-root`. If the SSH
session drops, the converge continues. Reattach with
`journalctl -fu den-apply-root`. If the package step hangs, read
`/var/log/den/apply-<time>.watch`.

Both commands use `dmikalova@den.local`. Off the LAN, pass `--host den` to go
through Tailscale.

### 7. Check den

`den notify` must reach Discord. If it does not, nothing later tells you
when something breaks. Fix it before you continue. `den notify` needs root,
because `/etc/den` is `0700`.

The kopia run connects den to the existing B2 repository. Without it, the
`backup repository connected` check fails until the 22:00 timer.

`den check` makes 23 assertions in one SSH round trip. It is the signoff.

### 8. Erase the USB stick

The stick holds the LUKS passphrase in clear.

## Previews

```sh
den bootstrap --dry-run
den apply --dry-run     # prints every script, contacts nothing
den apply --noop        # runs mgmt and reports what it would change
```

## CI and the tailnet

den is behind NAT and has no forwarded ports. A CI runner joins the tailnet
for the length of a job and asks den to reconcile early. The tailnet policy,
CI's federated identity and den's OAuth client are in the infrastructure
repo, in `tailscale/tailnet`. Change them there, not in the admin console.

`tag:den` makes den's node key never expire. The ACL lets `tag:ci` reach
`tag:den` over SSH as root, and nothing else.

## How the install works

The image carries the seed in `/nocloud`, and a GRUB entry with
`autoinstall ds=nocloud;s=/cdrom/nocloud/`. The `autoinstall` parameter makes
Subiquity skip its confirmation prompt.

The seed:

- Makes a 1 GB EFI partition, a 2 GB ext4 `/boot`, and gives the rest to a
  LUKS2 volume that holds btrfs.
- Creates one account: password login on the console, key-only login over
  SSH.
- Sets `America/Los_Angeles` and `LC_TIME=en_DK.UTF-8`, for a 24 hour clock
  and ISO 8601 dates.
- Installs `curl`, `git` and `gnupg`, and nothing else. mgmt does the rest.

Rule: **anything that shapes the disk belongs to the install. Anything that
can be converged belongs to mgmt.**

**Why `/boot` is not encrypted.** Ubuntu's GRUB cannot open a LUKS2 header
that uses argon2id, the cryptsetup default. Ubuntu's own encrypted install
makes the same split. PCR 7 does not measure the kernel, so this does not
change the accepted threat model.

**Root size.** The root partition uses `size: -1`, which Subiquity resolves to
the rest of the disk. Only the last partition can do this. Bare curtin has no
such form, so testing the seed against curtin gives a false failure.

**Install speed.** The seed names one apt mirror, turns off the geoip mirror
lookup, and turns off kdump. `updates: security` is the smallest update
setting that Subiquity has.

**Subvolumes.** curtin cannot make subvolumes. The last late-command
snapshots the installed tree into `@`, makes `@` the default subvolume, and
makes `@swap` for the swapfile. A snapshot rewrites metadata only, so it is
fast and keeps ownership and extended attributes. mgmt makes the other
subvolumes on the first apply.

| Subvolume | Mount   | Why                                       |
| --------- | ------- | ----------------------------------------- |
| `@`       | `/`     | a snapshot of root can be rolled back     |
| `@swap`   | `/swap` | a swapfile blocks snapshots of its volume |

**Swap.** The swapfile is made by the install, because `@swap` must exist
before the tree moves into `@`. zram is made by mgmt, because it is a package
and a config file. zram has the higher priority, so cold pages compress into
RAM first, which leaves more memory for the page cache.
