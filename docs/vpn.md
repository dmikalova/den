# VPN

The vpn pod runs the apps whose traffic must not leave from den's home
address. The design is in Notion: the egress isolation section of the den
page and the VPN entry of the decision log.

| Container     | Image                                  | Role                         |
| ------------- | -------------------------------------- | ---------------------------- |
| `gluetun`     | `qmcgaw/gluetun`                       | the AirVPN WireGuard tunnel  |
| `qbittorrent` | `qbittorrentofficial/qbittorrent-nox`  | torrents, public and private |
| `slskd`       | `slskd/slskd`                          | Soulseek                     |

The three containers share the network namespace of the pod. gluetun holds
the tunnel and a firewall in that namespace. The firewall drops all traffic
that does not go through the tunnel. Thus if the tunnel is down, the apps
have no network, and their traffic does not leak to the home address.

| App         | URL                                  | Local port |
| ----------- | ------------------------------------ | ---------- |
| qBittorrent | `https://qbittorrent.den.mklv.tech`  | `8080`     |
| slskd       | `https://slskd.den.mklv.tech`        | `5030`     |

The local ports listen on 127.0.0.1 only. Caddy is the path in.

## Private trackers

All torrents use the one qBittorrent and go through the tunnel. This includes
the private music trackers, Redacted (RED) and Orpheus (OPS). RED permits a
VPN for torrent traffic. Read the rules of each tracker before you add it.

- Do the tracker interview from the home address, not through a VPN. Both
  trackers refuse an interview from a VPN.
- Log in to the tracker website from your browser, as usual. Only the torrent
  traffic goes through the tunnel.
- Use only a client version on the client allowlist of the tracker. Check the
  allowlist before you bump the qBittorrent image.

If a tracker forbids a VPN, add a second qBittorrent outside the pod for that
tracker. qBittorrent binds all torrents to one interface. It cannot send some
torrents through the tunnel and some torrents around it.

## Ratio

On a private tracker, a client that peers can connect to uploads more. AirVPN
forwards a port on the VPN server to the tunnel, so peers can connect to den.
Each app has its own forwarded port. The firewall in gluetun lets in these
ports only.

The upload speed of den is the limit, with or without the tunnel. WireGuard
costs about 5% to 10% of the speed.

## Storage

Each app follows the [storage areas](storage.md).

| Path in the container                | Path on den                                   | Kopia | SnapRAID |
| ------------------------------------ | --------------------------------------------- | ----- | -------- |
| qBittorrent `/config`                | `/srv/appdata/qbittorrent`                    | yes   | no       |
| qBittorrent `/downloads`             | `/mnt/storage/downloads/torrents`             | yes   | yes      |
| qBittorrent `/downloads/.incomplete` | `/mnt/storage/downloads/torrents/.incomplete` | no    | no       |
| slskd `/app`                         | `/srv/appdata/slskd`                          | yes   | no       |
| slskd `/downloads`                   | `/mnt/storage/downloads/soulseek`             | yes   | yes      |
| slskd `/downloads/.incomplete`       | `/mnt/storage/downloads/soulseek/.incomplete` | no    | no       |
| slskd `/music`                       | `/mnt/storage/music`, read-only               | yes   | yes      |

Kopia backs up the complete downloads. A file from Soulseek can be hard to
find again, because it depends on one user that is online and shares it. The
rule that skips files that can be downloaded again is for files from a known
source, such as model weights. It is not for downloads from peers.

Kopia and SnapRAID do not back up the partial files in `.incomplete`. The
partial files change at each write, and the client deletes them or moves them
when the download is complete.

The partial files are on the array, not on the NVMe. A large torrent does not
use space on the NVMe. Each `.incomplete` directory is in the same bind mount
as its complete files. Thus the move of a complete file is a rename in the
mergerfs pool, not a copy.

Snapper takes snapshots of the data disks, and it cannot skip a directory.
A daily snapshot can hold a partial file. If you delete a partial download,
its space comes back when the snapshot expires, after 3 months at most.

