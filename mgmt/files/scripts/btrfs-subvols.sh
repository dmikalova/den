set -euo pipefail

# The subvolumes live at the top of the filesystem, which is not reachable from
# / because / is itself the @ subvolume. subvolid=5 is the top.
top=$(mktemp -d)
mount -o subvolid=5 /dev/mapper/den-root "$top"
trap 'umount "$top" 2>/dev/null || true; rmdir "$top" 2>/dev/null || true' EXIT

for sv in @home @appdata @dumps @models @containers; do
	[ -e "$top/$sv" ] || btrfs subvolume create "$top/$sv"
done

# One-time copy of whatever the install left in @/home. Once the mount unit
# takes over those files are shadowed rather than deleted, so this is safe to
# get wrong once. Skipped if /home is already a mount, which means a previous
# run finished.
if ! mountpoint -q /home; then
	rsync -aHAX --numeric-ids /home/ "$top/@home/"
fi

touch /var/lib/den/btrfs-subvols-v1
