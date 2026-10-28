set -euo pipefail

# mgmt hands its exec resources a bare environment: no HOME, no bus address.
# Derive both rather than assume them. Without HOME, set -u fails this script
# on the last line, after the work is already done, so the sentinel is never
# written and every later run repeats a nine gigabyte pull.
HOME="$(getent passwd "$(id -u)" | cut -d: -f6)"
export HOME
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"

# Quadlets are not enabled the way ordinary units are. A generator reads the
# .container file at daemon-reload and writes the service, so the reload is
# what installs it and [Install] in the file is what starts it at boot. There
# is no ollama.service on disk to enable, and trying produces a unit not
# found.
#
# restart, not start: start is a no-op on a unit that is already running, so a
# change to a .container file would be written and never taken up. This script
# only runs when its sentinel is bumped, which is exactly when the file did
# change, so the restart is never gratuitous.
systemctl --user daemon-reload

# /srv/appdata/beszel and the hub's key pair are made by the host layer, so
# the hub adopts a key the agent already trusts instead of generating its own.
#
# The hub goes first. It is what reports a failed ollama, so an ollama that
# cannot start must not keep it down.
systemctl --user restart beszel.service
systemctl --user restart ollama.service

# Wait for the API rather than the container, because the container is up well
# before the server is listening and the pull below would race it.
for _ in $(seq 60); do
  if curl -fsS http://127.0.0.1:11434/api/version >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

# The weights are not in the image. Pulling here means the first request is
# fast instead of blocking for nine gigabytes, and it is idempotent: ollama
# checks the manifest and returns immediately when the model is already local.
podman exec ollama ollama pull qwen3:14b

# Paseo reads its secrets from an env file that den-user-env.service writes.
# On a new install that file comes at the first boot with the sealed key, and
# [Install] starts Paseo then. Until that boot, there is nothing to start.
for app in paseo paseo-work; do
  if [ -f "/run/den-env/${app}.env" ]; then
    systemctl --user reset-failed "${app}.service" || true
    systemctl --user restart "${app}.service"
  else
    echo "quadlets: /run/den-env/${app}.env is missing; ${app} starts at the next boot" >&2
  fi
done

# The vpn pod has no tunnel without the AirVPN keys, and its units have a
# condition on the env file. A restart of the pod stops every container in it
# and starts them again on the new files, gluetun first.
if [ -f /run/den-env/gluetun.env ]; then
  systemctl --user reset-failed vpn-pod.service gluetun.service qbittorrent.service slskd.service || true
  systemctl --user restart vpn-pod.service
else
  echo "quadlets: /run/den-env/gluetun.env is missing; the vpn pod starts at the next boot" >&2
fi

mkdir -p "${HOME}/.local/state/den"
rm -f "${HOME}"/.local/state/den/quadlets-v*
touch "${HOME}/.local/state/den/quadlets-v18"