A private torrent must stay in `downloads/torrents` to seed. Import the music
that you keep into `/mnt/storage/music`. Kopia stores a file only once, so a
copy in `downloads` and in `music` does not cost twice.

slskd shares `/mnt/storage/music`. Soulseek users ban a client that shares
nothing.

The pod maps `dmikalova` to UID 1000. qBittorrent and slskd run as UID 1000,
so the files on den are owned by `dmikalova`. gluetun runs as root in the pod,
which is a subordinate ID of `dmikalova` on den.

## Secrets

A quadlet cannot use `LoadCredential=`. `den-user-env.service` writes
`/run/den-env/<app>.env` from the secrets path. See the Secrets section of
[paseo.md](paseo.md) for how this works.

The values are in two files in the infrastructure repo. The infrastructure repo
copies them into the `den-host` secret:

| File                 | Key in the file           | Key in `den-host`                | Variable in the container                  |
| -------------------- | ------------------------- | -------------------------------- | ------------------------------------------ |
| `airvpn.sops.json`   | `WIREGUARD_PRIVATE_KEY`   | `AIRVPN_WIREGUARD_PRIVATE_KEY`   | gluetun `WIREGUARD_PRIVATE_KEY`            |
| `airvpn.sops.json`   | `WIREGUARD_PRESHARED_KEY` | `AIRVPN_WIREGUARD_PRESHARED_KEY` | gluetun `WIREGUARD_PRESHARED_KEY`          |
| `airvpn.sops.json`   | `WIREGUARD_ADDRESSES`     | `AIRVPN_WIREGUARD_ADDRESSES`     | gluetun `WIREGUARD_ADDRESSES`              |
| `airvpn.sops.json`   | `TORRENT_PORT`            | `AIRVPN_TORRENT_PORT`            | qBittorrent `QBT_TORRENTING_PORT`          |
| `airvpn.sops.json`   | `SOULSEEK_PORT`           | `AIRVPN_SOULSEEK_PORT`           | slskd `SLSKD_SLSK_LISTEN_PORT`             |
| (both ports)         |                           | `AIRVPN_FORWARDED_PORTS`         | gluetun `FIREWALL_VPN_INPUT_PORTS`         |
| `soulseek.sops.json` | `USERNAME`                | `SOULSEEK_USERNAME`              | slskd `SLSKD_SLSK_USERNAME`                |
| `soulseek.sops.json` | `PASSWORD`                | `SOULSEEK_PASSWORD`              | slskd `SLSKD_SLSK_PASSWORD`                |
| `airvpn.sops.json`   | `SPARE_PORTS`             | (none)                           | (none)                                     |

`SPARE_PORTS` holds the 3 forwarded ports that no app uses yet. When an app
uses one, move it to its own key, and add it to `AIRVPN_FORWARDED_PORTS`.

Each unit in the pod has a condition on its env file. Until `den-user-env`
writes the files, the pod does not start, and nothing fails.

## Server city

gluetun uses the AirVPN servers in Vancouver. In November 2026, a latency
test from den's network put Vancouver first, San Jose second and Los Angeles
third. AirVPN has 6 WireGuard servers in Vancouver.

`SERVER_CITIES` in `gluetun.container` sets the city. gluetun picks a server
at random from all the servers in the listed cities. It has no order of
preference. If you add San Jose, gluetun uses a San Jose server as often as a
Vancouver server. Thus the list has Vancouver only. If one Vancouver server
fails, gluetun moves to a different Vancouver server.

If all the Vancouver servers are down, change `SERVER_CITIES` to `San Jose`.
The WireGuard keys are the same for all servers, so no secret changes.

## Set up AirVPN

The account is the 3-year plan.

1. In the Client Area, open Devices. Add a device with the name `den`. Each
   device has its own keys. You can revoke the keys of den and keep the keys
   of your other devices.
2. In the Client Area, open the Config Generator.
   1. Select Linux and WireGuard.
   2. Select the device `den`.
   3. Select any server. The keys are the same for all servers. gluetun
      selects the server, and it has the public keys of the servers.
   4. Generate the configuration. Do not download it to a shared folder.
