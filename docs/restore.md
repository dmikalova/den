# Restore an old drive

This procedure gets files off an old drive and onto den. It images the drive
one time, and then works only on copy-on-write copies of the image. The old
drive is never written to.

The plan for the old 3-disk array is in the den runbooks in Notion, section 7.
This document uses the same steps for one drive at a time, and records what
happened on each drive.

## Layout

| Path | Holds | kopia | snapraid |
| --- | --- | --- | --- |
| `~/data/recover/<drive>/` | the raw image, the ddrescue map, SMART output | no | no |
| `~/data/restore/<drive>/` | the files that came out of the image | yes | yes |

`~/data` is a link to `/mnt/storage`. `<drive>` is the device name, the model
and the serial, for example `sde-toshiba-85UT1EVAS`.

kopia and snapraid both exclude `/recover/`. The old drive is the only other
copy of an image. Keep the old drive until its files are in kopia.

> **Caution:** Do not image a drive before the `/recover/` exclude is on den.
> Check it with `grep recover /etc/snapraid.conf`. Without it, the next
> snapraid sync puts the image into parity.

Put the image on one branch directly, for example
`/mnt/disk1/data/recover/<drive>`. It is then visible through the pool at the
same path. A reflink copy of the image works only on a btrfs branch. Through
mergerfs, `cp --reflink=always` fails, and a plain `cp` reflinks only if the
pool puts the copy on the same branch.

## Rules

- The original drive is read-only. Open it only with `ddrescue`, `smartctl`
  or read-only `dd`/`pread`.
- The image is `chmod 444` after imaging. Never attach it read-write.
- Work on a reflink copy of the image. To reset, delete the copy and make a
  new one. This takes a second and uses no space.
- Every command that can write carries `-n`, `--readonly` or `ro`.
- Run tools that den does not install in a rootful container. Do not
  `apt install` on the host. The root layer belongs to mgmt.

## Steps

### 1. Identify the drive

```bash
lsblk -o NAME,SIZE,FSTYPE,LABEL,MODEL,SERIAL,TRAN
ls -l /dev/disk/by-id/ | grep -w sdX
sudo smartctl -d sat -x /dev/sdX
sudo wipefs -n /dev/sdX
sudo fdisk -l /dev/sdX
```

Read the SMART output before anything else. If `Reallocated_Sector_Ct`,
`Current_Pending_Sector` or `Offline_Uncorrectable` is not zero, the drive is
failing. Image it first, and do not run any other read against it.

