# Storage

den has one btrfs subvolume for each storage policy. A policy is a set of
rules: snapshots or no snapshots, backup or no backup. An app gets a
directory in each policy that it needs. Do not add a subvolume for an app.

[internal/storage](../internal/storage/storage.go) holds this table in code.
The tests and `den check` read it from there. If you change the layout,
change that table, this page and the mgmt files together.

## The areas

| Path                    | Subvolume   | Snapper | Kopia | Holds                                 |
| ----------------------- | ----------- | ------- | ----- | ------------------------------------- |
| `/`                     | `@`         | yes     | no    | the OS                                |
| `/swap`                 | `@swap`     | no      | no    | the swapfile                          |
| `/home`                 | `@home`     | yes     | no    | the home directory                    |
| `/srv/appdata/<app>`    | `@appdata`  | yes     | yes   | configuration and small state         |
| `/srv/db/<app>`         | `@db`       | yes     | no    | live database files                   |
| `/srv/db-dumps/<app>`   | `@db-dumps` | no      | yes   | consistent database dumps             |
| `/srv/cache/<app>`      | `@cache`    | no      | no    | data that den can make again          |
| `/mnt/storage/<thing>`  | data disks  | yes     | yes   | the large libraries, on the array     |

The autoinstall makes `@` and `@swap`. mgmt makes the other subvolumes. The
array has its own disks, its own snapper configuration for each disk, and
SnapRAID parity. See [backup.md](backup.md).

## Which area to use

Ask these questions in this order. Use the first area that matches.

1. Is it a file that the app can download or build again? Use `/srv/cache`.
   Examples: model weights, thumbnails, transcoded video, worktrees,
   research caches.
2. Is it a live database? Use `/srv/db` for the database files. Make the app
   write a dump to `/srv/db-dumps` before `kopia-backup.timer` starts.
3. Is it a large library that a person made, such as photos? Use
   `/mnt/storage`.
4. Use `/srv/appdata` for everything else.

An app can use more than one area. For example, Immich uses all of them:

| Path                         | Holds                       |
| ---------------------------- | --------------------------- |
| `/srv/appdata/immich`        | the configuration           |
| `/srv/db/immich`             | the Postgres data           |
| `/srv/db-dumps/immich`       | the nightly `pg_dump`       |
| `/srv/cache/immich`          | thumbnails and transcodes   |
| `/srv/cache/models/immich`   | the machine learning models |
| `/mnt/storage/photos`        | the original photos         |

## Why a database has two areas

Kopia copies files while the app runs. A copy of running database files is
only crash consistent, so a restore of it can fail. A dump is consistent
because the database makes it on purpose. Thus kopia backs up
`/srv/db-dumps` and does not back up `/srv/db`.

Snapper takes snapshots of `/srv/db`. A snapshot is also only crash
consistent, but it is local and fast. Use it to roll back after a bad
upgrade. Use the dump to restore after a loss.

## Containers

Each quadlet `Volume=` source must be a directory in one of the areas that
take container data: `/srv/appdata`, `/srv/db`, `/srv/db-dumps`, `/srv/cache`
or `/mnt/storage`. Named podman volumes are not permitted, because they put
data in the image store, which has no backup.

`go test ./internal/storage` makes sure of this rule for each quadlet in
`mgmt/user`. `mage ci:check` runs it.

The podman image store is `/srv/cache/containers`. A bind mount puts it at
`~/.local/share/containers`, so podman uses its default path. Model weights
are in `/srv/cache/models/<app>`.

## Mount unit names

systemd names a mount unit after its path. It escapes each `-` in the path as
`\x2d`. Thus the unit for `/srv/db-dumps` is `srv-db\x2ddumps.mount`. A unit
named `srv-db-dumps.mount` is for `/srv/db/dumps`, and systemd refuses it.
Use `systemd-escape --path --suffix=mount <path>` to get the name.

## Checks

- `mage ci:check` fails if a quadlet, mount unit, snapper configuration or the
  kopia source list does not agree with the table.
- `den check` makes sure that each area is mounted from its subvolume, and
  that the image store is a bind mount of `/srv/cache/containers`.

## History

Before this layout, den had `@dumps` at `/srv/dumps`, `@models` at
`/srv/models` and `@containers` at `~/.local/share/containers`. The
`btrfs-mounts-v3` step in mgmt moves `@models` and `@containers` into
`@cache` and deletes the empty `@dumps`. A move in one btrfs filesystem is a
rename, so nothing is copied or downloaded again. The moved subvolumes stay
as subvolumes in `@cache`. This has no effect, because `@cache` has no
snapshots and no backup.
