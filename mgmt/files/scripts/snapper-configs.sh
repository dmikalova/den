set -euo pipefail

# create-config is what registers the config in SNAPPER_CONFIGS and creates the
# .snapshots subvolume, so it has to run before any set-config. It errors if the
# config already exists, hence the guard.
for pair in root:/ home:/home appdata:/srv/appdata; do
	cfg=${pair%%:*}
	path=${pair#*:}
	[ -e "/etc/snapper/configs/$cfg" ] || snapper -c "$cfg" create-config "$path"
done

# Only the subvolume itself is captured: a nested subvolume reads as an empty
# directory inside a snapshot. That is why / can be rolled back without taking
# /home, /srv/appdata or the container store with it.

# apt brackets every package transaction with a pre/post pair through
# /etc/apt/apt.conf.d/80snapper, which is the pair a bad upgrade needs. The
# timeline is here as well because mgmt edits /etc and nothing brackets that;
# hourly 0 means the hourly run still fires but cleanup keeps only dailies.
snapper -c root set-config \
	TIMELINE_CREATE=yes \
	TIMELINE_CLEANUP=yes \
	TIMELINE_LIMIT_HOURLY=0 \
	TIMELINE_LIMIT_DAILY=7 \
	TIMELINE_LIMIT_WEEKLY=4 \
	TIMELINE_LIMIT_MONTHLY=0 \
	TIMELINE_LIMIT_YEARLY=0 \
	NUMBER_CLEANUP=yes \
	NUMBER_LIMIT=10 \
	NUMBER_LIMIT_IMPORTANT=10

# These two change continuously and no package manager brackets them, so the
# timeline is the only trigger they get.
#
# A snapshot of a running service's data is crash consistent, not a backup: it
# is what the database would recover from after a power cut. It is a fast undo
# for a bad config or an accidental delete, and nothing more. Off-host backup
# stays a separate job.
for cfg in home appdata; do
	snapper -c "$cfg" set-config \
		TIMELINE_CREATE=yes \
		TIMELINE_CLEANUP=yes \
		TIMELINE_LIMIT_HOURLY=12 \
		TIMELINE_LIMIT_DAILY=7 \
		TIMELINE_LIMIT_WEEKLY=4 \
		TIMELINE_LIMIT_MONTHLY=0 \
		TIMELINE_LIMIT_YEARLY=0 \
		NUMBER_CLEANUP=no \
		ALLOW_USERS=dmikalova \
		SYNC_ACL=yes
done

touch /var/lib/den/snapper-configs-v1
