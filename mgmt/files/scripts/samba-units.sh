#!/bin/bash
# nmbd is NetBIOS name service. den is found over the tailnet by name, the LAN
# never sees smbd, and nmbd broadcasting on an interface it is not serving is
# noise at best.
#
# smbd is enabled here rather than left to the package so that the order is
# written down: den-samba.service puts the password in place, and smbd is of
# no use before it. Its drop-in keeps it down while the pool is not mounted.
set -euo pipefail
systemctl daemon-reload
systemctl disable --now nmbd.service || true
systemctl mask nmbd.service
systemctl enable den-samba.service
systemctl enable smbd.service
touch /var/lib/den/samba-units-v2
