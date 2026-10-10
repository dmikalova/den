package autoinstall

import (
	"crypto/rand"
	"fmt"

	"github.com/GehirnInc/crypt/sha512_crypt"
)

// saltAlphabet is the character set crypt(3) allows in a salt.
const saltAlphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// HashPassword turns the console password into the crypt(3) SHA-512 hash that
// Subiquity's identity.password requires. The salt is fresh on every build, so
// two images never carry the same hash.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password is empty")
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	for i, b := range salt {
		salt[i] = saltAlphabet[int(b)%len(saltAlphabet)]
	}

	hash, err := sha512_crypt.New().Generate(
		[]byte(password),
		append(
			append(
				make([]byte, 0, len(sha512_crypt.MagicPrefix)+len(salt)),
				sha512_crypt.MagicPrefix...),
			salt...),
	)
	if err != nil {
		return "", fmt.Errorf("hashing the password: %w", err)
	}
	return hash, nil
}
