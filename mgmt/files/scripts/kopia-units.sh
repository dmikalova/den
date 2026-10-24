#!/bin/bash
# The timer is enabled and started; the service is not. Enabling a
# timer-driven oneshot would also run it at boot, and the first run uploads a
# whole array.
#
# The first backup therefore happens at the next 22:00 window, or sooner if
# someone would rather watch it:
#   systemctl start kopia-backup.service
set -euo pipefail
systemctl daemon-reload
systemctl enable --now kopia-backup.timer kopia-restore-drill.timer
touch /var/lib/den/kopia-units-v3
