set -euo pipefail

systemctl daemon-reload

# /home first: the container store mounts inside it.
systemctl enable --now home.mount
systemctl enable --now srv-appdata.mount srv-dumps.mount srv-models.mount

# systemd creates a missing mountpoint as root. Pre-create the parents with the
# right owner so only the leaf is systemd's, then fix the leaf.
install -d -o dmikalova -g dmikalova /home/dmikalova/.local /home/dmikalova/.local/share
systemctl enable --now 'home-dmikalova-.local-share-containers.mount'
chown dmikalova:dmikalova /home/dmikalova/.local/share/containers

# Ollama pulls weights as the login user.
chown dmikalova:dmikalova /srv/models

touch /var/lib/den/btrfs-mounts-v2
