# Backup

den keeps two kinds of copy, and they protect against different things.

| | snapraid | kopia |
| --- | --- | --- |
| Protects against | a data drive dying | everything else |
| Where the copy is | `/mnt/parity1`, same room | Backblaze B2, another state |
| History | none; parity tracks the current state | every snapshot, by retention policy |
| Recovers from a deleted file | only until the next sync | yes |
| Recovers from the building | no | yes |

snapraid is not a backup. It rebuilds a drive from the other drives plus
parity, and it is as current as the last sync, which means a file deleted
yesterday and synced last night is gone from parity too. kopia is the backup.

Neither is real until data has come back out of it. Both drills below are
designed to be run on a healthy host, on purpose, before the day they matter.

## What is backed up

| Path | kopia | Why |
| --- | --- | --- |
| `/srv/appdata` | yes | container state; small, irreplaceable |
| `/mnt/storage` | yes | the array |
| `/mnt/storage/recover` | no | **temporary**; a second copy of the old array during extraction |
| `/mnt/storage/timemachine` | no | already a backup, and it churns; see [shares.md](shares.md) |
| `/srv/models` | no | re-downloadable; paying to store it twice is waste |
| `/srv/dumps` | no | scratch |

The exclusions live in [mgmt/files/usr/local/bin/den-kopia](../mgmt/files/usr/local/bin/den-kopia).
Remove the `/mnt/storage/recover` rule once the extraction is finished.

## Schedule

| Unit | When |
| --- | --- |
| `kopia-backup.timer` | daily 22:00 |
| `snapraid-sync.timer` | daily 00:00 |
| `snapraid-scrub.timer` | Sunday 04:00 |
| `kopia-restore-drill.timer` | Sunday 06:00 |

The backup runs before the parity sync on purpose. The day's writes go offsite
first, so a slow local parity pass is never what stands between a new file and
its only other copy. Every timer carries a 30 minute random delay.

All four units inherit the global `OnFailure` drop-in, so a failure is a
Discord message. A unit that stops running entirely is not; that is what
`den check` is for.

## Credentials

The bucket and the key that reaches it are created by the `infrastructure`
repo and fetched by `den-secrets.service`. Nothing is typed.

Encryption is client side. Backblaze holds ciphertext, so `KOPIA_PASSWORD` is
the whole of what makes the backup readable. Losing it loses the backup as
surely as deleting the bucket. It is the one secret here that cannot be
reissued, so it is not generated: it is chosen and kept in
`secrets/den.sops.json` in the `infrastructure` repo, beside the LUKS
passphrase. A clone of that repo and the age key in Bitwarden recover it with
GCP gone.

## Restore a file

```bash
systemctl start kopia-restore-drill.service   # or, for a real restore:
sudo --preserve-env=KOPIA_CONFIG_PATH kopia snapshot list /srv/appdata
sudo kopia restore <snapshot-id>/path/to/file /var/tmp/recovered
```

`kopia` needs `KOPIA_CONFIG_PATH=/var/lib/kopia/repository.config` and the
repository password. The units supply both; a shell does not.

## Drill: kopia

```bash
sudo systemctl start kopia-restore-drill.service
journalctl -u kopia-restore-drill.service -n 50
```

This runs weekly on its own. It restores the newest `/srv/appdata` snapshot
into a scratch directory, lists what differs from the live tree, and then
verifies a sample of the repository, which reads real blobs back out of
Backblaze rather than out of the local cache.

Differences against the live tree are expected: the host keeps working between
the snapshot and the drill. An empty restore is a failure. So is a verify
error, which means stored data is damaged.

This is Gate 2. den does not hold data that exists nowhere else until this has
passed once.

## Drill: snapraid

```bash
sudo ./scripts/snapraid-drill.sh
```

The drill writes a 1 MiB random file to `/mnt/disk1`, syncs parity, and then
recovers that file twice: once after deleting it, and once after corrupting a
block in place while preserving the file's mtime, which is how snapraid is
made to notice. It compares sha256 both times and cleans up after itself.

It also asserts the content file is larger than 1 KiB. A 90 byte content file
means snapraid has indexed nothing and parity protects nothing, which is the
state this host was in before the drill existed.

Run it when the array is idle. It refuses to start if a sync or scrub is
running, and it runs a final sync on the way out so parity is left consistent.

The drill proves snapraid returns a byte it was given. It does not prove a
whole drive rebuild, which needs a spare drive and a day.
