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

## Netdata

Netdata is at `http://den/netdata/`.

It is in the host layer, not a container, because its job is to see the host:
drive temperatures, ZRAM, mergerfs mounts and NVIDIA state are all things a
container would be fighting its own isolation to reach.

It binds `127.0.0.1:19999` and has no authentication of its own, which is why
the tailnet check in Caddy is its whole access control. The registry and the
anonymous statistics upload are both off, and nothing is claimed into Netdata
Cloud.

Netdata's own alarm notifications are left at their defaults, which go
nowhere. Alerting on den is `den-notify@`, which reports failed units to
Discord; Netdata is for looking at after something has already said that
there is a problem.