If `wipefs` and `fdisk` find nothing, sample the raw bytes. Zeros mean the
area was never written. Random bytes with no plaintext header mean
encryption. See [Encrypted drives](#encrypted-drives).

### 2. Image the drive

```bash
D=/mnt/disk1/data/recover/<drive>
sudo mkdir -p "$D" && sudo chown "$USER:" "$D"
sudo smartctl -d sat -x /dev/sdX > "$D/smart-before.txt"

sudo systemd-run --unit=recover-sdX --description="ddrescue sdX image" \
  podman run --rm --name recover-sdX --network host \
  --device /dev/sdX:/dev/src:r -v "$D:/out:z" \
  docker.io/library/debian:trixie-slim sh -c "
    apt-get update -qq && apt-get install -y -qq gddrescue >/dev/null &&
    ddrescue -d -n --sparse /dev/src /out/disk.img /out/disk.map &&
    ddrescue -d -r3 --sparse /dev/src /out/disk.img /out/disk.map"
```

- The first pass (`-n`) copies everything that reads easily. The second pass
  (`-r3`) retries only the bad areas.
- `-d` uses direct I/O. A USB bridge that drops out cannot corrupt the map.
- `--sparse` does not write zero blocks. A 2 TB drive with 50 GB of data uses
  about 50 GB.
- The unit continues if the SSH session drops. Follow it with
  `journalctl -fu recover-sdX`. If it stops, start it again. ddrescue reads
  the map and continues where it stopped.
- Use `/dev/sdX`, not the `/dev/disk/by-id` path. A by-id name can contain a
  `:`, which breaks the podman `--device` argument.
- Use `--network host`. The default rootful network cannot reach the apt
  mirror.

> **Note:** If the unit fails, the global `OnFailure=` sends a Discord notify.
> This is expected for a failed `recover-*` unit.

### 3. Seal the image

Do this when ddrescue reports `pct rescued: 100%`, or when the second pass
cannot recover more.

```bash
sudo chown "$USER:" "$D"/disk.*
chmod 444 "$D/disk.img" "$D/disk.map"
sha256sum "$D/disk.img" > "$D/manifest.txt"
sudo smartctl -d sat -x /dev/sdX > "$D/smart-after.txt"
```

Disconnect the drive. Label it with the `<drive>` name. Keep it until the
files are in kopia.

### 4. Make the copy-on-write sandbox

```bash
cp --reflink=always "$D/disk.img" "$D/work.img"
chmod 644 "$D/work.img"
LOOP=$(sudo losetup --find --show --read-only --partscan "$D/work.img")
```

Attach read-only by default. Use a read-write loop only if a tool must write,
for example `fsck` without `-n` as a last resort. A write changes `work.img`
only. `disk.img` keeps its own blocks.

To reset:

```bash
sudo losetup -d "$LOOP"
rm "$D/work.img"
cp --reflink=always "$D/disk.img" "$D/work.img"
```

Check that the copy shares its blocks. `Exclusive` must be near zero:

```bash
sudo btrfs filesystem du "$D"
```

### 5. Identify the contents

```bash
sudo wipefs -n "$LOOP"
sudo blkid -p "$LOOP" "${LOOP}"p*
sudo file -s "$LOOP" "${LOOP}"p*
```

| Result | Next step |
| --- | --- |
| A filesystem (`ext4`, `ntfs`, `exfat`, `hfsplus`, `apfs`) | step 6 |
| `linux_raid_member` | assemble the array read-only; see the Notion runbook, section 7 |
| `crypto_LUKS`, `BitLocker` | unlock read-only; see [Encrypted drives](#encrypted-drives) |
| Nothing, and the bytes are random | see [Encrypted drives](#encrypted-drives) |
| Nothing, and the bytes are not random | carve with `photorec` as a last resort; carving loses every file name |

### 6. Mount read-only

```bash
sudo mkdir -p /mnt/recovered
sudo mount -o ro,noload "$DEV" /mnt/recovered   # ext4; noload stops a journal replay
sudo mount -o ro "$DEV" /mnt/recovered          # ntfs3, exfat, hfsplus
```

If the mount fails, do a dry-run check before anything else:
`sudo fsck.ext4 -n "$DEV"` or `sudo ntfsfix -n "$DEV"`.

### 7. Make an inventory

```bash
sudo find /mnt/recovered -type f -printf '%s\t%TY-%Tm-%Td\t%p\n' \
  > ~/data/recover/<drive>/inventory.tsv
sudo find /mnt/recovered -type f -exec file -b --mime-type {} + |
  sort | uniq -c | sort -rn > ~/data/recover/<drive>/mime-types.txt
```

Read the inventory before you copy anything. Look for music, photos, and
documents such as taxes.

### 8. Copy the files out

```bash
mkdir -p ~/data/restore/<drive>
sudo rsync -aHAX --info=progress2 \
  --exclude='.cache/' --exclude='node_modules/' --exclude='.git/objects/' \
  --exclude='$RECYCLE.BIN/' --exclude='System Volume Information/' \
  /mnt/recovered/ ~/data/restore/<drive>/
sudo chown -R "$USER:" ~/data/restore/<drive>
```

Keep the directory structure. Sort the files into the library later.

### 9. Verify

```bash
sudo rsync -aHAX --dry-run --checksum --itemize-changes \
  /mnt/recovered/ ~/data/restore/<drive>/ | head
```

No output means every file is the same as in the image.

### 10. Clean up

Do these in this sequence:

1. Unmount and detach: `sudo umount /mnt/recovered && sudo losetup -d "$LOOP"`.
2. Let the nightly kopia backup take `~/data/restore/<drive>`, or start it:
   `sudo systemctl start kopia-backup.service`.
3. Restore one file from the new snapshot. See
   [backup.md](backup.md#restore-a-file).
4. Delete `work.img`. Keep `disk.img` until you no longer need the old drive.
5. When `~/data/recover` is empty, remove both `/recover/` rules: the ignore
   rule in `den-kopia` and the exclude in `snapraid.conf`. Without this, files
   that you put in a `recover` folder later get no backup and no parity.

## Encrypted drives

Encryption is visible as random bytes from sector 0 with no plaintext header.
Never-written areas of the drive stay as zeros.

| Type | How to see it | How to open it |
| --- | --- | --- |
| LUKS | `wipefs` finds `crypto_LUKS` | `cryptsetup open --readonly` |
| BitLocker | `-FVE-FS-` at byte 3 of the partition | `cryptsetup open --type bitlk --readonly` |
| VeraCrypt or TrueCrypt | random from byte 0, and the last 128 KiB random | `cryptsetup open --type tcrypt --veracrypt --readonly` |
| plain dm-crypt | random from byte 0, no header anywhere | `cryptsetup open --type plain --readonly` with the original cipher and key |
| hardware enclosure | random from byte 0; the drive was in a USB enclosure with encryption | put the drive back in the original enclosure model, then image it again through that enclosure |

Hardware enclosures that encrypt every sector include the WD My Book, the
Toshiba Canvio Desk and some Buffalo DriveStation models. The bridge board
holds the key. A different USB adapter, or a new enclosure of the same model,
reads only the ciphertext. Some of these enclosures encrypt even when no
password was set.

When the drive is back on its own board, the board decrypts transparently.
The drive then shows a normal partition table, and you can start again at
step 1. Image the drive through its own board, not through a generic adapter.

## Drive log

### sde-toshiba-85UT1EVAS

| | |
| --- | --- |
| Drive | Toshiba DT01ABA200, 2 TB, 5700 rpm, serial `85UT1EVAS` |
| Adapter | ASMedia USB 3.0 bridge (`ASMT USB 3.0 Destop H`), no encryption in the bridge |
| SMART | passed; 148 power-on hours, 2276 power cycles, 0 reallocated, 0 pending |
| Image | `/mnt/disk1/data/recover/sde-toshiba-85UT1EVAS/disk.img` |
| Restore | `~/data/restore/sde-toshiba-85UT1EVAS/` |

Findings before imaging:

- No partition table, no filesystem signature, no plaintext header.
- 0 to 49.7 GB: random bytes. 49.7 GB to the end: zeros. Some random blocks
  in the last 16 MiB, and the last 8.5 KiB are zeros.
- The random blocks are ciphertext. Entropy is 7.95 to 8.0 bits per byte, and
  no sample has a file signature.
- This is not LUKS, BitLocker, APFS or CoreStorage, because each of those has
  a plaintext header.
- It is probably not VeraCrypt, because the last 128 KiB are not all random.
- The most likely cause is a hardware encrypting enclosure. The high power
  cycle count and the low power-on hours fit an external backup drive.

The drive came from a Toshiba Canvio Desk (`HDWC120XK3J1`). Its USB bridge
board encrypts every sector, and only that board can decrypt them. The board
is kept, and only its cable is missing.

Status:

- 2026-10-24: imaged the first 222 GB through the ASMedia adapter, 0 read
  errors. This holds all of the ciphertext. Imaging stopped so that the
  snapraid exclude can go to den first.
- Next: get a USB 3.0 Micro-B cable for the Canvio Desk board, and the 12 V
  power supply if it is also missing. Put the drive back on its own board.
  Image it again through that board, into a new directory. That image is
  plaintext. Then delete the ciphertext image.
