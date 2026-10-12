set -euo pipefail

# 0000 root:root underneath every mount. If a drive fails to mount, a write to
# that path hits an unwritable directory instead of silently filling the root
# filesystem, which is the failure mode that makes a pool look fine until it is
# full.
for d in /mnt/disk1 /mnt/disk2 /mnt/disk3 /mnt/parity1 /mnt/storage; do
    install -d -o root -g root -m 0000 "$d"
done

systemctl daemon-reload
systemctl enable --now mnt-disk1.mount mnt-disk2.mount mnt-disk3.mount mnt-parity1.mount
systemctl enable --now mnt-storage.mount

# The pool is the user's data directory, so it is theirs once mounted. The 0000
# above guards the directory underneath, which is now shadowed.
chown dmikalova:dmikalova /mnt/storage
chmod 0755 /mnt/storage

ln -sfn /mnt/storage /home/dmikalova/data
chown -h dmikalova:dmikalova /home/dmikalova/data

touch /var/lib/den/storage-mounts-v1
