#!/bin/bash
# Default deny inbound.
#
# The LAN rule is not optional: den is reached over the LAN during bootstrap,
# before it is on the tailnet, and losing it means a trip to the physical
# console. reset drops every rule first, so the allows must follow it here and
# not in a later run.
set -euo pipefail
ufw --force reset
ufw default deny incoming
ufw default allow outgoing
ufw allow in on tailscale0
ufw allow from 192.168.0.0/24 to any port 22 proto tcp
ufw allow from 192.168.0.0/24 to any port 5353 proto udp
ufw --force enable
touch /var/lib/den/ufw-policy-v1
