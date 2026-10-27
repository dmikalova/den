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
| `~/data/restore/<drive>/` | the files that came out of the image, before validation | no | no |
| `~/data/mess/<drive>/` | the validated files, before you sort them into the library | yes | yes |

`~/data` is a link to `/mnt/storage`. `<drive>` is the device name, the model
and the serial, for example `sde-toshiba-85UT1EVAS`.

kopia and snapraid both exclude `/recover/` and `/restore/`. Nothing in them
gets a backup or parity, so nothing in them costs B2 storage. The old drive
and the image are the only other copies. Keep the old drive and the image
until the files are in `~/data/mess` and in a kopia snapshot.

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
2. Delete `work.img`.
3. Look through `~/data/restore/<drive>`. Delete what you do not want.
4. Move the rest to `~/data/mess/<drive>`. Only now do the files get a
   backup and parity.
5. Let the nightly kopia backup take `~/data/mess/<drive>`, or start it:
   `sudo systemctl start kopia-backup.service`.
6. Restore one file from the new snapshot. See
   [backup.md](backup.md#restore-a-file).
7. Delete the image. Keep the old drive until you no longer need it.
8. When `~/data/recover` and `~/data/restore` are both empty, remove the
   `/recover/` and `/restore/` rules: the ignore rules in `den-kopia` and the
   excludes in `snapraid.conf`. Without this, files that you put in a folder
   with one of these names later get no backup and no parity.

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
| ecryptfs (Ubuntu encrypted home) | the filesystem mounts, but `home/<user>` has only `.Private` and `.ecryptfs` links, and `home/.ecryptfs/<user>/` exists | see [ecryptfs home folders](#ecryptfs-home-folders) |

Hardware enclosures that encrypt every sector include the WD My Book and
some Buffalo DriveStation models. The Toshiba Canvio Desk does not encrypt:
its board passes the sectors through unchanged. The bridge board
holds the key. A different USB adapter, or a new enclosure of the same model,
reads only the ciphertext. Some of these enclosures encrypt even when no
password was set.

When the drive is back on its own board, the board decrypts transparently.
The drive then shows a normal partition table, and you can start again at
step 1. Image the drive through its own board, not through a generic adapter.

### LVM in an image

If the image has an LVM volume group, den sees the same volume group on the
drive and on the image. Do these steps before you attach the image:

1. Unmount the drive and deactivate its volume group:
   `sudo vgchange -an <vg>`.
2. Attach the image read-only: `LOOP=$(sudo losetup -r -P --show -f work.img)`.
3. Activate the volume group from the image only:
   `sudo vgchange -ay --devices ${LOOP}p3 <vg>`.
4. Make sure that the logical volume uses the loop device:
   `sudo dmsetup deps -o devname <vg>-root`.

### ecryptfs home folders

Ubuntu 18.04 and earlier can encrypt the home folder with ecryptfs. The key
is wrapped with the old login password. The ecryptfs module is built into the
den kernel. The user-space tools are only in a container, so den stays clean.

1. Mount the root filesystem of the image read-only at `/mnt/ssd-image`.
2. Build a rootful image `localhost/ecryptfs-tools` from `debian:trixie-slim`
   with `ecryptfs-utils`, `keyutils` and `rsync`.
3. Start the container. The `rshared` volume makes the decrypted mount visible
   on den:

   ```bash
   sudo mkdir -p /mnt/ssd-home
   sudo podman run -d --name ssd-decrypt --privileged --network none \
     -v /mnt/ssd-image:/src:ro -v /mnt/ssd-home:/view:rshared \
     localhost/ecryptfs-tools sleep infinity
   ```

4. Load the key. The owner types the old login password:

   ```bash
   sudo podman exec -it ssd-decrypt ecryptfs-insert-wrapped-passphrase-into-keyring \
     /src/home/.ecryptfs/<user>/.ecryptfs/wrapped-passphrase
   ```

5. Mount the home folder read-only. The two signatures are the two lines of
   `.ecryptfs/Private.sig`. They are key IDs, not secrets.

   ```bash
   sudo podman exec ssd-decrypt keyctl session - sh -c '
     keyctl link @u @s
     mount -i -t ecryptfs -o ro,ecryptfs_sig=<sig1>,ecryptfs_fnek_sig=<sig2>,ecryptfs_cipher=aes,ecryptfs_key_bytes=16 \
       /src/home/.ecryptfs/<user>/.Private /view'
   ```

CAUTION: Do not skip `keyctl session` and `keyctl link @u @s`. Step 4 puts
the keys in the root user keyring. `podman exec` inherits the session keyring
of the caller, and the kernel does not search the root user keyring from that
session. Without the link, the mount fails with `fsconfig() failed: No such
file or directory`.

To lock the home folder again, do these steps:

1. Unmount it: `sudo podman exec ssd-decrypt umount /view`.
2. Remove the container: `sudo podman rm -f ssd-decrypt`.
3. Clear the keys. `keyctl` is not on den, so use a container:
   `sudo podman run --rm --network none localhost/ecryptfs-tools keyctl clear @u`.
4. Remove the image: `sudo podman rmi localhost/ecryptfs-tools`.

## The old /mnt/data pool

The old server `dmikalova-server` (Ubuntu 18.04, 2016-03 to 2019-09) had a
btrfs pool at `/mnt/data`. The pool held `Plex`, `Torrents`, `Dropbox` and
`mess`. The links in the old home folder point there.

| | |
| --- | --- |
| Label | `data` |
| UUID | `1571af3f-0838-4463-bc1e-0939ab88eb75` |
| Size | 4 devices, 11.0 TB in total, 5.03 TiB used |
| Mount options | `noatime,autodefrag,compress-force=zlib,space_cache` |
| Last mount | 2019-09-07, transid 128763 on all 4 devices, so the pool was consistent |
| SYSTEM chunks | RAID1 on devid 1 and devid 2 |
| Data and metadata profile | unknown; read it with `btrfs filesystem df` after the pool mounts |

The members come from the old server's records. The SSD image holds them:
`/var/lib/smartmontools/` names each drive by model and serial, and the
2019-09-07 boot in `/var/log/kern.log` maps each devid to a `/dev/sdX` and a
model.

| devid | Old name | Drive | Serial | dev_uuid | Status |
| --- | --- | --- | --- | --- | --- |
| 1 | `sdb` | Seagate ST3000DM001, 3 TB | `Z500MRLD` | `0619e0ff-4ee7-4cbf-bfa3-bab6efc4166e` | imaged and sealed; see the drive log |
| 2 | `sdd` | WD Red WD40EFRX, 4 TB | `WD-WCC4E7HP6U7J` | `afd881a8-b886-4e15-ac86-2f7aea78d500` | imaged and sealed; see the drive log |
| 3 | `sdc` | Seagate ST3000DM001, 3 TB | `Z500NWN8` | devid 3 in `btrfs-super.txt` | imaged and sealed; see the drive log |
| 4 | `sde` | WD Green WD10EARX, 1 TB | `WD-WMC0S1089686` | in `btrfs-super.txt` | imaged; see the drive log |

Notes:

- The two Seagates are the same model. Their superblocks give the devid.
- devid 2 had 1 write error and 12 read errors on its btrfs counters at the
  last boot. Image it with care, and keep the ddrescue map.
- The ST3000DM001 has a high failure rate. Image each Seagate before you do
  anything else with it.
- At least one of devid 1 and devid 2 is necessary to read the chunk tree.
  How many of the 4 devices are necessary for the data depends on the data
  profile.
- These drives were in the server only in 2016-03, and are not in the btrfs
  pool: Seagate ST3000DM001 `Z50055EX`, `Z5006GSW` and `Z5006P1Z` (until
  2016-03-28), and WD Green WD15EADS 1.5 TB `WD-WMAVU0785064` (until
  2016-03-19). The three Seagates are a separate, older ZFS raidz1 pool. See
  "The old ZFS pool" below.
- The Toshiba from the Canvio Desk was never in this server.

To identify a drive, attach it and run:

```bash
sudo smartctl -i /dev/sdX | grep -E 'Model|Serial'
sudo btrfs inspect-internal dump-super /dev/sdX | grep -E '^(label|fsid)|dev_item.(devid|uuid)'
```

Image each member to its own directory under `recover/`. Put the images on
different branches, because the 4 images are 11 TB in total. Use `ddrescue
-S`, so the never-written areas take no space.

When all the members that you can find are imaged, do these steps:

1. Make a reflink `work.img` of each image.
2. Attach each one read-only: `sudo losetup -r -f --show work.img`.
3. Scan for the devices: `sudo btrfs device scan`.
4. Mount the pool:
   `sudo mount -o ro,rescue=nologreplay,degraded /dev/loopN /mnt/olddata`.
   Use `degraded` only if a member is missing.
5. Read the profiles: `sudo btrfs filesystem df /mnt/olddata`.
6. Continue at step 7 of the steps above.

### Mount result and inventory

All 4 members imaged and sealed. The pool mounted read-only on the first
try with `ro,rescue=nologreplay`. All 4 devices report the same transid
128763 and 0 corruption errors. loop8 (devid 2, WD Red) still carries its
old error counters from the 2019 server (1 write, 12 read), but no new
checksum failures appeared at mount.

Profiles are mixed. The pool holds both single and RAID5 block groups:

- Data: single 3.42 TiB (used 3.21), RAID5 1.87 TiB (used 1.81).
- Metadata: RAID1 5 GiB (used 3.76), RAID5 3.09 GiB (used 2.59).
- System: RAID1 8 MiB.

Total used about 5.64 TiB. The RAID5 groups span all 4 devices, so a
missing member would need `degraded` and lose the RAID5 data. All 4 are
present here.

Top level (5.1 TiB, 448,993 files):

| Top dir | Size | Dated | Main contents |
| --- | --- | --- | --- |
| `mess` | 3.2 TiB | Oct 2018 | music 911G, torrents 993G, videos 443G, Recovery 521G, Mess 167G, originals 69G, games 48G, books 38G |
| `Torrents` | 1.8 TiB | Apr 2016 | Seeding 1.3T, Finished 443G |
| `Plex` | 175 GiB | Jun 2016 | movies 100G, tv 72G, music 4.2G |
| `Dropbox` | 4.1 GiB | Jul 2017 | Music 1.4G, Camera Uploads 1.2G, Apps, keys, Public |

The root dir is dated 2017-01-02. There are no subvolumes.

## The old ZFS pool

Three Seagate ST3000DM001 drives form a separate, older pool. ZFS, not
btrfs. They were in `dmikalova-server` only until 2016-03-28, so this pool
predates the btrfs pool. The user says these three drives are a separate
older set, and the partition or format may be damaged, so expect extra
investigation.

| | |
| --- | --- |
| Type | ZFS, raidz1, 3 disks, `ashift=12` |
| Pool name | `data` |
| Host | `dmikalova-server` |
| Members | `Z50055EX`, `Z5006P1Z`, `Z5006GSW` (by `ata-ST3000DM001-1ER166_<serial>-part1`) |
| Layout | GPT: part 1 = `zfs` (2.7 TiB), part 9 = reserved (8 MiB) |

The ZFS label is on partition 1, not the raw disk. The label `vdev_tree`
names the raidz vdev and its 3 child disks by `/dev/disk/by-id` path, which
gives the serials above.

`zdb -l` and `zdb -lu` on the saved label of `Z5006GSW` show:

- `state: 0` (active). The pool was not exported and not destroyed. Import
  needs `-f`.
- Pool GUID `5836723255957420361`, last txg 19,267,508.
- `nparity: 1`. None of the 3 children is marked faulted or missing.
- Features for read: only `hole_birth` and `embedded_data`. Any current
  OpenZFS can read it.
- The last uberblock is from 2016-03-28 15:09:43 UTC. This agrees with the
  server records: the pool was retired on that day.

To check the other 2 members, read their labels first. A matching pool GUID
and a txg near 19,267,508 mean the 3 disks are consistent.

Run `zdb` in a container. Debian `zfsutils-linux` is in `contrib`. `zdb` is
user space and does not need the kernel module.

To image and read this pool:

1. Image each of the 3 members to its own `recover/` directory, same
   ddrescue pattern as the btrfs pool.
2. raidz1 tolerates 1 missing disk, so 2 of 3 images can still import the
   pool. All 3 is best.
3. Reflink a `work.img` of each image. Attach read-only with partition scan:
   `sudo losetup -r -P -f --show work.img`. The ZFS partition is `loopNp1`.
4. Install `zfsutils-linux` in a container, or on the host.
5. Import read-only, no mount, by a scan directory of the loop partitions:
   `sudo zpool import -d /dev/disk/by-loop-part -o readonly=on -N data`.
   If the pool name or state is bad, use `zpool import -d <dir>` first to
   list what ZFS sees, then `-f` if it needs a force, still `readonly=on`.
6. `zfs list`, then mount the datasets read-only and inventory to
   `~/data/restore/`.

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
- The high power cycle count and the low power-on hours fit an external
  backup drive.

The drive came from a Toshiba Canvio Desk (`HDWC120XK3J1`). The first theory
was that the Canvio board encrypts every sector. That is not correct. See
the 2026-10-25 status.

Status:

- 2026-10-24: imaged the first 222 GB through the ASMedia adapter, 0 read
  errors. This holds all of the ciphertext. Imaging stopped so that the
  snapraid exclude can go to den first.
- Next: get a USB 3.0 Micro-B cable for the Canvio Desk board, and the 12 V
  power supply if it is also missing. Put the drive back on its own board.
  Image it again through that board, into a new directory. That image is
  plaintext. Then delete the ciphertext image.
- 2026-10-25: the user deleted the 222 GB ciphertext image before this
  check.
- 2026-10-25: attached the drive through its own Canvio Desk board (USB ID
  `0480:d011`). The board gives the same bytes as the ASMedia adapter: random
  from 0 to 49,725,767,680 bytes, then zeros, and random blocks in the last
  13 MiB. ATA security is not enabled and not locked. The Canvio Desk has no
  hardware encryption. Its "password protection" is the NTI backup software,
  which encrypts files, not the drive.
- 2026-10-25: the encryption is from software on the host. The user thinks a
  Linux machine set up the drive. That points to plain dm-crypt, or LUKS with
  a detached header. A VeraCrypt volume is also possible. The old server (the
  OCZ image) has only `cryptswap1` in `/etc/crypttab`, and no history or key
  file for this drive.
- 2026-10-25: the user tried passphrases with `/tmp/try-toshiba.sh` on den.
  The script opens the drive read-only in 8 plain dm-crypt modes and in
  VeraCrypt and TrueCrypt modes. It reports a match only if a filesystem or a
  partition table appears. No passphrase gave a match.
- 2026-10-25: ran `/tmp/toshiba-brute.sh`. It tested every 3-letter lowercase
  prefix plus one fixed suffix against 2 plain dm-crypt modes: 35,152 tries
  in 5,610 s. No match.
- 2026-10-25: parked this drive. The automated sweep is done. A sweep can
  only cover plain dm-crypt. It cannot cover LUKS with a detached header (the
  header file is gone with the old laptop) or VeraCrypt (the slow KDF makes a
  sweep take years). The drive most likely came from a laptop the user no
  longer has. The data may already exist on the pool drives, or it is lost.
  The user does not remember the contents. Do not image or sweep this drive
  again unless the real passphrase or a detached header file turns up.

### ssd-ocz-vertex3-TF917I2JJP0YN2ST

| | |
| --- | --- |
| Drive | OCZ Vertex 3, 120 GB SSD, serial `OCZ-TF917I2JJP0YN2ST` |
| Adapter | ASMedia USB 3.0 bridge (`ASMT USB 3.0 Destop H`) |
| SMART | passed; 27,453 power-on hours, 0 uncorrectable, 1 reallocation event, 9.4 TiB written |
| Image | `/mnt/disk1/data/recover/ssd-ocz-vertex3-TF917I2JJP0YN2ST/image.img`, SHA-256 in `image.img.sha256` |
| Restore | `~/data/restore/ssd-ocz-vertex3-TF917I2JJP0YN2ST/` |

Findings:

- The OS root drive of `dmikalova-server`. Ubuntu 18.04, installed 2016-03,
  last write 2019-09.
- GPT: EFI partition, ext2 `/boot`, and LVM volume group
  `dmikalova-server-vg` with `root` (ext4, 79 GB) and `swap_1`. No disk
  encryption. Swap was encrypted with `cryptswap1`.
- The home folder is ecryptfs, 23 GB. The old login password unwrapped the
  key. See [ecryptfs home folders](#ecryptfs-home-folders).
- `~/Plex`, `~/Dropbox`, `~/Torrents` and `~/mess` are links to the old
  `/mnt/data` pool. The media is not on this drive.
- `/var/lib/smartmontools/` and `/var/log/kern.log*` list the drives of the
  old pool. See [The old /mnt/data pool](#the-old-mntdata-pool).
- Data of interest: `Documents` (Enpass and Anki), `Downloads` (PDFs such as
  a loan agreement and an insurance card), `code`, `go/src`, `.ssh`,
  `.gnupg`, `.bash_history`, `.config`, `.mozilla` and `.factorio`.

Restored to `~/data/restore/ssd-ocz-vertex3-TF917I2JJP0YN2ST/`:

- `home/`: 195,642 files, 7.9 GB. Not copied: `.cache`, `.steam`, `.npm`,
  `.vscode`, `.dropbox`, `.dropbox-dist`, `snap`, Steam and the trash.
  Verified with an rsync `--checksum` dry run: no differences.
- `plex-databases/`: the Plex library database. 470 media files, and the
  watch history.
- `deluge/`: `/var/lib/deluge`, with 1,080 `.torrent` files.

Status:

- 2026-10-24: imaged 120 GB, 0 read errors, 12 minutes. Restored and
  verified. Locked the home folder, cleared the keys, deleted `work.img`.
- Next: look through the restore directory. Move what you keep to
  `~/data/mess/`. Then delete the image, or keep it for the old system
  files.

### sde-wd-WMC0S1089686

| | |
| --- | --- |
| Drive | WD Green WD10EARX, 1 TB, serial `WD-WMC0S1089686` |
| Adapter | ASMedia USB 3.0 bridge (`ASMT USB 3.0 Destop H`) |
| SMART | passed; 24,723 power-on hours, 98 power cycles, 0 reallocated, 0 pending |
| Image | `/mnt/disk2/data/recover/sde-wd-WMC0S1089686/image.img` (sparse) |
| Files | `smart-before.txt`, `btrfs-super.txt` (the full superblock with `dump-super -f`) |

Findings:

- No partition table. The whole drive is one btrfs device.
- devid 4 of the old `/mnt/data` pool. See
  [The old /mnt/data pool](#the-old-mntdata-pool). This drive holds 639 GiB
  of chunks.
- The pool does not mount from this drive alone. The SYSTEM chunks are on
  devid 1 and devid 2 only, so btrfs cannot read the chunk tree.
- The pool used `compress-force=zlib`, so file carving from one device gives
  little.

Status:

- 2026-10-24: imaged with the `recover-wd1tb` unit, 16:01 to 19:57. 100%
  rescued, 0 read errors, about 70 MB/s. SMART after: 0 reallocated, 0
  pending. Sealed: `chmod 444`, SHA-256 `28b4d3d1…5340` in
  `image.img.sha256`. The image uses 620 GiB on disk because it is sparse.
- The drive is off den. Next: devid 2, the WD Red 4 TB.

### sde-seagate-Z500MRLD

| | |
| --- | --- |
| Drive | Seagate ST3000DM001-1ER166, 3 TB, 7200 rpm, serial `Z500MRLD` |
| SMART | passed; 27,494 power-on hours, 110 power cycles, 0 reallocated, 0 pending, 0 offline uncorrectable, 0 reported uncorrect, 0 CRC |
| Image | `/mnt/disk2/data/recover/sde-seagate-Z500MRLD/image.img` (sparse) |
| Files | `smart-before.txt`, `btrfs-super.txt` |

Findings:

- No partition table. The whole drive is one btrfs device.
- devid 1 of the old `/mnt/data` pool, dev_uuid
  `0619e0ff-4ee7-4cbf-bfa3-bab6efc4166e`, generation 128763.
- This drive has a copy of the SYSTEM chunks (SYSTEM is RAID1 on devid 1 and
  devid 2).
- The ST3000DM001 is a failure-prone model, so image it early. This one is
  healthy: the large `Raw_Read_Error_Rate` and `Seek_Error_Rate` raw values
  are Seagate's encoded counters, not real errors. The normalized values pass.

Status:

- 2026-10-25 20:42: started the image with the `recover-seagate1` unit.
  Pass 1 (`-n`) skips the bad areas. Pass 2 (`-d -r3`) retries them. The
  first estimate is about 4.5 h.
- 2026-10-26 06:47: the image finished. ddrescue rescued all
  3,000,592,982,016 bytes. 0 bad bytes, 0 read errors. A clean read.
  Detached the drive. The `seal-seagate1` unit saved `smart-after.txt` and
  seals the image.
- 2026-10-26: sealed. SHA-256 `787efee680253e07…c3a13673` in
  `image.img.sha256`; `chmod 444` on the image, map, and hash file.

### sde-seagate-Z500NWN8

| | |
| --- | --- |
| Drive | Seagate ST3000DM001-1ER166, 3 TB, 7200 rpm, serial `Z500NWN8` |
| SMART | passed; 27,473 power-on hours, 0 reallocated, 0 pending, 0 offline uncorrectable, 0 reported uncorrect, 0 CRC |
| Image | `/mnt/disk1/data/recover/sde-seagate-Z500NWN8/image.img` (sparse) |
| Files | `smart-before.txt`, `btrfs-super.txt` |

Findings:

- No partition table. The whole drive is one btrfs device.
- devid 3 of the old `/mnt/data` pool, generation 128763. This is the last
  pool member.
- Same model and vintage as devid 1 (`Z500MRLD`). SMART is clean. The large
  `Raw_Read_Error_Rate` and `Seek_Error_Rate` raw values are Seagate's
  encoded counters, not real errors.

Status:

- 2026-10-26 13:59: started the image with the `recover-seagate3` unit.
  Pass 1 (`-n`) skips the bad areas. Pass 2 (`-d -r3`) retries them. The
  first estimate is about 3.75 h. `seal-seagate3` seals it after.
- 2026-10-26 23:25: the image finished after about 9.5 h. ddrescue rescued
  all 3,000,592,982,016 bytes. 0 bad bytes, 0 read errors. A clean read.
  The inner tracks read slow, so the real time was far over the first
  estimate. That is normal for this model. Detached the drive. The
  `seal-seagate3` unit saves `smart-after.txt` and seals the image.
- 2026-10-26: sealed. SHA-256 `87e808a38b4c50ae…2f6727e7` in
  `image.img.sha256`; `chmod 444` on the image, map, and hash file. All four
  pool members are now imaged and sealed.
- 2026-10-26: the `mount-oldpool` unit (`/tmp/mount-oldpool.sh`) made the 4
  reflink `work.img` copies, attached them read-only, and mounts the pool at
  `/mnt/olddata`. The log is `/mnt/disk1/data/recover/oldpool-mount.log`.

### sde-seagate-Z5006GSW

| | |
| --- | --- |
| Drive | Seagate ST3000DM001-1ER166, 3 TB, 7200 rpm, serial `Z5006GSW` |
| SMART | passed; 13,899 power-on hours, 29 power cycles, 0 reallocated, 0 pending, 0 offline uncorrectable, 0 reported uncorrect, 0 CRC |
| Image | `/mnt/disk3/data/recover/sde-seagate-Z5006GSW/image.img` (sparse) |
| Files | `smart-before.txt`, `gpt.txt`, `zfs-label0.bin` |

Findings:

- GPT, not a raw btrfs device. Part 1 is `zfs` (2.7 TiB). Part 9 is the 8 MiB
  ZFS reserved partition.
- Member of a 3-disk ZFS raidz1 pool named `data`, host `dmikalova-server`,
  `ashift=12`. This is a separate, older pool from the btrfs `/mnt/data`
  pool. See "The old ZFS pool".
- The label `vdev_tree` names the 3 members by
  `ata-ST3000DM001-1ER166_<serial>-part1`: `Z50055EX`, `Z5006P1Z`,
  `Z5006GSW`.

Status:

- 2026-10-26 23:34: started the image with the `recover-zfs-Z5006GSW` unit.
  Pass 1 (`-n`), then pass 2 (`-d -r3`). First estimate about 4.5 h.
  `seal-zfs-Z5006GSW` detaches the drive and seals the image after.
- Next: image the other 2 members (`Z50055EX`, `Z5006P1Z`). Then import the
  pool read-only from reflink copies.

### sde-wd-WCC4E7HP6U7J

| | |
| --- | --- |
| Drive | WD Red WD40EFRX, 4 TB, serial `WD-WCC4E7HP6U7J` |
| SMART | passed; 16,452 power-on hours, 0 reallocated, **1 pending**, 11 raw read errors, no errors in the error log |
| Image | `/mnt/disk3/data/recover/sde-wd-WCC4E7HP6U7J/image.img` (sparse) |
| Files | `smart-before.txt`, `smart-after.txt`, `btrfs-super.txt` |

Findings:

- No partition table. The whole drive is one btrfs device.
- devid 2 of the old `/mnt/data` pool, dev_uuid
  `afd881a8-b886-4e15-ac86-2f7aea78d500`, generation 128763. The drive
  holds 3.07 TB of chunks.
- The drive has a copy of the SYSTEM chunks.
- The old server logged 1 write error and 12 read errors on this drive. The
  1 pending sector agrees with that.

Status:

- 2026-10-24 22:00: started the image with the `recover-wdred4tb` unit.
  Pass 1 (`-n`) skips the bad areas. Pass 2 (`-d -r3`) retries them.
- 2026-10-25 20:34: the image finished after 22 h. ddrescue rescued
  4,000,787,030,016 of 4,000,787,030,016 bytes. 306,176 bytes (299 KiB) in
  84 small spots stayed unreadable. The bad spots are spread between 83 GB
  and 2.8 TB in, which fits a worn drive, not a failing one. 299 KiB lost
  out of 3.07 TB means a few damaged files at most. btrfs checksums will
  name them when the pool mounts.
- 2026-10-25: detached the drive. The `seal-wdred4tb` unit saved
  `smart-after.txt` and sealed the image. SHA-256
  `0fb69ddbc845…bd1ed7e9` in `image.img.sha256`; `chmod 444` on the image,
  map, and hash file.
- 2026-10-25: `smart-after.txt` shows `Current_Pending_Sector` at 99, up
  from 1. The pass 2 retries read the worn areas hard and flagged more
  sectors as pending. The drive still reports PASSED and 0 reallocated. This
  drive is a discard, so the higher pending count does not matter.

### sdf-sdcard-512gb

| | |
| --- | --- |
| Drive | SD card, 512 GB, exFAT, 288 GiB used |
| Reader | NORELSYS 1081 USB card reader |
| Copy | `~/data/mess/sd-card-512gb/` |

Findings:

- A healthy card. `backup/` has 7,320 files: music, Keymander, taxes,
  images, scripts and Ableton projects.
- The card reads at only about 3 MB/s. The Mac SD slot and the USB reader
  give the same speed, so the limit is the card.
- No image. The card is healthy, and the files go to `mess` directly.

Status:

- 2026-10-24: started at 13:44 with the `copy-sdcard` unit:
  `rsync -rt` from `/mnt/sdcard` (mounted `ro`). The copy excludes the
  macOS metadata: `.Spotlight-V100`, `.fseventsd`, `._*` and `.Trashes`.
- 2026-10-25 14:30: the copy finished. It took 24 h 45 min at 3.4 MB/s
  for 307.3 GB.
- 2026-10-25: verified with `rsync -rt --dry-run --itemize-changes`. All
  7,310 files agree in size and time. The 111 other files on the card are
  the excluded macOS metadata. rsync checks each file with a checksum while
  it copies, so a second `--checksum` pass (about 25 h) was not done.
- 2026-10-25: unmounted the card. The card can be removed.
- 2026-10-25: SnapRAID added the copy to parity. Kopia backs it up with
  `mess`.
- 2026-10-25: compared a second 512 GB card (exFAT UUID `6131-3763`) with
  the copy. The file paths and sizes agree byte for byte: 7,310 files. One
  file has a time that is 25 s different. Its SHA-256 is the same. The
  second card is a duplicate and needs no copy.
