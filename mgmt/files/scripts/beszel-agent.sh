#!/usr/bin/env bash
# Install the Beszel agent.
#
# Beszel publishes no apt repository, so this takes the release tarball rather
# than adding a third-party source like kopia and caddy do. The version is
# pinned to the hub's: an agent that moves on its own is a monitor nobody can
# trust. Bump this and mgmt/user/files/containers/systemd/beszel.container
# together, and bump the sentinel below so the install runs again.
#
# The .deb release asset is deliberately not used. It ships its own unit and
# its own user, which mgmt would then spend every run arguing with.
set -euo pipefail

version="0.21.0"
url="https://github.com/henrygd/beszel/releases/download/v${version}/beszel-agent_linux_amd64.tar.gz"

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

curl -fsSL --retry 3 "${url}" -o "${tmp}/agent.tar.gz"
tar -xzf "${tmp}/agent.tar.gz" -C "${tmp}" beszel-agent

install -d -m 0755 /opt/beszel-agent
install -m 0755 "${tmp}/beszel-agent" /opt/beszel-agent/beszel-agent

install -d -m 0755 /var/lib/den
touch /var/lib/den/beszel-agent-v1
