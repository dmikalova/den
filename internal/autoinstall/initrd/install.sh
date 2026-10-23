#!/bin/bash
# Installs SSH unlock for the LUKS passphrase prompt at boot, then rebuilds
# every initramfs. Run as root on den, from a copy of this folder that also
# holds the authorized_keys to let in.
set -euo pipefail

here=$(dirname "$(readlink -f "$0")")

if [ ! -s "$here/authorized_keys" ]; then
  echo "no authorized_keys in $here: nobody could log in" >&2
  exit 1
fi
install -D -m 0600 "$here/authorized_keys" /etc/dracut-sshd/authorized_keys

# Without PAM, sshd refuses every login to an account whose password field is
# "!", but still takes a key for "*".
usermod -p '*' root

for m in 45den-net 46sshd; do
  rm -rf "/usr/lib/dracut/modules.d/$m"
  cp -r "$here/$m" "/usr/lib/dracut/modules.d/$m"
  chmod 0755 "/usr/lib/dracut/modules.d/$m/module-setup.sh"
done

update-initramfs -u -k all

for img in /boot/initrd.img-*; do
  if ! lsinitrd "$img" | grep 'usr/sbin/sshd$' >/dev/null; then
    echo "$img has no sshd: the passphrase prompt is console only" >&2
    exit 1
  fi
done
