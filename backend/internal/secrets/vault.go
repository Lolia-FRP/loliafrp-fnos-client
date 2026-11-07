package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
)

type Vault struct{ aead cipher.AEAD }

func Open(path string) (*Vault, error) {
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		if err = os.WriteFile(path, key, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid master key length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead}, nil
}

func (v *Vault) Seal(plain string, aad []byte) (string, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := v.aead.Seal(nonce, nonce, []byte(plain), aad)
	return base64.RawStdEncoding.EncodeToString(out), nil
}

func (v *Vault) Open(ciphertext string, aad []byte) (string, error) {
	in, err := base64.RawStdEncoding.DecodeString(ciphertext)
	if err != nil || len(in) < v.aead.NonceSize() {
		return "", fmt.Errorf("invalid encrypted value")
	}
	n := v.aead.NonceSize()
	plain, err := v.aead.Open(nil, in[:n], in[n:], aad)
	if err != nil {
		return "", fmt.Errorf("decrypt value: %w", err)
	}
	return string(plain), nil
}
