package autoinstall

import (
	"testing"

	"github.com/GehirnInc/crypt/sha512_crypt"
)

// TestHashPasswordMatchesSpecVector pins the implementation to the published
// sha512-crypt test vector. A hash that glibc disagrees with locks the console.
func TestHashPasswordMatchesSpecVector(t *testing.T) {
	const (
		salt = "$6$saltstring"
		want = "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/" +
			"O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"
	)

	got, err := sha512_crypt.New().Generate([]byte("Hello world!"), []byte(salt))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != want {
		t.Errorf("sha512-crypt disagrees with the spec vector:\ngot  %s\nwant %s", got, want)
	}
}

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !cryptHash.MatchString(hash) {
		t.Errorf("%q is not a crypt(3) hash", hash)
	}
	if err := sha512_crypt.New().Verify(hash, []byte("correct horse battery staple")); err != nil {
		t.Errorf("the hash does not verify against its own password: %v", err)
	}
	if err := sha512_crypt.New().Verify(hash, []byte("wrong")); err == nil {
		t.Error("the hash verifies against the wrong password")
	}
}

func TestHashPasswordSaltsEachCall(t *testing.T) {
	first, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	second, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if first == second {
		t.Error("two builds produced the same hash, so the salt is not fresh")
	}
}

func TestHashPasswordRejectsEmpty(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Error("want an error for an empty password")
	}
}
