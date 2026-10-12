set -euo pipefail

# 0000 root:root underneath every mount. If a drive fails to mount, a write to
# that path hits an unwritable directory instead of silently filling the root
# filesystem, which is the failure mode that makes a pool look fine until it is
# full.
#
# Guarded by mountpoint: run on a mounted path this would chmod the drive's own
# root directory instead, which persists on the drive and locks the pool out.
for d in /mnt/disk1 /mnt/disk2 /mnt/disk3 /mnt/parity1 /mnt/storage; do
    mountpoint -q "$d" || install -d -o root -g root -m 0000 "$d"
done

systemctl daemon-reload
systemctl enable --now mnt-disk1.mount mnt-disk2.mount mnt-disk3.mount mnt-parity1.mount

# The mergerfs branches. They have to exist on the drives before the pool can
# mount, and they live one level down so that snapper's .snapshots subvolume is
# a sibling rather than something the pool would show.
#
# The drive roots must be traversable: mergerfs acts as the calling user, so a
# root-only drive directory makes the whole pool read as missing.
for d in /mnt/disk1 /mnt/disk2 /mnt/disk3; do
    chmod 0755 "$d"
    install -d -o dmikalova -g dmikalova -m 0755 "$d/data"
done

systemctl enable --now mnt-storage.mount

# The pool is the user's data directory, so it is theirs once mounted. The 0000
# above guards the directory underneath, which is now shadowed.
chown dmikalova:dmikalova /mnt/storage
chmod 0755 /mnt/storage

ln -sfn /mnt/storage /home/dmikalova/data
chown -h dmikalova:dmikalova /home/dmikalova/data

touch /var/lib/den/storage-mounts-v2
