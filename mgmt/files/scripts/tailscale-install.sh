#!/bin/bash
# Tailscale's apt repo has to exist before the package is visible, which is why
# this is an exec and not a pkg resource.
set -euo pipefail
. /etc/os-release
base=https://pkgs.tailscale.com/stable/ubuntu
curl -fsSL "$base/${VERSION_CODENAME}.noarmor.gpg" \
  -o /usr/share/keyrings/tailscale-archive-keyring.gpg
curl -fsSL "$base/${VERSION_CODENAME}.tailscale-keyring.list" \
  -o /etc/apt/sources.list.d/tailscale.list
apt-get -o DPkg::Lock::Timeout=600 update
DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=600 install -y tailscale
touch /var/lib/den/tailscale-v1
