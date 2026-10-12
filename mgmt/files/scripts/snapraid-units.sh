set -euo pipefail

# The fourth content copy lives on the root filesystem, so it needs a home that
# is not one of the drives it protects.
install -d -o root -g root -m 0755 /var/lib/snapraid

systemctl daemon-reload
systemctl enable --now snapraid-sync.timer snapraid-scrub.timer

touch /var/lib/den/snapraid-units-v1
