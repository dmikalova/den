#!/bin/bash
# Caddy is not in Ubuntu's archive, so it comes from the project's own apt
# repository rather than a pkg resource.
#
# Tracking stable rather than pinning a version: this is the thing in front of
# everything else, which makes it the thing that most needs security updates
# without a human in the loop.
set -euo pipefail

apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl

curl -fsSL 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' |
	gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg

# The published list file already names the keyring above.
curl -fsSL 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
	-o /etc/apt/sources.list.d/caddy-stable.list

apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y caddy

touch /var/lib/den/caddy-v1
