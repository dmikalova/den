# Autoinstall

den installs unattended. `den usb build` makes a bootable image from the
Ubuntu Server ISO. `den usb write` copies that image to a USB stick. The
installer then runs with no questions and reboots into a host you can SSH to.

## Build the image

The build needs `xorriso`, `gpg` and `gcloud`:

```sh
brew install xorriso gnupg
brew install --cask gcloud-cli
```

The install secrets live in the `den-install` secret in GCP Secret Manager,
written by the infrastructure repo. That secret carries no IAM binding, so
only a project admin reads it, which is what keeps den itself out of it.

It holds two keys the build uses:

| Key               | What it is                                        |
| ----------------- | ------------------------------------------------- |
| `PASSWORD`        | console and sudo password; den hashes it at build |
| `LUKS_PASSPHRASE` | unlocks the root volume, and the TPM recovery key |

Keep these different. `PASSWORD` is typed often, so it must be easy to type.
`LUKS_PASSPHRASE` faces offline brute force if the disk is ever stolen, so it
must be long and random. After the TPM is enrolled you never type it.

Pass `--password-file` or `--luks-passphrase-file` to build without GCP.

Build:

```sh
den usb build
```

The build downloads the release ISO into `~/Downloads` and writes
`~/Downloads/den-autoinstall.iso`. Both are several GB. They live there rather
than in a cache directory so you see them and delete them when you are done.
It checks the source ISO's SHA256 against the signed `SHA256SUMS`, and stops
if the signature does not verify. If `gpg` does not have the Ubuntu CD signing
key, add it:

```sh
gpg --keyserver keyserver.ubuntu.com \
  --recv-keys 843938DF228D22F7B3742BC0D94AA3F0EFE21092
```

Pass `--iso <path>` to use an ISO you already have. The checksum is still
verified.

## Write the USB stick

Find the disk:

```sh
diskutil list external
```

**Caution: the write erases the whole disk.** `den usb write` refuses an
internal disk, a disk that is not removable, and a partition. It still cannot
tell one USB stick from another, so read the printed name and size before you
pass `--yes`.

```sh
sudo den usb write --image ~/Downloads/den-autoinstall.iso --device /dev/disk4 --yes
```

The write reads the stick back and compares the SHA256 against the image.

## What the installer does

The image carries the seed in `/nocloud` and a GRUB menu entry with
`autoinstall ds=nocloud;s=/cdrom/nocloud/`. The `autoinstall` parameter is
what makes Subiquity skip its confirmation prompt. Without it the installer
waits for a keypress before it touches a disk.

The seed:

- Pins the install to `/dev/nvme0n1` by path. It never matches a disk by
  size or by model, so the data drives are not candidates.
- Makes a 1 GB EFI partition and a 2 GB `/boot`, then gives the rest to a
  LUKS2 volume with btrfs inside it.
- Creates one account, with password login for the console and key-only
  login for SSH.
- Sets the timezone to `America/Los_Angeles` and the time format to ISO 8601.
- Installs `curl`, `git` and `gnupg`, then reboots.

Nothing else is installed at this stage. Everything above the host layer
comes from mgmt on first boot.

**The LUKS passphrase is in clear on the install media.** curtin accepts it no
other way, and it copies the seed to `/var/log/installer` on the installed
system. Wipe the USB stick after the install.

### Why /boot is not encrypted

GRUB reads the kernel and initramfs before anything is unlocked. Ubuntu's GRUB
cannot open a LUKS2 header that uses the argon2id KDF, which is the cryptsetup
default. So `/boot` is its own ext4 partition outside the encryption, which is
what Ubuntu's own guided encrypted install does.

The cost is that the kernel and initramfs are readable and writable by anyone
with physical access. TPM PCR 7 does not measure them, so this does not change
the threat model already accepted.

### Root partition size

