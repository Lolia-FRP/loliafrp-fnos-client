package repository

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/LoliaFRP/loliafrp-fnos/backend/internal/secrets"
)

func TestCredentialsRejectsLegacyRowsAndClears(t *testing.T) {
	ctx := context.Background()
	vault, err := secrets.Open(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, filepath.Join(t.TempDir(), "database.sqlite"), vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// 模拟迁移前旧格式行：refresh_token 列用旧 AAD "session" 加密，token_type/scope 为明文。
	access, err := vault.Seal("access-1", aad("1000", "access"))
	if err != nil {
		t.Fatal(err)
	}
	session, err := vault.Seal("session-1", aad("1000", "session"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO accounts(owner_uid,username,access_token,refresh_token,token_type,scope,expires_at,state,updated_at) VALUES('1000','legacy',?,?,?,?,0,'authenticated',0)`, access, session, "Bearer", "")
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := store.Credentials(ctx, "1000")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy row must surface as not found, got %#v, %v", credentials, err)
	}
	var count int
	if err = store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE owner_uid='1000'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("legacy row must be cleared, %d rows remain", count)
	}
}

func TestCredentialsRoundTrip(t *testing.T) {
	ctx := context.Background()
	vault, err := secrets.Open(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, filepath.Join(t.TempDir(), "database.sqlite"), vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	want := Credentials{AccessToken: "access-1", RefreshToken: "refresh-1", TokenType: "Bearer", Scope: "profile tunnel:read", ExpiresAt: 1234567890}
	if err = store.SaveCredentials(ctx, "1000", "", want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Credentials(ctx, "1000")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: %#v != %#v", got, want)
	}
}

func TestBootAutoStartRoundTripAndClear(t *testing.T) {
	ctx := context.Background()
	vault, err := secrets.Open(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, filepath.Join(t.TempDir(), "database.sqlite"), vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	enabled, err := store.BootAutoStart(ctx, "1000")
	if err != nil || enabled {
		t.Fatalf("default must be off, got %v %v", enabled, err)
	}
	if err = store.SetBootAutoStart(ctx, "1000", true); err != nil {
		t.Fatal(err)
	}
	enabled, err = store.BootAutoStart(ctx, "1000")
	if err != nil || !enabled {
		t.Fatalf("expected on, got %v %v", enabled, err)
	}
	uids, err := store.BootAutoStartUIDs(ctx)
	if err != nil || len(uids) != 1 || uids[0] != "1000" {
		t.Fatalf("expected [1000], got %#v %v", uids, err)
	}
	if err = store.SaveCredentials(ctx, "1000", "", Credentials{AccessToken: "a", RefreshToken: "r", TokenType: "Bearer", Scope: "profile", ExpiresAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteAccount(ctx, "1000"); err != nil {
		t.Fatal(err)
	}
	enabled, err = store.BootAutoStart(ctx, "1000")
	if err != nil || enabled {
		t.Fatalf("preference must be cleared, got %v %v", enabled, err)
	}
}
