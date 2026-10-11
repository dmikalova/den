#!/bin/bash
# den's own binary, installed on den. `den notify` is what every unit's
# OnFailure= runs, so it has to be present before the first failure rather than
# after the next laptop-driven apply.
#
# Bump `version` here and the matching sentinel in main.mcl together.
set -euo pipefail

version=1.6.0
base="https://github.com/dmikalova/den/releases/download/v${version}"

case "$(uname -m)" in
x86_64) arch=amd64 ;;
aarch64) arch=arm64 ;;
*)
	echo "no den release for $(uname -m)" >&2
	exit 1
	;;
esac

tarball="den_${version}_linux_${arch}.tar.gz"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

curl -fsSL "$base/${tarball}" -o "$work/${tarball}"
curl -fsSL "$base/den_${version}_checksums.txt" -o "$work/checksums.txt"

# Verify before unpacking. The archive arrives over the network and is about to
# be installed into a root-owned path on PATH.
(cd "$work" && grep " ${tarball}\$" checksums.txt | sha256sum -c -)

tar -xzf "$work/${tarball}" -C "$work" den
install -o root -g root -m 0755 "$work/den" /usr/local/bin/den

# Holds the Discord webhook and the healthcheck URL. Both are bearer
# credentials, so the directory is root-only and the files are placed by hand,
# never by this repository.
install -d -o root -g root -m 0700 /etc/den

touch "/var/lib/den/den-binary-v${version}"