The root partition uses `size: -1`. Subiquity reads a negative size as "the
rest of the disk" and resolves it against the real device before it calls
curtin. The rule is that only the last partition on a disk may do this;
anywhere else Subiquity raises `NotFinalPartitionError`.

curtin on its own has no such form, so testing this config against bare curtin
gives a false failure.

### Locale and time

The seed sets `timezone: America/Los_Angeles`. Left unset, Subiquity derives
the zone from a geoip lookup, which makes the result depend on the network the
install ran on.

Autoinstall has one `locale` key and no key for the time format, so the 24
hour clock and ISO 8601 dates come from `LC_TIME=en_DK.UTF-8`, written by a
late-command. Only `LC_TIME` is taken from `en_DK`, so messages stay in
English. The setting lands in `/etc/default/locale`, which PAM reads for every
login shell including root. This is machine state, not user state, so mgmt
does not own it.

Journal timestamps are not affected either way. `journalctl` prints its own
format, which ignores the locale.

### Install speed

`updates: security` is the Subiquity default and the smallest setting it
offers; there is no way to install zero updates. Everything beyond that is
deferred to mgmt on first boot.

Three keys cut install-time work:

- `apt.mirror-selection.primary` names one mirror. The default is a candidate
  list that the installer probes one at a time.
- `apt.geoip: false` stops the country-mirror lookup from choosing the
  mirror. It does not stop the request itself, which Subiquity makes for other
  reasons.
- `kernel-crash-dumps.enabled: false`. From 24.10 the default enables kdump on
  any machine that qualifies, which installs `kdump-tools` and reserves RAM
  for a crash kernel on every boot.

### Subvolume layout

curtin has no subvolume action. The storage schema stops at `format`, and
`fstype: btrfs` only works because curtin falls through to `mkfs.btrfs` for
filesystems it does not know. So curtin lays the tree down at the top level of
the filesystem and the last late-command rebuilds it:

| Subvolume | Mount   | Why                                              |
| --------- | ------- | ------------------------------------------------ |
| `@`       | `/`     | lets a snapshot be rolled back at boot time      |
| `@swap`   | `/swap` | a swapfile blocks snapshots of its own subvolume |

The move into `@` is a `btrfs subvolume snapshot`, not a copy. It rewrites
metadata only, so it is quick and cannot lose ownership or extended
attributes. The initramfs then finds `@` because the late-command makes it the
default subvolume; the kernel command line carries no `rootflags`.

The late-command leaves the original tree at the top level. It shares every
extent with `@`, so it costs almost nothing at first, and it is a working
fallback if the new layout does not boot. Delete it by hand once the host
comes up (see First boot), because it does start to cost real space as `@`
diverges from it.

### Swap

den runs both zram and a disk swapfile, and they are configured in different
places on purpose.

| Layer    | Where            | Why there                         |
| -------- | ---------------- | --------------------------------- |
| swapfile | this autoinstall | it needs its own subvolume        |
| zram     | `mgmt/main.mcl`  | it is a package and a config file |

The split follows one rule: **anything that shapes the disk belongs to the
install, anything reconcilable belongs to mgmt.** `@swap` has to exist before
the tree moves into `@`, because btrfs refuses to snapshot a subvolume that
holds a swapfile. There is no way to add it later without unwinding the
layout. zram has no such constraint, so putting it in the image would only
make it harder to change.

They cooperate rather than compete. zram takes the higher priority, so the
kernel compresses cold anonymous pages into RAM first and only reaches the
SSD when zram is full. The point of either is less about running out of
memory, which 64 GB makes unlikely, than about letting the kernel evict pages
some daemon allocated at startup and will never touch again, and hand that
memory to page cache. On a file server, page cache is what makes it fast.

## First boot

The host comes up on DHCP and asks for the LUKS passphrase on the console.
Enter it once. The TPM takes over from the second boot.

