#!/bin/bash
# Enable and start the Beszel agent.
#
# Started here, unlike den-samba.service: beszel-hub.sh has already written
# the key this agent trusts, so there is nothing left to wait for.
set -euo pipefail

systemctl daemon-reload
systemctl enable --now beszel-agent.service

touch /var/lib/den/beszel-agent-units-v1
