set -euo pipefail

user=dmikalova
uid=$(id -u "$user")
store=/home/$user/.local/share/containers

systemctl daemon-reload

# /home first: the container store mounts inside it.
systemctl enable --now home.mount
# systemd escapes each "-" in a mount path, so /srv/db-dumps is srv-db\x2ddumps.
systemctl enable --now srv-appdata.mount srv-db.mount 'srv-db\x2ddumps.mount' srv-cache.mount

top=$(mktemp -d)
mount -o subvolid=5 /dev/mapper/den-root "$top"
trap 'umount "$top" 2>/dev/null || true; rmdir "$top" 2>/dev/null || true' EXIT

# One time, on a den from before docs/storage.md: @models, @containers and
# @dumps become directories of @cache and @db-dumps. A fresh install has none
# of them and skips all of this.
if [ -e "$top/@models" ] || [ -e "$top/@containers" ] || [ -e "$top/@dumps" ]; then
  # Stop every quadlet so nothing holds the image store or the weights. The
  # user layer starts them again with the new paths. Starting them here would
  # start ollama with its old Volume= and fail it.
  if [ -d "/run/user/$uid" ]; then
    as_user() { runuser -u "$user" -- env HOME="/home/$user" XDG_RUNTIME_DIR="/run/user/$uid" "$@"; }
    units=$(as_user podman ps --format '{{index .Labels "PODMAN_SYSTEMD_UNIT"}}' | sort -u)
    for unit in $units; do
      as_user systemctl --user stop "$unit"
    done
  fi

  # The old units mounted the subvolumes by name. Unmount them before the move.
  for path in "$store" /srv/models /srv/dumps; do
    if mountpoint -q "$path"; then umount "$path"; fi
  done
  systemctl disable srv-models.mount srv-dumps.mount 2>/dev/null || true

  # A move inside one btrfs filesystem is a rename: no copy, no download. The
  # moved subvolumes stay subvolumes nested in @cache, which changes nothing:
  # @cache has no snapshots and no backup.
  if [ -e "$top/@models" ] && [ ! -e "$top/@cache/models" ]; then
    mv "$top/@models" "$top/@cache/models"
  fi
  if [ -e "$top/@containers" ] && [ ! -e "$top/@cache/containers" ]; then
    mv "$top/@containers" "$top/@cache/containers"
  fi
  # No database wrote a dump before this layout, so @dumps is empty. Delete it
  # only if that is still true.
  if [ -e "$top/@dumps" ]; then
    if [ -z "$(ls -A "$top/@dumps")" ]; then
      btrfs subvolume delete "$top/@dumps"
    else
      echo "@dumps is not empty; move its contents to /srv/db-dumps by hand" >&2
      exit 1
    fi
  fi
  rmdir /srv/models /srv/dumps 2>/dev/null || true
fi

# Ollama pulls weights as the login user, and rootless podman owns its store.
install -d -o "$user" -g "$user" /srv/cache/containers /srv/cache/models

# systemd creates a missing mountpoint as root. Pre-create the parents with the
# right owner so only the leaf is systemd's, then fix the leaf. restart, not
# start: on a migrated den the unit is already loaded with its old What=.
install -d -o "$user" -g "$user" "/home/$user/.local" "/home/$user/.local/share"
systemctl enable 'home-dmikalova-.local-share-containers.mount'
systemctl restart 'home-dmikalova-.local-share-containers.mount'
chown "$user:$user" "$store"

touch /var/lib/den/btrfs-mounts-v3
