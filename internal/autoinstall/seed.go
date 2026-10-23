// Package autoinstall builds the Subiquity seed and the install image that
// carries it. The seed is a cloud-init user-data document; the image is the
// Ubuntu Server ISO remastered to boot straight into an unattended install.
package autoinstall

import (
	"bytes"
	"embed"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"go.yaml.in/yaml/v3"
)

//go:embed user-data.tmpl
var templates embed.FS

// Seed is everything the install needs that is not in the template: the
// identity and the keys that let the first SSH session in.
type Seed struct {
	Hostname       string
	Username       string
	PasswordHash   string
	AuthorizedKeys []string

	// LUKSPassphrase unlocks the root volume. curtin can only take it in
	// plaintext, so it reaches the installed system in clear.
	LUKSPassphrase string

	// HostKey is den's OpenSSH ed25519 host key. Every install gets the same
	// one, so known_hosts never changes across a reinstall.
	HostKey string
}

// cryptHash matches a crypt(3) hash, which is what identity.password must be.
// A plaintext password here would be written to the installed system verbatim.
var cryptHash = regexp.MustCompile(`^\$(6|y|7|2[aby])\$`)

// Validate reports what is missing or malformed before anything is written.
func (s Seed) Validate() error {
	switch {
	case s.Hostname == "":
		return fmt.Errorf("hostname is empty")
	case s.Username == "":
		return fmt.Errorf("username is empty")
	case s.PasswordHash == "":
		return fmt.Errorf("password hash is empty: generate one with `mkpasswd -m sha-512`")
	case !cryptHash.MatchString(s.PasswordHash):
		return fmt.Errorf(
			"password hash is not a crypt(3) hash: it must start with $6$, $y$ or similar",
		)
	case len(s.AuthorizedKeys) == 0:
		return fmt.Errorf("no authorized keys: without one you cannot reach the installed host")
	case len(s.LUKSPassphrase) < 16:
		return fmt.Errorf("luks passphrase is shorter than 16 characters")
	case !strings.HasPrefix(s.HostKey, "-----BEGIN OPENSSH PRIVATE KEY-----"):
		return fmt.Errorf("host key is not an OpenSSH private key")
	}
	for _, k := range s.AuthorizedKeys {
		if !strings.HasPrefix(k, "ssh-") && !strings.HasPrefix(k, "ecdsa-") {
			return fmt.Errorf("authorized key is not an OpenSSH public key: %.20s", k)
		}
	}
	return nil
}

// UserData renders the seed. It parses the result back and checks the
// autoinstall version, so a broken template fails here rather than on the
// machine three days later.
func (s Seed) UserData() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}

	funcs := template.FuncMap{
		"quote": func(v string) string {
			out, err := yaml.Marshal(v)
			if err != nil {
				return ""
			}
			return string(bytes.TrimSpace(out))
		},
		"seedKey": func() string { return "/cdrom/" + SeedDir + "/" + KeyFile },
		"seedHostKey": func() string {
			return "/cdrom/" + SeedDir + "/" + HostKeyFile
		},
		"seedInitrd": func() string { return "/cdrom/" + SeedDir + "/" + InitrdDir },
	}
	t, err := template.New("user-data.tmpl").Funcs(funcs).ParseFS(templates, "user-data.tmpl")
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, s); err != nil {
		return nil, err
	}
	if err := checkUserData(buf.Bytes()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// checkUserData catches the failures that otherwise surface as a stalled
// installer: invalid YAML, the wrong schema version, or a storage stanza that
// no longer pins the install to one disk.
func checkUserData(b []byte) error {
	if !bytes.HasPrefix(b, []byte("#cloud-config\n")) {
		return fmt.Errorf("user-data must start with the #cloud-config line")
	}

	var doc struct {
		Autoinstall struct {
			Version int `yaml:"version"`
			Storage struct {
				Config []map[string]any `yaml:"config"`
				Layout map[string]any   `yaml:"layout"`
			} `yaml:"storage"`
		} `yaml:"autoinstall"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("user-data is not valid YAML: %w", err)
	}
	if doc.Autoinstall.Version != 1 {
		return fmt.Errorf("autoinstall.version is %d, want 1", doc.Autoinstall.Version)
	}
	if err := checkPinnedToOneDisk(doc.Autoinstall.Storage.Config); err != nil {
		return err
	}
	if err := checkFillerIsLast(doc.Autoinstall.Storage.Config); err != nil {
		return err
	}
	return checkRootIsEncrypted(doc.Autoinstall.Storage.Config)
}

// checkFillerIsLast enforces Subiquity's rule for a negative partition size:
// it means "the rest of the disk" and only the last partition may use it.
// Anywhere else Subiquity raises NotFinalPartitionError mid-install.
func checkFillerIsLast(config []map[string]any) error {
	last := ""
	for _, e := range config {
		if asString(e["type"]) == "partition" {
			last = asString(e["id"])
		}
	}
	for _, e := range config {
		if asString(e["type"]) != "partition" {
			continue
		}
		size, ok := e["size"].(int)
		if ok && size < 0 && asString(e["id"]) != last {
			return fmt.Errorf(
				"partition %q fills the disk but is not the last partition",
				asString(e["id"]),
			)
		}
	}
	return nil
}

// checkRootIsEncrypted walks mount -> format -> volume and refuses a root
// filesystem that does not sit on a dm_crypt device. Encryption at rest is
// install-time only, so losing it here means a reinstall.
func checkRootIsEncrypted(config []map[string]any) error {
	byID := make(map[string]map[string]any, len(config))
	for _, item := range config {
		if id, ok := item["id"].(string); ok {
			byID[id] = item
		}
	}

	for _, item := range config {
		if item["type"] != "mount" || item["path"] != "/" {
			continue
		}
		format, ok := byID[asString(item["device"])]
		if !ok {
			return fmt.Errorf("the / mount names no format")
		}
		volume, ok := byID[asString(format["volume"])]
		if !ok || volume["type"] != "dm_crypt" {
			return fmt.Errorf("the root filesystem is not on a dm_crypt device")
		}
		return nil
	}
	return fmt.Errorf("storage declares no / mount")
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// checkPinnedToOneDisk refuses a storage stanza that could select a drive by
// size or by wildcard. The install must name exactly one disk by path, so the
// hard drives are never candidates.
func checkPinnedToOneDisk(config []map[string]any) error {
	paths := make([]string, 0, 1)
	for _, item := range config {
		if item["type"] != "disk" {
			continue
		}
		path, ok := item["path"].(string)
		if !ok || path == "" {
			return fmt.Errorf(
				"storage declares a disk without a path: every disk must be named by path",
			)
		}
		if _, matched := item["match"]; matched {
			return fmt.Errorf("storage disk %s uses match: name the disk by path alone", path)
		}
		paths = append(paths, path)
	}
	if len(paths) != 1 {
		return fmt.Errorf("storage declares %d disks, want exactly 1: %s",
			len(paths), strings.Join(paths, ", "))
	}
	return nil
}

// MetaData is the companion document cloud-init requires alongside user-data.
// The instance id marks the install as a first boot.
func MetaData(hostname string) []byte {
	return fmt.Appendf(nil, "instance-id: %s-autoinstall\nlocal-hostname: %s\n", hostname, hostname)
}
