#!/bin/bash
# Enable Caddy and the path unit that reloads it.
#
# The config is validated before caddy is started, so a bad Caddyfile fails
# here rather than leaving the host with nothing in front of it.
set -euo pipefail

/usr/local/bin/caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile

systemctl daemon-reload
systemctl enable caddy.service
systemctl enable --now den-caddy-reload.path

# Started only once the sealed key exists, the same way den-secrets.service is
# enabled but not started. caddy.service wants
# LoadCredentialEncrypted=gcp-key, and `den apply` seals that key after this
# converge, so on a fresh host there is nothing to open yet. Starting it
# anyway fails the converge, and that failure returns from `den apply` before
# it ever reaches the sealing step, which leaves the host unable to fix
# itself. On a fresh host, den apply starts caddy after the seal.
if [[ -f /etc/den/credentials/gcp-key.cred ]]; then
  systemctl start caddy.service
  systemctl reload caddy.service
fi

touch /var/lib/den/caddy-units-v3
