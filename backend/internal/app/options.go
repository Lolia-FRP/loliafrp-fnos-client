package app

import (
	"github.com/LoliaFRP/loliafrp-fnos/backend/internal/api"
	"os"
	"path/filepath"
)

var Version = "dev"

// OAuthClientID identifies the Public OAuth application shipped with this client.
const OAuthClientID = "lccnu723pyw1uoc8"

type Options struct {
	DataDir, TempDir, ConfigDir, AppDest, APIBase, CoreBinary string
	Dev                                                       bool
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func FromEnvironment() Options {
	root := env("LOLIA_DEV_ROOT", filepath.Join(os.TempDir(), "loliafrp-fnos"))
	return Options{DataDir: env("TRIM_PKGVAR", filepath.Join(root, "data")), TempDir: env("TRIM_PKGTMP", filepath.Join(root, "tmp")), ConfigDir: env("TRIM_PKGETC", filepath.Join(root, "etc")), AppDest: env("TRIM_APPDEST", filepath.Join(root, "app")), APIBase: env("LOLIA_API_BASE_URL", api.DefaultBaseURL), CoreBinary: os.Getenv("LOLIA_CORE_BINARY"), Dev: os.Getenv("LOLIA_DEV_MODE") == "1"}
}
