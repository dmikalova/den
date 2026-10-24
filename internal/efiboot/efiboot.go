// Package efiboot reads the UEFI boot entries that `efibootmgr -v` lists, and
// picks the one that boots den from a USB stick.
package efiboot

import (
	"regexp"
	"strings"
)

// Entry is one BootXXXX variable.
type Entry struct {
	Num    string // four hex digits, as efibootmgr --bootnext takes them
	Label  string
	Path   string // the device path, empty if efibootmgr printed none
	Active bool
}

var line = regexp.MustCompile(`^Boot([0-9A-Fa-f]{4})(\*?)\s+(.*)$`)

// Parse reads efibootmgr output. Lines that are not boot entries, such as
// BootCurrent and BootOrder, are skipped.
func Parse(out string) []Entry {
	var entries []Entry
	for _, l := range strings.Split(out, "\n") {
		m := line.FindStringSubmatch(strings.TrimRight(l, "\r"))
		if m == nil {
			continue
		}
		label, path, _ := strings.Cut(m[3], "\t")
		entries = append(entries, Entry{
			Num:    strings.ToUpper(m[1]),
			Label:  strings.TrimSpace(label),
			Path:   strings.TrimSpace(path),
			Active: m[2] == "*",
		})
	}
	return entries
}

// USB returns the first active entry that boots from a USB device. The
// firmware makes these entries itself, and only for a stick that is present at
// power-on, so their numbers change from boot to boot.
func USB(entries []Entry) (Entry, bool) {
	for _, e := range entries {
		if e.Active && isUSB(e) {
			return e, true
		}
	}
	return Entry{}, false
}

func isUSB(e Entry) bool {
	if strings.Contains(e.Path, "/USB(") {
		return true
	}
	label := strings.ToLower(e.Label)
	return strings.Contains(label, "usb") || strings.Contains(label, "removable")
}
