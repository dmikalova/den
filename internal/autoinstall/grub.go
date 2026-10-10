package autoinstall

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SeedDir is where the seed lands on the image. The installer mounts the
// image at /cdrom, so cloud-init reads it from /cdrom/nocloud/.
const SeedDir = "nocloud"

// KernelParams make the live installer run unattended. Without `autoinstall`
// Subiquity stops and asks for confirmation before it touches a disk, which
// defeats the point. The quotes matter: GRUB reads an unquoted semicolon as
// the end of a statement.
const KernelParams = `autoinstall "ds=nocloud;s=/cdrom/` + SeedDir + `/"`

// casperLinux matches the line that loads the live kernel. Ubuntu writes one
// per menu entry, for BIOS and for EFI.
var casperLinux = regexp.MustCompile(`^(\s*linux\s+/casper/vmlinuz)(\s.*)?$`)

// grubTimeout matches the menu timeout GRUB waits before booting the default.
var grubTimeout = regexp.MustCompile(`^(\s*set\s+timeout=)\d+\s*$`)

// PatchGrub adds the autoinstall parameters to every live kernel line and
// shortens the menu timeout. It is idempotent, and it fails rather than
// returning an unchanged file: a silently unpatched image boots to the
// interactive installer, which you only find out about at the machine.
func PatchGrub(cfg string, timeout int) (string, error) {
	lines := strings.Split(cfg, "\n")
	patched := 0

	for i, line := range lines {
		switch {
		case casperLinux.MatchString(line):
			if strings.Contains(line, "autoinstall") {
				patched++
				continue
			}
			m := casperLinux.FindStringSubmatch(line)
			lines[i] = m[1] + " " + KernelParams + m[2]
			patched++
		case grubTimeout.MatchString(line):
			lines[i] = grubTimeout.ReplaceAllString(line, "${1}"+strconv.Itoa(timeout))
		}
	}

	if patched == 0 {
		return "", fmt.Errorf(
			"no `linux /casper/vmlinuz` line in grub.cfg: the ISO layout changed, so the patch rules need updating",
		)
	}
	return strings.Join(lines, "\n"), nil
}
