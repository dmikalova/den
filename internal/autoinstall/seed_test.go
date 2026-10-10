package autoinstall

import (
	"bytes"
	"strings"
	"testing"
)

// ubuntuGrubCfg is the shape Ubuntu Server ships: a timeout, and one live
// kernel line per menu entry.
const ubuntuGrubCfg = `if loadfont /boot/grub/font.pf2 ; then
	set gfxmode=auto
	load_video
	insmod gfxterm
fi
set menu_color_normal=white/black
set timeout=30
menuentry "Try or Install Ubuntu Server" {
	set gfxpayload=keep
	linux	/casper/vmlinuz  ---
	initrd	/casper/initrd
}
menuentry "Ubuntu Server with the HWE kernel" {
	set gfxpayload=keep
	linux	/casper/hwe-vmlinuz  ---
	initrd	/casper/hwe-initrd
}
grub_platform
if [ "$grub_platform" = "efi" ]; then
menuentry 'Boot from next volume' {
	exit 1
}
fi`

func TestPatchGrub(t *testing.T) {
	out, err := PatchGrub(ubuntuGrubCfg, 1)
	if err != nil {
		t.Fatalf("PatchGrub: %v", err)
	}

	if got := strings.Count(out, KernelParams); got != 1 {
		t.Errorf("kernel params appear %d times, want 1", got)
	}
	if !strings.Contains(out, `linux	/casper/vmlinuz `+KernelParams+`  ---`) {
		t.Errorf("params not inserted before the trailing ---:\n%s", out)
	}
	if !strings.Contains(out, "set timeout=1") {
		t.Errorf("timeout not shortened:\n%s", out)
	}
	if strings.Contains(out, "set timeout=30") {
		t.Errorf("original timeout still present:\n%s", out)
	}
	if !strings.Contains(out, "exit 1") {
		t.Errorf("unrelated lines were lost:\n%s", out)
	}
}

func TestPatchGrubIsIdempotent(t *testing.T) {
	once, err := PatchGrub(ubuntuGrubCfg, 1)
	if err != nil {
		t.Fatalf("PatchGrub: %v", err)
	}
	twice, err := PatchGrub(once, 1)
	if err != nil {
		t.Fatalf("PatchGrub twice: %v", err)
	}
	if once != twice {
		t.Errorf("second patch changed the file:\n%s", twice)
	}
}

func TestPatchGrubFailsOnUnknownLayout(t *testing.T) {
	if _, err := PatchGrub("set timeout=30\nmenuentry \"nothing\" {}\n", 1); err == nil {
		t.Error("want an error when no live kernel line is present")
	}
}

func TestSeedValidate(t *testing.T) {
	good := Seed{
		Hostname:       "den",
		Username:       "dmikalova",
		PasswordHash:   "$6$rounds=4096$salt$hash",
		AuthorizedKeys: []string{"ssh-ed25519 AAAAC3Nz key@host"},
		LUKSPassphrase: "correct horse battery staple",
	}

	cases := []struct {
		name string
		seed Seed
		ok   bool
	}{
		{"complete", good, true},
		{"no hostname", func() Seed { s := good; s.Hostname = ""; return s }(), false},
		{"no username", func() Seed { s := good; s.Username = ""; return s }(), false},
		{
			"plaintext password",
			func() Seed { s := good; s.PasswordHash = "hunter2"; return s }(),
			false,
		},
		{"no keys", func() Seed { s := good; s.AuthorizedKeys = nil; return s }(), false},
		{
			"key is a path",
			func() Seed { s := good; s.AuthorizedKeys = []string{"~/.ssh/id_ed25519.pub"}; return s }(),
			false,
		},
		{"no luks passphrase", func() Seed { s := good; s.LUKSPassphrase = ""; return s }(), false},
		{
			"short luks passphrase",
			func() Seed { s := good; s.LUKSPassphrase = "short"; return s }(),
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.seed.Validate()
			if tc.ok && err != nil {
				t.Errorf("Validate: %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("want an error")
			}
		})
	}
}

func TestCheckFillerIsLast(t *testing.T) {
	filler := func(id string) map[string]any {
		return map[string]any{"type": "partition", "id": id, "size": -1}
	}
	fixed := func(id string) map[string]any {
		return map[string]any{"type": "partition", "id": id, "size": "1G"}
	}

	if err := checkFillerIsLast([]map[string]any{fixed("esp"), filler("root")}); err != nil {
		t.Errorf("filler last: %v", err)
	}
	if err := checkFillerIsLast([]map[string]any{filler("root"), fixed("esp")}); err == nil {
		t.Error("want an error when the filler is not last")
	}
}

func TestUserDataPinsOneDisk(t *testing.T) {
	s := Seed{
		Hostname:       "den",
		Username:       "dmikalova",
		PasswordHash:   "$6$rounds=4096$salt$hash",
		AuthorizedKeys: []string{"ssh-ed25519 AAAAC3Nz key@host"},
		LUKSPassphrase: "correct horse battery staple",
	}

	out, err := s.UserData()
	if err != nil {
		t.Fatalf("UserData: %v", err)
	}
	if !bytes.Contains(out, []byte("/dev/nvme0n1")) {
		t.Errorf("install is not pinned to the NVMe:\n%s", out)
	}
	if !bytes.Contains(out, []byte(s.AuthorizedKeys[0])) {
		t.Errorf("authorized key missing:\n%s", out)
	}
	if !bytes.Contains(out, []byte(s.LUKSPassphrase)) {
		t.Errorf("luks passphrase missing:\n%s", out)
	}
}

func TestCheckRootIsEncrypted(t *testing.T) {
	encrypted := []map[string]any{
		{"type": "dm_crypt", "id": "root-crypt", "volume": "root-part"},
		{"type": "format", "id": "root-fs", "volume": "root-crypt"},
		{"type": "mount", "id": "root-mount", "device": "root-fs", "path": "/"},
	}
	bare := []map[string]any{
		{"type": "format", "id": "root-fs", "volume": "root-part"},
		{"type": "mount", "id": "root-mount", "device": "root-fs", "path": "/"},
	}

	cases := []struct {
		name   string
		config []map[string]any
		ok     bool
	}{
		{"root on dm_crypt", encrypted, true},
		{"root on a bare partition", bare, false},
		{"no root mount", encrypted[:2], false},
		{"root mount names no format", []map[string]any{
			{"type": "mount", "device": "missing", "path": "/"},
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkRootIsEncrypted(tc.config)
			if tc.ok && err != nil {
				t.Errorf("checkRootIsEncrypted: %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("want an error")
			}
		})
	}
}

func TestCheckPinnedToOneDisk(t *testing.T) {
	cases := []struct {
		name   string
		config []map[string]any
		ok     bool
	}{
		{"one disk by path", []map[string]any{{"type": "disk", "path": "/dev/nvme0n1"}}, true},
		{"two disks", []map[string]any{
			{"type": "disk", "path": "/dev/nvme0n1"},
			{"type": "disk", "path": "/dev/sda"},
		}, false},
		{"no disk", []map[string]any{{"type": "partition"}}, false},
		{"disk without a path", []map[string]any{{"type": "disk"}}, false},
		{"disk selected by match", []map[string]any{
			{"type": "disk", "path": "/dev/nvme0n1", "match": map[string]any{"size": "largest"}},
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkPinnedToOneDisk(tc.config)
			if tc.ok && err != nil {
				t.Errorf("checkPinnedToOneDisk: %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("want an error")
			}
		})
	}
}
