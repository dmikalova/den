#!/bin/bash
# Prepare the hub's data directory before the hub has ever run.
#
# Two things have to be in place first, and both are one-shot. The SSH key
# pair, because the hub generates it on first start otherwise and the agent
# would have nothing to trust until a key had been copied out of a web UI; the
# hub reuses a pair it finds. And the password, because the account is created
# from USER_EMAIL and USER_PASSWORD during the hub's first migration, which is
# the only chance to set it without filling in a form.
#
# Neither half leaves this host, and /srv/appdata is backed up, so both survive
# a rebuild along with the hub's database.
set -euo pipefail

dir=/srv/appdata/beszel
key="${dir}/id_ed25519"
env_file="${dir}/hub.env"

install -d -m 0755 -o dmikalova -g dmikalova "${dir}"

if [[ ! -f ${key} ]]; then
  ssh-keygen -t ed25519 -N '' -C beszel-hub -f "${key}"
fi

# The hub is a rootless container running as dmikalova, so it has to own the
# key it is about to read.
chown dmikalova:dmikalova "${key}" "${key}.pub"
chmod 0600 "${key}"
chmod 0644 "${key}.pub"

# Random, and never typed: AUTO_LOGIN means the login form is never reached.
# Kept rather than discarded so there is a way back in if AUTO_LOGIN is ever
# turned off, and never regenerated, because only the first start reads it.
if [[ ! -f ${env_file} ]]; then
  printf 'USER_PASSWORD=%s\n' "$(openssl rand -hex 24)" >"${env_file}"
fi
chown dmikalova:dmikalova "${env_file}"
chmod 0600 "${env_file}"

touch /var/lib/den/beszel-hub-v1
