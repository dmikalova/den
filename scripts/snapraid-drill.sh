#!/usr/bin/env bash
#
# Prove that snapraid can give a byte back.
#
# The timers running and the content files existing prove only that snapraid is
# installed. Parity nobody has ever restored from is a belief, not a backup, and
# the usual way to find out it was a belief is the day a drive dies.
#
# This drill writes a file it owns, syncs it into parity, destroys it two ways,
# and restores it. It is the only snapraid operation here that deletes anything,
# and it only ever deletes the file it just created, under a directory named
# after itself.
#
# What it does NOT prove: that a whole dead drive can be rebuilt. That needs a
# spare drive and hours, and it is the drill to run before trusting the array
# with something irreplaceable.
#
# Run it on den, as root:
#   sudo ./scripts/snapraid-drill.sh
#
# It leaves the array as it found it, including a final sync.

set -euo pipefail

disk=${DRILL_DISK:-d1}
root=${DRILL_ROOT:-/mnt/disk1}
rel=/.snapraid-drill/canary
canary=$root$rel

[ "$(id -u)" = 0 ] || {
  echo "snapraid needs root" >&2
  exit 1
}

# snapraid takes a whole-array lock, so a drill racing the nightly sync fails
# halfway and leaves parity describing neither state.
for unit in snapraid-sync.service snapraid-scrub.service; do
  if systemctl is-active --quiet "$unit"; then
    echo "$unit is running; wait for it to finish" >&2
    exit 1
  fi
done

mountpoint -q "$root" || {
  echo "$root is not mounted" >&2
  exit 1
}

cleanup() {
  rm -rf "$(dirname "$canary")"
}

# A drill that dies partway leaves parity describing a file that is gone. The
# canary goes away either way; the sync that forgets it has to be run by hand.
failed() {
  cleanup
  echo "drill failed; run 'snapraid sync' before the next one" >&2
}
trap failed EXIT

echo "== writing the canary"
mkdir -p "$(dirname "$canary")"
# Random rather than a fixed string: a restore that returned a zeroed block, or
# the previous run's file, would still match a constant.
head -c 1048576 /dev/urandom >"$canary"
sync
want=$(sha256sum <"$canary" | cut -d' ' -f1)
echo "   $canary  $want"

echo "== syncing it into parity"
snapraid sync

# The complaint that started this drill: content files of 90 bytes mean snapraid
# is protecting nothing. One megabyte of canary cannot fit in 90 bytes of index.
size=$(stat -c %s /var/lib/snapraid/snapraid.content)
[ "$size" -gt 1024 ] || {
  echo "content file is $size bytes; snapraid has indexed nothing" >&2
  exit 1
}
echo "   content file is $size bytes"

echo "== case 1: the file is deleted"
rm -f "$canary"
snapraid fix -d "$disk" -f "$rel"
got=$(sha256sum <"$canary" | cut -d' ' -f1)
[ "$got" = "$want" ] || {
  echo "restored file does not match: $got" >&2
  exit 1
}
echo "   restored and identical"

echo "== case 2: the file is silently corrupted"
# Overwrite in place and put the timestamp back, so the size and mtime are
# unchanged. This is the failure snapraid exists for, and the one that a copy
# made at the filesystem level would happily copy.
when=$(stat -c %y "$canary")
printf 'rot' | dd of="$canary" bs=1 seek=4096 conv=notrunc status=none
touch -d "$when" "$canary"
sync
snapraid fix -d "$disk" -f "$rel"
got=$(sha256sum <"$canary" | cut -d' ' -f1)
[ "$got" = "$want" ] || {
  echo "repaired file does not match: $got" >&2
  exit 1
}
echo "   repaired and identical"

echo "== removing the canary and re-syncing"
cleanup
trap - EXIT
snapraid sync

echo
echo "snapraid returned a byte it was given. Parity is real."