1. Reach the host over SSH. The installer runs `avahi-daemon`, so the host
   answers to its hostname on the local network and you do not have to hunt
   for the address:

   ```sh
   ping -c1 den.local
   ssh -i ~/.ssh/id_dmikalova -o IdentitiesOnly=yes dmikalova@den.local
   ```

   `IdentitiesOnly=yes` stops the agent from offering every key it holds.
   The server takes one key and refuses passwords, so without this flag a
   refusal does not tell you which key failed.

   If mDNS does not answer, find the address from the console with
   `ip -4 -br addr show scope global`, or sweep the subnet from the Mac with
   `nmap -sn 192.168.0.0/24`. The router's DHCP lease table also shows the
   hostname the host claimed.

   SSH asks you to accept a host key the first time. Read the real
   fingerprint off the console first and compare:

   ```sh
   ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub
   ```

   Confirm the install touched one disk only. The four data drives must still
   be empty:

   ```sh
   lsblk -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINT
   systemctl --failed
   ```

   Confirm the subvolume layout took. `findmnt` has to report `subvol=/@`,
   and swap has to come from `/swap/swapfile`:

   ```sh
   sudo btrfs subvolume list /
   findmnt -no SOURCE,FSTYPE,OPTIONS /
   swapon --show
   ```

   Confirm the clock. `timedatectl` has to report `America/Los_Angeles` with
   NTP active, and `date` has to print a 24 hour time:

   ```sh
   timedatectl
   locale | grep LC_TIME
   date
   ```

   The installer leaves the pre-subvolume tree at the top level as a
   fallback. Once the host boots from `@` and the TPM has enrolled, delete
   it:

   ```sh
   sudo mkdir -p /mnt/top
   sudo mount -o subvolid=5 /dev/mapper/den-root /mnt/top
   ls /mnt/top                     # expect @, @swap, and the stale tree
   sudo find /mnt/top -mindepth 1 -maxdepth 1 \
     ! -name @ ! -name @swap -exec rm -rf {} +
   sudo umount /mnt/top && sudo rmdir /mnt/top
   ```

2. Confirm the TPM enrolled itself. `den-tpm-enroll.service` runs once on the
   first boot, binds a key slot to PCR 7, adds `tpm2-device=auto` to
   `/etc/crypttab`, rebuilds the initramfs, and shreds the passphrase that the
   installer left in `/etc/den/luks.key`.

   ```sh
   systemctl status den-tpm-enroll.service
   journalctl -u den-tpm-enroll.service
   ```

   The unit refuses to touch `/etc/crypttab` if there is no TPM, or if the
   initramfs has no `systemd-cryptsetup` to use one, because an option the
   boot scripts do not understand can leave the root volume unopenable. Both
   cases are logged and leave the passphrase as the only unlock.

   An unexpected failure leaves the unit enabled and the key file in place, so
   the next boot tries again. Only a decided outcome shreds the key and
   disables the unit.

   Reboot and confirm it comes up with no prompt. **Keep the passphrase in
   Bitwarden.** A firmware update changes PCR 7 and you will need it.

3. Apply the host config:

   ```sh
   den bootstrap
   den apply
   ```

   `den apply` installs Tailscale through mgmt and joins the tailnet itself,
   with the auth key from `tailscale-den` and `tag:den`. Do not run
   `tailscale up` by hand first. The binary is not on the host until this step
   installs it.

   `den apply` also reads den's service account key out of `den-install` and
   seals it to the TPM, so den can fetch its own secrets from then on. That
   key is the only credential ever delivered to den.

4. Reboot for the NVIDIA driver. mgmt installs it but does not reboot, and the
   GPU container stack stays broken until the module loads.

5. Confirm the host can reach you:

   ```sh
   ssh den sudo den notify --title bootstrap "den is up"
   ```

   The message lands in Discord. If it does not, nothing later will tell you
   when something breaks, so fix it before you go on. `den notify` needs root
   because `/etc/den` is `0700`.

6. Run the smoke checks:

   ```sh
   den check
   ```
