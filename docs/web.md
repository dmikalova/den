# The web layer

Caddy is the only thing on den that listens for HTTP. Everything with a web
interface binds `127.0.0.1` and is reached through it.

That ordering is deliberate and it came before the first container: with a
proxy in place, adding an app is one block in one file. Without it, every app
invents its own port, its own TLS story and its own answer to who may reach
it.

## Reach

The listener is on port 80, plain HTTP. The only route to it is the tailnet,
which is encrypted already, and terminating TLS would mean either a public
name or a private CA that every client has to be told about. When
`den.mklv.tech` exists this gets a second site block with real certificates;
the tailnet one stays.

Two things keep it to the tailnet:

- `ufw` denies inbound from the LAN except ssh and mDNS.
- Caddy answers `403` to any source outside `100.64.0.0/10` and loopback.

The second exists because the first is one `ufw reset` away from being wrong.

## Adding an app

Add a block to [mgmt/files/etc/caddy/Caddyfile](../mgmt/files/etc/caddy/Caddyfile):

```caddyfile
handle_path /thing/* {
    reverse_proxy 127.0.0.1:8080
}
```

`den-caddy-reload.path` watches the file and reloads Caddy when it changes, so
there is no sentinel to bump and nothing to remember. A bad config fails the
reload unit and leaves the running config in place, so the proxy does not go
down for a typo.

## Beszel

Beszel is at `http://den/beszel/`.

It replaces Netdata, which Ubuntu dropped from its archive before 26.04 with
no replacement. The swap is also an improvement: the thing that breaks on den
is systemd, not the CPU. `kopia-backup.timer` does not fire, `den-samba.service`
dies, `den-reconcile` wedges. Beszel reports unit state, restart counts and the
tail of each unit's journal. Netdata reported none of that.

Beszel has two halves.

The agent is in the host layer, not a container, because its job is to see the
host: SMART health, drive temperatures, mergerfs mounts and NVIDIA state are
all things a container would be fighting its own isolation to reach. It runs
as root for the raw-device reads SMART needs, and in the `systemd-journal`
group for the logs. It listens on `127.0.0.1:45876`, and the hub reaches it
there over SSH. Both ends are on this host, so that connection never leaves
loopback.

The hub is a rootless quadlet in the user layer, like every other app. It
binds `127.0.0.1:8090` and keeps a PocketBase database on `/srv/appdata/beszel`,
so it is backed up with everything else there.

Unlike Netdata, the hub has a login of its own, so the tailnet check in Caddy
is a second lock rather than the only one. It is still loopback-only: a login
is not a reason to put an admin panel on the internet.

The agent authenticates the hub with the hub's public key. Nothing in this
feature is a secret and nothing is copied: both halves sit on this host at
`/srv/appdata/beszel/id_ed25519`, and the agent reads the `.pub` half in place.

The hub would normally generate that pair on its own first start, which would
leave the agent with nothing to trust until someone had copied a key out of a
web UI. It reuses a key it finds instead, so `beszel-hub.sh` writes the pair
before the hub has ever run. The agent is handed a key on the same pass that
installs it.

The private half never leaves den. `/srv/appdata` is backed up, so the pair
survives a rebuild along with the hub's database rather than every agent
registration being invalidated.

The system den monitors is declared in `mgmt/files/srv/appdata/beszel/config.yml`,
which the hub syncs into its database on every start and prunes against, so
that file is the whole list. Nothing is registered by hand.

There is no login either. `AUTO_LOGIN` authorises every request as the hub's
admin account, which `USER_EMAIL` and `USER_PASSWORD` create during the hub's
first migration. Reaching the hub at all already means passing Caddy's tailnet
check and ufw, and the hub binds loopback, so a password form would be a second
lock on the same door and one more thing to restore after a rebuild.

The password is still generated, by `beszel-hub.sh`, and kept at
`/srv/appdata/beszel/hub.env`. Nobody types it. It exists so there is a way
back in if `AUTO_LOGIN` is ever removed.

`AUTO_LOGIN` is not in Beszel's documented environment variables, only in
`internal/hub/api.go`. Check it still exists when bumping the image.

The array members and parity are separate filesystems, so the agent unit names
them in `EXTRA_FILESYSTEMS`. Without that only the mergerfs pool is reported
and a single failing disk is invisible. btrfs pools are discovered on their
own; scrub status and per-subvolume usage are not reported, so those stay a
`snapraid`/`snapper` matter.

Beszel can send its own alerts. Alerting on den is `den-notify@`, which reports
failed units to Discord; Beszel is for looking at after something has already
said that there is a problem.
