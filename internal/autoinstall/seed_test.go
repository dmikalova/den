package autoinstall

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
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

// testHostKey is shaped like an OpenSSH private key and is not one. Split so a
// secret scanner does not read the test as a leaked key.
const testHostKey = "-----BEGIN OPENSSH " + "PRIVATE KEY-----\nnot-a-key\n-----END OPENSSH " + "PRIVATE KEY-----\n"

func TestSeedValidate(t *testing.T) {
	good := Seed{
		Hostname:       "den",
		Username:       "dmikalova",
		PasswordHash:   "$6$rounds=4096$salt$hash",
		AuthorizedKeys: []string{"ssh-ed25519 AAAAC3Nz key@host"},
		LUKSPassphrase: "correct horse battery staple",
		HostKey:        testHostKey,
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
		{"no host key", func() Seed { s := good; s.HostKey = ""; return s }(), false},
		{
			"public host key",
			func() Seed { s := good; s.HostKey = "ssh-ed25519 AAAAC3Nz root@den"; return s }(),
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
		HostKey:        testHostKey,
	}

	out, err := s.UserData()
	if err != nil {
		t.Fatalf("UserData: %v", err)
	}
	if bytes.Contains(out, []byte("PRIVATE KEY")) {
		t.Errorf("the host key leaked into user-data, which the installer logs:\n%s", out)
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

func TestUserDataInstallsInitrdUnlock(t *testing.T) {
	s := Seed{
		Hostname:       "den",
		Username:       "dmikalova",
		PasswordHash:   "$6$rounds=4096$salt$hash",
		AuthorizedKeys: []string{"ssh-ed25519 AAAAC3Nz key@host"},
		LUKSPassphrase: "correct horse battery staple",
		HostKey:        testHostKey,
	}

	out, err := s.UserData()
	if err != nil {
		t.Fatalf("UserData: %v", err)
	}
	text := string(out)
	install := strings.Index(text, "bash /tmp/den-initrd/install.sh")
	hostKey := strings.Index(text, "/target/etc/ssh/ssh_host_ed25519_key")
	subvols := strings.Index(text, "bash /tmp/den-subvols")
	switch {
	case install < 0:
		t.Fatalf("user-data does not run the initrd install:\n%s", out)
	case hostKey < 0 || install < hostKey:
		t.Error("the initrd install runs before the host key is in place")
	case subvols < install:
		t.Error("the initrd install runs after the subvolume rebuild, which must be last")
	}
}

func TestWaitDriverDropInMatchesMgmt(t *testing.T) {
	const path = "/etc/systemd/system/nvidia-persistenced.service.d/10-den-wait-driver.conf"
	want, err := os.ReadFile(filepath.Join("..", "..", "mgmt", "files", path))
	if err != nil {
		t.Fatal(err)
	}
	s := Seed{
		Hostname:       "den",
		Username:       "dmikalova",
		PasswordHash:   "$6$rounds=4096$salt$hash",
		AuthorizedKeys: []string{"ssh-ed25519 AAAAC3Nz key@host"},
		LUKSPassphrase: "correct horse battery staple",
		HostKey:        testHostKey,
	}
	out, err := s.UserData()
	if err != nil {
		t.Fatalf("UserData: %v", err)
	}

	var doc struct {
		Autoinstall struct {
			LateCommands []string `yaml:"late-commands"`
		} `yaml:"autoinstall"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range doc.Autoinstall.LateCommands {
		_, body, ok := strings.Cut(cmd, path+" <<'UNIT'\n")
		if !ok {
			continue
		}
		got, _, _ := strings.Cut(body, "UNIT\n")
		if got != string(want) {
			t.Errorf(
				"late-command drop-in differs from mgmt's copy:\ngot:\n%s\nwant:\n%s",
				got,
				want,
			)
		}
		return
	}
	t.Fatal("no late-command writes the nvidia-persistenced drop-in")
}

func TestWriteInitrd(t *testing.T) {
	dir := t.TempDir()
	keys := []string{"ssh-ed25519 AAAA one", "ssh-ed25519 BBBB two"}
	if err := writeInitrd(dir, keys); err != nil {
		t.Fatalf("writeInitrd: %v", err)
	}

	for _, f := range []string{
		"install.sh",
		"45den-net/module-setup.sh",
		"45den-net/10-den.network",
		"45den-net/mdns.conf",
		"46sshd/module-setup.sh",
		"46sshd/sshd.service",
		"46sshd/sshd_config",
		"46sshd/motd",
		"46sshd/profile",
	} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	got, err := os.ReadFile(filepath.Join(dir, "authorized_keys"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "ssh-ed25519 AAAA one\nssh-ed25519 BBBB two\n"; string(got) != want {
		t.Errorf("authorized_keys = %q, want %q", got, want)
	}
}
