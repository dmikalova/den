#!/bin/bash
# Netdata ships enabled and talking to its cloud and its registry. This turns
# that off, and leaves it bound to localhost behind Caddy.
set -euo pipefail

# Opt out of the anonymous statistics upload. The file's existence is the
# switch; there is no setting for it in netdata.conf.
touch /etc/netdata/.opt-out-from-anonymous-statistics

# Netdata Cloud is a separate opt-in and claiming is a deliberate act, so this
# only makes sure nothing claimed it during install.
rm -f /var/lib/netdata/cloud.d/claimed_id

systemctl daemon-reload
systemctl enable --now netdata.service
systemctl restart netdata.service

touch /var/lib/den/netdata-units-v1