3. Copy these values from one `.conf` file:
   - `PrivateKey`, in the `[Interface]` section, is `WIREGUARD_PRIVATE_KEY`.
   - `PresharedKey`, in the `[Peer]` section, is `WIREGUARD_PRESHARED_KEY`.
   - `Address`, in the `[Interface]` section, is `WIREGUARD_ADDRESSES`. Use
     the IPv4 address only, for example `10.150.20.30/32`. The pod has no
     IPv6.

   The `Address` is also the IPv4 address on the device page. The public
   key on the device page is not necessary.
4. Delete the `.conf` files and the zip file.
5. In the Client Area, open Ports. Add 5 ports, the maximum for one account.
   For each port:
   - Forward TCP and UDP.
   - Leave the local port empty, so it is the same as the remote port.
   - Set the device to `den`.
6. Put the ports in `airvpn.sops.json`:
   - The first port is `TORRENT_PORT`.
   - The second port is `SOULSEEK_PORT`.
   - The other 3 ports are `SPARE_PORTS`, a comma-separated list. No app uses
     them yet.
7. Make a Soulseek user name and password. These are the credentials of the
   Soulseek network account, not of the slskd web UI. Soulseek makes the
   account at the first login.

## Add the secrets

1. In the infrastructure repo, make the two secret files:

   ```sh
   sops secrets/airvpn.sops.json
   sops secrets/soulseek.sops.json
   ```

2. Add the keys to the `den-host` secret in `den/secrets/main.tf`, and apply
   the infrastructure repo.
3. In this repo, add the credentials to `den-user-env.service`. Then bump the
   version of `den-user-env-units` in `mgmt/main.mcl`. `den-reconcile` writes
   the env files and starts the pod.
4. Run the checks:

   ```sh
   den check
   ```

   These checks must pass: `vpn tunnel healthy`, `qbittorrent answering`,
   `slskd answering`, `qbittorrent egress is the vpn` and
   `slskd egress is the vpn`.

## Set up qBittorrent

qBittorrent owns `qBittorrent.conf` and writes it again at each change, so mgmt
does not write it. Kopia backs up the file with the rest of
`/srv/appdata/qbittorrent`.

1. Get the temporary password. qBittorrent writes a new one to the log at each
   start until you set a password:

   ```sh
   ssh den podman logs qbittorrent 2>&1 | grep -i password
   ```

2. Log in at `https://qbittorrent.den.mklv.tech` as `admin`.
3. In Options, Web UI, set a password. Keep it in Bitwarden.
4. In Options, Advanced, set Network interface to `tun0`. The firewall in
   gluetun already blocks other routes. This is a second barrier.
5. In Options, Connection, make sure that the listening port is
   `TORRENT_PORT`. `QBT_TORRENTING_PORT` sets it at each start.
6. In Options, Downloads, make sure that the default save path is
   `/downloads`.
7. In Options, Downloads, select "Keep incomplete torrents in", and set it
   to `/downloads/.incomplete`. Kopia and SnapRAID skip this directory.

## Test the tunnel

Do these tests after the first start.

1. Make sure that the egress checks pass:

   ```sh
   den check
   ```

2. Make sure that the ports are open. In the Client Area, open Ports and
   test each port while qBittorrent and slskd run.
3. Download a large public torrent, for example an Ubuntu ISO, and record the
   speed.
4. Seed it, and record the upload speed. Compare it with the upload speed of
   den without the tunnel.

## Update an image

Change the `Image=` tag in the `.container` file. Then bump the `quadlets`
version in `mgmt/user/main.mcl` and in `mgmt/user/files/scripts/quadlets.sh`.
The quadlets script restarts the pod.

- Before you bump qBittorrent, check the client allowlists of RED and OPS.
  Keep the `lt1` build. libtorrent 2.0 has no disk cache in RAM.
- A gluetun bump also updates the AirVPN server list.
