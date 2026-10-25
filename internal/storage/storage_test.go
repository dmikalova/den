package storage

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// root is the repository root, from this package's directory.
const root = "../.."

func TestVolumeAllowed(t *testing.T) {
	cases := map[string]bool{
		"/srv/appdata/beszel":        true,
		"/srv/cache/models/ollama":   true,
		"/srv/db/immich":             true,
		"/srv/db-dumps/immich":       true,
		"/mnt/storage/photos":        true,
		"/srv/appdata":               false, // the area itself, not an app's directory
		"/srv/appdatax/beszel":       false,
		"/srv/models/ollama":         false,
		"/home/dmikalova/data":       false,
		"/srv/cache/../etc":          false,
		"ollama-data":                false, // a named volume
		"/run/user/1000/podman.sock": false,
	}
	for source, want := range cases {
		if got := VolumeAllowed(source); got != want {
			t.Errorf("VolumeAllowed(%q) = %v, want %v", source, got, want)
		}
	}
}

// Every bind mount in every quadlet is inside an area that takes container
// data. This is the rule docs/storage.md states, checked where it is broken.
func TestQuadletVolumes(t *testing.T) {
	dir := filepath.Join(root, "mgmt", "user", "files", "containers", "systemd")
	found := 0
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch filepath.Ext(path) {
		case ".volume":
			t.Errorf(
				"%s: named volumes are not allowed; bind mount a directory from docs/storage.md",
				path,
			)
			return nil
		case ".container", ".conf", ".pod", ".kube":
		default:
			return nil
		}
		for _, source := range volumeSources(t, path) {
			found++
			if !VolumeAllowed(source) {
				t.Errorf(
					"%s: Volume source %q is outside %v; see docs/storage.md",
					path,
					source,
					VolumeRoots(),
				)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatal("found no Volume= lines; the quadlet directory moved")
	}
}

// volumeSources returns the source half of each Volume= and Mount= line.
func volumeSources(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var sources []string
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if v, ok := strings.CutPrefix(line, "Volume="); ok {
			source, _, _ := strings.Cut(v, ":")
			sources = append(sources, source)
		}
		if v, ok := strings.CutPrefix(line, "Mount="); ok {
			for field := range strings.SplitSeq(v, ",") {
				key, value, _ := strings.Cut(field, "=")
				if key == "source" || key == "src" {
					sources = append(sources, value)
				}
			}
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	return sources
}

// Each area mgmt makes has a mount unit for the right subvolume, and the
// script that makes the subvolumes names it.
func TestMountUnits(t *testing.T) {
	units := filepath.Join(root, "mgmt", "files", "etc", "systemd", "system")
	script := readFile(t, filepath.Join(root, "mgmt", "files", "scripts", "btrfs-subvols.sh"))
	m := regexp.MustCompile(`for sv in ([^;]+);`).FindStringSubmatch(script)
	if m == nil {
		t.Fatal("btrfs-subvols.sh has no `for sv in ...;` list")
	}
	created := strings.Fields(m[1])
	for _, a := range Areas {
		if a.Subvolume == "" || a.Subvolume == "@" || a.Subvolume == "@swap" {
			continue
		}
		unit := readFile(t, filepath.Join(units, unitName(a.Path)))
		if !strings.Contains(unit, "\nWhere="+a.Path+"\n") {
			t.Errorf("%s: Where= is not %s", unitName(a.Path), a.Path)
		}
		if !strings.Contains(unit, "subvol="+a.Subvolume+"\n") {
			t.Errorf("%s: does not mount subvol=%s", unitName(a.Path), a.Subvolume)
		}
		if !slices.Contains(created, a.Subvolume) {
			t.Errorf("btrfs-subvols.sh does not create %s", a.Subvolume)
		}
	}

	store := readFile(t, filepath.Join(units, "home-dmikalova-.local-share-containers.mount"))
	if !strings.Contains(store, "\nWhat="+ContainerStore+"\n") {
		t.Errorf("the container store mount does not bind %s", ContainerStore)
	}
}

// Snapper keeps a timeline of exactly the areas marked Snapshots.
func TestSnapperConfigs(t *testing.T) {
	dir := filepath.Join(root, "mgmt", "files", "etc", "snapper", "configs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	configured := map[string]bool{}
	re := regexp.MustCompile(`(?m)^SUBVOLUME="([^"]+)"`)
	for _, e := range entries {
		m := re.FindStringSubmatch(readFile(t, filepath.Join(dir, e.Name())))
		if m == nil {
			t.Errorf("%s: no SUBVOLUME", e.Name())
			continue
		}
		configured[m[1]] = true
	}
	for _, a := range Areas {
		if a.Subvolume == "" {
			continue // the array's disks have their own configs
		}
		if a.Snapshots != configured[a.Path] {
			t.Errorf(
				"%s: Snapshots is %v, but a snapper config exists: %v",
				a.Path,
				a.Snapshots,
				configured[a.Path],
			)
		}
	}
}

// kopia backs up exactly the areas marked Backup.
func TestKopiaSources(t *testing.T) {
	script := readFile(t, filepath.Join(root, "mgmt", "files", "usr", "local", "bin", "den-kopia"))
	m := regexp.MustCompile(`(?s)\nsources=\(\n(.*?)\n\)`).FindStringSubmatch(script)
	if m == nil {
		t.Fatal("den-kopia has no sources=( ... ) list")
	}
	got := strings.Fields(m[1])
	var want []string
	for _, a := range Areas {
		if a.Backup {
			want = append(want, a.Path)
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("den-kopia sources are %v, want %v", got, want)
	}
}

// unitName is systemd-escape --path --suffix=mount.
func unitName(path string) string {
	var b strings.Builder
	for i, c := range []byte(strings.Trim(path, "/")) {
		switch {
		case c == '/':
			b.WriteByte('-')
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == ':',
			c == '.' && i > 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, `\x%02x`, c)
		}
	}
	return b.String() + ".mount"
}

func TestUnitName(t *testing.T) {
	cases := map[string]string{
		"/srv/appdata":  "srv-appdata.mount",
		"/srv/db-dumps": `srv-db\x2ddumps.mount`,
		"/home/dmikalova/.local/share/containers": "home-dmikalova-.local-share-containers.mount",
	}
	for path, want := range cases {
		if got := unitName(path); got != want {
			t.Errorf("unitName(%q) = %q, want %q", path, got, want)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
