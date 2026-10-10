# Autoinstall

den installs unattended. `den usb build` makes a bootable image from the
Ubuntu Server ISO. `den usb write` copies that image to a USB stick. The
installer then runs with no questions and reboots into a host you can SSH to.

## Build the image

The build needs `xorriso`, `gpg` and `sops`:

```sh
brew install xorriso gnupg sops
```

The install secrets live in `secrets/install.sops.json`, encrypted to the age
key in Bitwarden. Create it once:

```sh
sops secrets/install.sops.json
```

It holds two keys:

| Key               | What it is                                        |
| ----------------- | ------------------------------------------------- |
| `PASSWORD`        | console and sudo password; den hashes it at build |
| `LUKS_PASSPHRASE` | unlocks the root volume, and the TPM recovery key |

Keep these different. `PASSWORD` is typed often, so it must be easy to type.
`LUKS_PASSPHRASE` faces offline brute force if the disk is ever stolen, so it
must be long and random. After the TPM is enrolled you never type it.

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
- Installs `curl`, `git` and `gnupg`, then reboots.

Nothing else is installed at this stage. Everything above the host layer
comes from chezmoi on first boot.

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

> **Unverified:** the btrfs and LUKS storage stanza has not been run end to
> end. Confirm the result on first boot.

## First boot

The host comes up on DHCP and asks for the LUKS passphrase on the console.
Enter it once. The TPM takes over from the second boot.

1. Confirm the TPM enrolled itself. `den-tpm-enroll.service` runs once on the
   first boot, binds a key slot to PCR 7, adds `tpm2-device=auto` to
   `/etc/crypttab`, rebuilds the initramfs, and shreds the seed that curtin
   left in `/var/log/installer/autoinstall-user-data`.

   ```sh
   systemctl status den-tpm-enroll.service
   journalctl -u den-tpm-enroll.service
   ```

   The unit refuses to touch `/etc/crypttab` if there is no TPM, or if the
   initramfs has no `systemd-cryptsetup` to use one, because an option the
   boot scripts do not understand can leave the root volume unopenable. Both
   cases are logged and leave the passphrase as the only unlock.

   Reboot and confirm it comes up with no prompt. **Keep the passphrase in
   Bitwarden.** A firmware update changes PCR 7 and you will need it.

2. Install Tailscale and join the tailnet:

   ```sh
   curl -fsSL https://tailscale.com/install.sh | sh
   sudo tailscale up --ssh
   ```

3. Put the age key in place. Copy it from Bitwarden:

   ```sh
   sudo install -d -m 700 /var/lib/sops/age
   sudo tee /var/lib/sops/age/keys.txt > /dev/null   # paste, then Ctrl-D
   sudo chmod 600 /var/lib/sops/age/keys.txt
   ```

4. Apply the host config:

   ```sh
   sh -c "$(curl -fsLS get.chezmoi.io)" -- init --apply dmikalova/den
   ```

5. Confirm the host can reach you:

   ```sh
   den notify --level ok --title bootstrap
   ```

   The message lands in Discord. If it does not, nothing later will tell you
   when something breaks, so fix it before you go on.
