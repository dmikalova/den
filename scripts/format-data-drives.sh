#!/usr/bin/env bash
#
#  ########################################################################
#  #                                                                      #
#  #   THIS SCRIPT ERASES WHOLE HARD DRIVES. IT DESTROYS ALL DATA ON      #
#  #   THEM. THERE IS NO UNDO AND NO SNAPSHOT THAT SURVIVES IT.           #
#  #                                                                      #
#  #   Run it only when building the array for the first time, or when    #
#  #   replacing a dead drive. Never run it to "fix" a mount problem.     #
#  #                                                                      #
#  ########################################################################
#
# Nothing calls this. mgmt does not and must not: a mkfs behind a sentinel
# file is one lost file away from erasing the array, so making the
# filesystems stays a deliberate act by a human who typed the confirmation.
#
# It exists so that act is reproducible. Rebuilding den must not depend on
# anyone remembering which drive was parity or which mkfs flags were used.
#
# The drive list is read from the mount units rather than repeated here, so
# adding a drive means editing one file and this follows.
#
# Reinstalling the OS does NOT require this. The autoinstall only touches the
# NVMe, so the data drives survive a rebuild untouched and merely need
# mounting. Running this after an OS reinstall is how you would lose the data
# the reinstall preserved.
#
# Usage:
#   ./scripts/format-data-drives.sh            show what would happen
#   ./scripts/format-data-drives.sh --erase    actually do it

set -euo pipefail

units=mgmt/files/etc/systemd/system
[ -d "$units" ] || units=/etc/systemd/system

erase=false
[ "${1:-}" = "--erase" ] && erase=true

plan=()
for name in mnt-disk1 mnt-disk2 mnt-disk3 mnt-parity1; do
    unit=$units/$name.mount
    [ -f "$unit" ] || {
        echo "missing $unit" >&2
        exit 1
    }

    what=$(sed -n 's/^What=//p' "$unit")
    where=$(sed -n 's/^Where=//p' "$unit")
    fstype=$(sed -n 's/^Type=//p' "$unit")
    label=den-$(basename "$where")

    [ -e "$what" ] || {
        echo "no such drive: $what" >&2
        exit 1
    }
    dev=$(readlink -f "$what")

    # The one check that matters. A drive with anything mounted on it is in
    # use, and the boot drive always is, so this is also what stops a typo in
    # a unit from pointing mkfs at the running system.
    if lsblk -no MOUNTPOINT "$dev" | grep -q .; then
        echo "refusing: $what ($dev) has mounted filesystems" >&2
        exit 1
    fi

    existing=$(lsblk -no FSTYPE,LABEL "$dev" | head -1 | xargs || true)
    printf '%-10s %-9s %-14s %s\n' "$where" "$fstype" "$label" "$what"
    [ -n "$existing" ] && printf '%-10s %s\n' "" "ERASES EXISTING: $existing"

    plan+=("$fstype|$label|$dev")
done

if ! $erase; then
    echo
    echo "dry run. Re-run with --erase to destroy and recreate the above."
    exit 0
fi

echo
echo "This erases every drive listed above. Type ERASE ALL DATA to continue."
read -r reply
[ "$reply" = "ERASE ALL DATA" ] || {
    echo "aborted"
    exit 1
}

for entry in "${plan[@]}"; do
    IFS='|' read -r fstype label dev <<<"$entry"
    case $fstype in
    btrfs)
        mkfs.btrfs -q -f -L "$label" "$dev"
        ;;
    ext4)
        # No reserved blocks: nothing but snapraid parity lives here, and root
        # never needs headroom on it. largefile4 trades inodes for the space
        # one huge parity file actually wants.
        mkfs.ext4 -q -F -m 0 -T largefile4 -L "$label" "$dev"
        ;;
    *)
        echo "unknown filesystem $fstype" >&2
        exit 1
        ;;
    esac
    echo "made $fstype $label"
done

echo
echo "Done. mgmt mounts these on the next converge, or:"
echo "  sudo systemctl start mnt-disk1.mount mnt-disk2.mount mnt-disk3.mount mnt-parity1.mount mnt-storage.mount"
