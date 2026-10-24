#!/bin/bash
# kopia ships its own apt repo and is in no Ubuntu component, which is why this
# is an exec and not a pkg resource.
#
# The version is whatever the repo offers. A backup tool that cannot read a
# repository written by a newer build of itself is a backup tool with a hole in
# it, so this tracks the stable channel rather than pinning.
set -euo pipefail

curl -fsSL https://kopia.io/signing-key |
  gpg --batch --yes --dearmor -o /usr/share/keyrings/kopia-keyring.gpg
cat >/etc/apt/sources.list.d/kopia.list <<'EOF'
deb [signed-by=/usr/share/keyrings/kopia-keyring.gpg] http://packages.kopia.io/apt/ stable main
EOF
apt-get -o DPkg::Lock::Timeout=600 update
DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=600 install -y kopia

touch /var/lib/den/kopia-v1
