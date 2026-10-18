#!/bin/bash
# Enable Caddy and the path unit that reloads it.
#
# The config is validated before caddy is started, so a bad Caddyfile fails
# here rather than leaving the host with nothing in front of it.
set -euo pipefail

/usr/local/bin/caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile

systemctl daemon-reload
systemctl enable --now caddy.service
systemctl enable --now den-caddy-reload.path
systemctl reload caddy.service

touch /var/lib/den/caddy-units-v2
