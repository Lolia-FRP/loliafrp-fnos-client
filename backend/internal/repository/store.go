package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/LoliaFRP/loliafrp-fnos/backend/internal/secrets"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Credentials struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	Scope        string
	ExpiresAt    int64
}

type Preference struct {
	TunnelID  string `json:"tunnelId"`
	AutoStart bool   `json:"autoStart"`
}

type Store struct {
	db    *sql.DB
	vault *secrets.Vault
}

func Open(ctx context.Context, path string, vault *secrets.Vault) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// Keep connection-local SQLite pragmas effective for every operation and
	// serialize short writes from different NAS accounts.
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		`PRAGMA journal_mode=WAL`, `PRAGMA foreign_keys=ON`, `PRAGMA busy_timeout=5000`,
		`CREATE TABLE IF NOT EXISTS accounts (owner_uid TEXT PRIMARY KEY, username TEXT NOT NULL, access_token TEXT NOT NULL, refresh_token TEXT NOT NULL, token_type TEXT NOT NULL, scope TEXT NOT NULL, expires_at INTEGER NOT NULL, state TEXT NOT NULL, updated_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS tunnel_preferences (owner_uid TEXT NOT NULL, tunnel_id TEXT NOT NULL, auto_start INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(owner_uid,tunnel_id))`,
		`CREATE TABLE IF NOT EXISTS app_preferences (owner_uid TEXT PRIMARY KEY, boot_auto_start INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL)`,
	} {
		if _, err = db.ExecContext(ctx, q); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db, vault: vault}, nil
}

func (s *Store) Close() error      { return s.db.Close() }
func aad(uid, field string) []byte { return []byte("loliafrp:v1:" + uid + ":" + field) }

func (s *Store) SaveCredentials(ctx context.Context, uid, username string, credentials Credentials) error {
	access, err := s.vault.Seal(credentials.AccessToken, aad(uid, "access"))
	if err != nil {
		return err
	}
	refresh, err := s.vault.Seal(credentials.RefreshToken, aad(uid, "refresh"))
	if err != nil {
		return err
	}
	tokenType, err := s.vault.Seal(credentials.TokenType, aad(uid, "token_type"))
	if err != nil {
		return err
	}
	scope, err := s.vault.Seal(credentials.Scope, aad(uid, "scope"))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO accounts(owner_uid,username,access_token,refresh_token,token_type,scope,expires_at,state,updated_at) VALUES(?,?,?,?,?,?,?,'authenticated',?) ON CONFLICT(owner_uid) DO UPDATE SET username=CASE WHEN excluded.username='' THEN accounts.username ELSE excluded.username END,access_token=excluded.access_token,refresh_token=excluded.refresh_token,token_type=excluded.token_type,scope=excluded.scope,expires_at=excluded.expires_at,state='authenticated',updated_at=excluded.updated_at`, uid, username, access, refresh, tokenType, scope, credentials.ExpiresAt, time.Now().Unix())
	return err
}

func (s *Store) Credentials(ctx context.Context, uid string) (Credentials, error) {
	var credentials Credentials
	var access, refresh, tokenType, scope string
	var expiresAt int64
	err := s.db.QueryRowContext(ctx, `SELECT access_token,refresh_token,token_type,scope,expires_at FROM accounts WHERE owner_uid=? AND state='authenticated'`, uid).Scan(&access, &refresh, &tokenType, &scope, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return credentials, ErrNotFound
	}
	if err != nil {
		return credentials, err
	}
	open := func(sealed, field string) (string, error) {
		value, err := s.vault.Open(sealed, aad(uid, field))
		if err != nil {
			_, _ = s.db.ExecContext(ctx, `DELETE FROM accounts WHERE owner_uid=?`, uid)
			return "", ErrNotFound
		}
		return value, nil
	}
	if credentials.AccessToken, err = open(access, "access"); err != nil {
		return credentials, err
	}
	if credentials.RefreshToken, err = open(refresh, "refresh"); err != nil {
		return credentials, err
	}
	if credentials.TokenType, err = open(tokenType, "token_type"); err != nil {
		return credentials, err
	}
	if credentials.Scope, err = open(scope, "scope"); err != nil {
		return credentials, err
	}
	credentials.ExpiresAt = expiresAt
	return credentials, nil
}

func (s *Store) DeleteAccount(ctx context.Context, uid string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM tunnel_preferences WHERE owner_uid=?`, uid); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM app_preferences WHERE owner_uid=?`, uid); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM accounts WHERE owner_uid=?`, uid); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetAutoStart(ctx context.Context, uid, tunnel string, value bool) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO tunnel_preferences(owner_uid,tunnel_id,auto_start) VALUES(?,?,?) ON CONFLICT(owner_uid,tunnel_id) DO UPDATE SET auto_start=excluded.auto_start`, uid, tunnel, value)
	return err
}

func (s *Store) Preferences(ctx context.Context, uid string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tunnel_id,auto_start FROM tunnel_preferences WHERE owner_uid=?`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		var value bool
		if err = rows.Scan(&id, &value); err != nil {
			return nil, err
		}
		out[id] = value
	}
	return out, rows.Err()
}

func (s *Store) AutoStartAccounts(ctx context.Context) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT owner_uid,tunnel_id FROM tunnel_preferences WHERE auto_start=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var uid, tunnel string
		if err = rows.Scan(&uid, &tunnel); err != nil {
			return nil, err
		}
		out[uid] = append(out[uid], tunnel)
	}
	return out, rows.Err()
}

func (s *Store) SetBootAutoStart(ctx context.Context, uid string, value bool) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO app_preferences(owner_uid,boot_auto_start,updated_at) VALUES(?,?,?) ON CONFLICT(owner_uid) DO UPDATE SET boot_auto_start=excluded.boot_auto_start,updated_at=excluded.updated_at`, uid, value, time.Now().Unix())
	return err
}

func (s *Store) BootAutoStart(ctx context.Context, uid string) (bool, error) {
	var value bool
	err := s.db.QueryRowContext(ctx, `SELECT boot_auto_start FROM app_preferences WHERE owner_uid=?`, uid).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return value, err
}

func (s *Store) BootAutoStartUIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT owner_uid FROM app_preferences WHERE boot_auto_start=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var uid string
		if err = rows.Scan(&uid); err != nil {
			return nil, err
		}
		out = append(out, uid)
	}
	return out, rows.Err()
}

func (s *Store) String() string { return fmt.Sprintf("Store(%p)", s.db) }
