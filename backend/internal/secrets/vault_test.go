package secrets

import (
	"path/filepath"
	"testing"
)

func TestVaultRoundTripAndAADIsolation(t *testing.T) {
	vault, err := Open(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := vault.Seal("refresh-token", []byte("uid:1000"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := vault.Open(sealed, []byte("uid:1000"))
	if err != nil || plain != "refresh-token" {
		t.Fatalf("round trip failed: %q %v", plain, err)
	}
	if _, err := vault.Open(sealed, []byte("uid:1001")); err == nil {
		t.Fatal("different user AAD must not decrypt")
	}
}
