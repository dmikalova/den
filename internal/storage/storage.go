// Package storage is den's storage layout: one btrfs subvolume for each storage
// policy, never one for each app. An app gets a directory in each area it
// needs, such as /srv/appdata/beszel and /srv/cache/models/ollama.
//
// The table here is the one source the rest of the repo is tested against:
// the mount units, the snapper configs, the kopia sources and every quadlet
// Volume=. docs/storage.md explains the policies.
package storage

import "strings"

// Area is one storage policy and where it is mounted.
type Area struct {
	// Path is where the area is mounted.
	Path string
	// Subvolume is the btrfs subvolume on the NVMe. Empty for the array,
	// which is its own set of disks.
	Subvolume string
	// Snapshots says snapper keeps a timeline of it.
	Snapshots bool
	// Backup says kopia sends it offsite.
	Backup bool
	// Volumes says a container may keep data under it.
	Volumes bool
}

// Areas is den's layout. @ and @swap are made by the autoinstall; the rest
// are made by mgmt.
var Areas = []Area{
	{Path: "/", Subvolume: "@", Snapshots: true},
	{Path: "/swap", Subvolume: "@swap"},
	{Path: "/home", Subvolume: "@home", Snapshots: true},
	{Path: "/srv/appdata", Subvolume: "@appdata", Snapshots: true, Backup: true, Volumes: true},
	{Path: "/srv/db", Subvolume: "@db", Snapshots: true, Volumes: true},
	{Path: "/srv/db-dumps", Subvolume: "@db-dumps", Backup: true, Volumes: true},
	{Path: "/srv/cache", Subvolume: "@cache", Volumes: true},
	{Path: "/mnt/storage", Backup: true, Volumes: true},
}

// ContainerStore is where rootless podman keeps images and layers. It is a
// bind mount of this directory, so podman stays on its default path.
const ContainerStore = "/srv/cache/containers"

// VolumeRoots returns the paths a container's bind mount source may be under.
func VolumeRoots() []string {
	var roots []string
	for _, a := range Areas {
		if a.Volumes {
			roots = append(roots, a.Path)
		}
	}
	return roots
}

// VolumeAllowed reports whether a bind mount source is inside an area that
// takes container data. A named volume is never allowed: it puts data in the
// image store, which is on @cache and is not backed up.
func VolumeAllowed(source string) bool {
	if !strings.HasPrefix(source, "/") || strings.Contains(source, "..") {
		return false
	}
	for _, root := range VolumeRoots() {
		if strings.HasPrefix(source, root+"/") {
			return true
		}
	}
	return false
}
