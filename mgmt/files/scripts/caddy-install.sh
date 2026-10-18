#!/usr/bin/env bash
# Install Caddy with the Cloud DNS plugin.
#
# Not the apt repository any more. ACME DNS-01 needs caddy-dns/googleclouddns
# compiled in, and Caddy has no runtime plugin loading, so a stock binary
# cannot do it at all. caddyserver.com builds on request, which is what
# `caddy add-package` does under the hood.
#
# Only the plugin is pinned; the Caddy version is whatever that API calls
# current. The sentinel is what stops it drifting: a rebuild happens when the
# version below is bumped, not when upstream releases.
set -euo pipefail

plugin="github.com/caddy-dns/googleclouddns@v1.1.0"

# The apt package ships its own caddy.service and its own binary path. Leaving
# it installed means unattended-upgrades quietly replacing a Caddy that can do
# DNS-01 with one that cannot.
if dpkg -s caddy >/dev/null 2>&1; then
  systemctl disable --now caddy.service || true
  DEBIAN_FRONTEND=noninteractive apt-get purge -y caddy
fi
rm -f /etc/apt/sources.list.d/caddy-stable.list \
  /usr/share/keyrings/caddy-stable-archive-keyring.gpg

# Access logs go to the journal now. The leftover file is root-owned 0600,
# which is what wedged every reload while it was still in the Caddyfile.
rm -rf /var/log/caddy

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

curl -fsSL --retry 3 --max-time 600 \
  "https://caddyserver.com/api/download?os=linux&arch=amd64&p=${plugin}" \
  -o "${tmp}/caddy"
install -m 0755 "${tmp}/caddy" /usr/local/bin/caddy

# The package used to supply this. Owning the unit means owning the user too.
if ! id caddy >/dev/null 2>&1; then
  useradd --system --home /var/lib/caddy --shell /usr/sbin/nologin caddy
fi

install -d -m 0755 /etc/caddy
install -d -m 0755 /var/lib/den
touch /var/lib/den/caddy-v2
