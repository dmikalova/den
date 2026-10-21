#!/bin/bash
# The .snapshots subvolume each snapper config points at. snapper create-config
# would make it, but it refuses one that already exists, and a reinstall keeps
# the data disks with their snapshots. So mgmt writes the configs and this only
# fills in what is missing: everything on a fresh NVMe, nothing on the disks.
set -euo pipefail

# snapperd reads SNAPPER_CONFIGS when it starts, and the apt hook may have
# started it before the configs existed.
systemctl try-restart snapperd.service

for pair in root:/ home:/home appdata:/srv/appdata disk1:/mnt/disk1 disk2:/mnt/disk2 disk3:/mnt/disk3; do
  cfg=${pair%%:*}
  snaps="${pair#*:}"
  snaps="${snaps%/}/.snapshots"
  btrfs subvolume show "$snaps" >/dev/null 2>&1 && continue

  btrfs subvolume create "$snaps"
  chmod 0750 "$snaps"
  # create-config is what applies SYNC_ACL to a new .snapshots; setting the same
  # ALLOW_USERS again is how to ask for it without create-config.
  if grep -q '^SYNC_ACL="yes"' "/etc/snapper/configs/$cfg"; then
    snapper -c "$cfg" set-config ALLOW_USERS=dmikalova
  fi
done

touch /var/lib/den/snapper-subvols-v1
