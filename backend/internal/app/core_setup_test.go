package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LoliaFRP/loliafrp-fnos/backend/internal/repository"
)

type setupFixture struct {
	service *Service
	release func()
	catalog atomic.Int32
	files   atomic.Int32
	links   atomic.Int32
}

func newSetupFixture(t *testing.T, installed, badChecksum, fallback bool) *setupFixture {
	t.Helper()
	binary, err := os.ReadFile("/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(binary)
	digest := hex.EncodeToString(hash[:])
	if badChecksum {
		digest = strings.Repeat("0", 64)
	}
	filename := "frpc_linux_" + runtime.GOARCH
	gate := make(chan struct{})
	fixture := &setupFixture{}
	var once sync.Once
	fixture.release = func() { once.Do(func() { close(gate) }) }
	fileServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.files.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("OAuth token was forwarded to the binary download host")
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(binary)))
		w.Write(binary[:1024])
		w.(http.Flusher).Flush()
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
		w.Write(binary[1024:])
	}))
	t.Cleanup(fileServer.Close)
	t.Cleanup(fixture.release)
	// Trust only this local TLS fixture while retaining the installer's HTTPS checks.
	transport := http.DefaultTransport
	http.DefaultTransport = fileServer.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = transport })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/api/v1/oauth2/") {
			switch r.URL.Path {
			case "/api/v1/oauth2/device/code":
				fmt.Fprint(w, `{"device_code":"private","user_code":"ABCD-EFGH","verification_uri":"https://dash.lolia.link/oauth/device","expires_in":600,"interval":5}`)
			case "/api/v1/oauth2/token":
				fmt.Fprint(w, `{"access_token":"account-token","token_type":"Bearer","expires_in":3600}`)
			default:
				fmt.Fprint(w, `{}`)
			}
			return
		}
		if r.Header.Get("Authorization") != "Bearer account-token" {
			t.Errorf("missing account token for %s", r.URL.Path)
		}
		var data any
		switch r.URL.Path {
		case "/api/v1/user/info":
			data = map[string]any{"id": 1, "username": "lolia"}
		case "/api/v1/user/tunnel":
			data = map[string]any{"list": []any{}, "total": 0}
		case "/api/v1/releases":
			fixture.catalog.Add(1)
			// Repository metadata alone must not match; select the exact configured slug.
			data = map[string]any{"items": []any{map[string]any{"id": 8, "slug": "other-client", "github_owner": "Lolia-FRP", "github_repo": "lolia-frp"}, map[string]any{"id": 9, "slug": "lolia_frp_cli"}}, "total": 2}
		case "/api/v1/releases/versions/latest":
			if r.URL.Query().Get("release_id") != "9" {
				t.Error("wrong release selected")
			}
			data = map[string]any{"id": 10, "tag": "0.69.2", "prerelease": false}
		case "/api/v1/releases/versions/10/assets":
			if r.URL.Query().Get("os") != "linux" || r.URL.Query().Get("arch") != runtime.GOARCH {
				t.Error("incorrect platform selection")
			}
			sources := []any{map[string]any{"source": "2", "enabled": true, "global_enabled": true, "sort_order": 2}}
			if fallback {
				sources = append(sources, map[string]any{"source": "1", "enabled": true, "global_enabled": true, "sort_order": 1})
			}
			data = map[string]any{"items": []any{map[string]any{"id": 11, "filename": filename, "os": "linux", "arch": runtime.GOARCH, "checksum": digest, "size": len(binary), "available_sources": sources}, map[string]any{"id": 12, "filename": "frps_linux_" + runtime.GOARCH, "os": "linux", "arch": runtime.GOARCH}, map[string]any{"id": 13, "filename": "frpc_windows.exe", "os": "windows", "arch": runtime.GOARCH}}}
		case "/api/v1/releases/download":
			fixture.links.Add(1)
			if r.URL.Query().Get("release_id") != "9" || r.URL.Query().Get("asset_id") != "11" {
				t.Error("wrong binary selected")
			}
			if r.URL.Query().Get("source") == "1" {
				w.WriteHeader(409)
				fmt.Fprint(w, `{"code":409,"msg":"source unavailable"}`)
				return
			}
			data = map[string]any{"url": fileServer.URL, "checksum": digest, "filename": filename, "size": len(binary), "tag": "0.69.2"}
		default:
			w.WriteHeader(404)
			fmt.Fprint(w, `{"code":404,"msg":"not found"}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": data})
	}))
	t.Cleanup(upstream.Close)
	root := t.TempDir()
	if installed {
		if err := os.MkdirAll(filepath.Join(root, "data/core"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "data/core/frpc"), binary, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "data/core/frpc.version"), []byte("existing"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(context.Background(), Options{DataDir: filepath.Join(root, "data"), TempDir: filepath.Join(root, "tmp"), ConfigDir: filepath.Join(root, "etc"), AppDest: filepath.Join(root, "app"), APIBase: upstream.URL + "/api/v1", Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	s.opts.Dev = false
	fixture.service = s
	t.Cleanup(func() { s.Close() })
	return fixture
}
func authorizeFixture(t *testing.T, s *Service, uid string) {
	t.Helper()
	if err := s.store.SaveCredentials(context.Background(), uid, "lolia", repository.Credentials{AccessToken: "account-token", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
}
func waitCore(t *testing.T, s *Service, state string) CoreStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status := s.coreStatus()
		if status.State == state {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("core did not enter %s: %+v", state, s.coreStatus())
	return CoreStatus{}
}

func TestLoginAutomaticallyDownloadsCoreAndReportsProgress(t *testing.T) {
	f := newSetupFixture(t, false, false, true)
	h := f.service.Handler()
	// No catalog requests or downloads are allowed before login.
	if w := call(h, "POST", "/core/setup", "1000", "false", `{}`); w.Code != 401 {
		t.Fatal(w.Body.String())
	}
	if f.catalog.Load() != 0 {
		t.Fatal("queried authenticated releases before login")
	}
	if w := call(h, "POST", "/auth/login", "1000", "false", `{}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	f.service.mu.Lock()
	f.service.pending["1000"].Next = time.Now().Add(-time.Second)
	f.service.mu.Unlock()
	if w := call(h, "POST", "/auth/login/poll", "1000", "false", `{}`); w.Code != 200 || !strings.Contains(w.Body.String(), "authenticated") {
		t.Fatal(w.Body.String())
	}
	status := waitCore(t, f.service, "downloading")
	if status.Total <= 0 || status.Downloaded <= 0 || status.Progress >= 100 || status.Installed {
		t.Fatalf("incorrect partial progress: %+v", status)
	}
	w := call(h, "GET", "/core/status", "1000", "false", `{}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "downloading") {
		t.Fatalf("status blocked during transfer: %s", w.Body.String())
	}
	if w := call(h, "POST", "/tunnels/1234567890abcdef1234567890abcdef/start", "1000", "false", `{}`); w.Code != 409 {
		t.Fatal("starting without ready core was not blocked")
	}
	// Opening another session and retrying setup must join the same task.
	for range 3 {
		if w := call(h, "GET", "/session", "1000", "false", `{}`); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		if w := call(h, "POST", "/core/setup", "1000", "false", `{}`); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	f.release()
	status = waitCore(t, f.service, "ready")
	if !status.Installed || status.Version != "0.69.2" || status.Progress != 100 {
		t.Fatalf("core not installed: %+v", status)
	}
	if f.catalog.Load() != 1 || f.files.Load() != 1 || f.links.Load() != 2 {
		t.Fatalf("duplicated setup or failed source fallback: catalog=%d files=%d links=%d", f.catalog.Load(), f.files.Load(), f.links.Load())
	}
}
func TestSetupContinuesAfterBrowserRequestEndsAndExistingCoreIsReused(t *testing.T) {
	f := newSetupFixture(t, false, false, false)
	authorizeFixture(t, f.service, "1000")
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", Prefix+"/api/v1/core/setup", strings.NewReader(`{}`)).WithContext(ctx)
	r.Header.Set("X-Trim-Userid", "1000")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-LoliaFRP-Request", "1")
	w := httptest.NewRecorder()
	f.service.Handler().ServeHTTP(w, r)
	cancel()
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	waitCore(t, f.service, "downloading")
	f.release()
	waitCore(t, f.service, "ready")
	if w := call(f.service.Handler(), "POST", "/core/setup", "1000", "false", `{}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if f.catalog.Load() != 1 || f.files.Load() != 1 {
		t.Fatal("re-downloaded an installed core")
	}
}
func TestFailedUpdateKeepsExistingCoreAndRequiresAdmin(t *testing.T) {
	f := newSetupFixture(t, true, true, false)
	authorizeFixture(t, f.service, "1000")
	h := f.service.Handler()
	if w := call(h, "GET", "/session", "1000", "false", `{}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if f.catalog.Load() != 0 {
		t.Fatal("existing core was automatically replaced")
	}
	if w := call(h, "POST", "/core/update", "1000", "false", `{}`); w.Code != 403 {
		t.Fatal("non-admin updated shared core")
	}
	if w := call(h, "POST", "/core/setup", "1000", "false", `{"releaseId":8}`); w.Code != 400 {
		t.Fatal("caller-controlled release was accepted")
	}
	if w := call(h, "POST", "/core/update", "1000", "true", `{}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	waitCore(t, f.service, "downloading")
	f.release()
	status := waitCore(t, f.service, "failed")
	if !status.Installed || status.Version != "existing" || !strings.Contains(status.Error, "SHA-256") {
		t.Fatalf("failed update damaged installed core: %+v", status)
	}
}
func TestLogoutCancelsOwnedDownloadAndLeavesNoPartialCore(t *testing.T) {
	f := newSetupFixture(t, false, false, false)
	authorizeFixture(t, f.service, "1000")
	h := f.service.Handler()
	if w := call(h, "GET", "/session", "1000", "false", `{}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	waitCore(t, f.service, "downloading")
	if w := call(h, "POST", "/auth/logout", "1000", "false", `{}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	status := waitCore(t, f.service, "failed")
	if status.Installed {
		t.Fatal("cancelled download was installed")
	}
	files, err := os.ReadDir(filepath.Join(f.service.opts.DataDir, "core"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("partial download files remained: %v", files)
	}
}

func TestCoreCatalogSelectionRejectsOtherClientsAndUnsupportedFiles(t *testing.T) {
	if !isCoreRelease(coreRelease{Slug: "lolia_frp_cli"}) {
		t.Fatal("configured core slug did not match")
	}
	for _, slug := range []string{"other-client", "lolia-frp", "loliafrp", "loliafrp-cli", "Lolia_FRP_CLI", " lolia_frp_cli"} {
		if isCoreRelease(coreRelease{Slug: slug}) {
			t.Fatalf("unconfigured software matched: %q", slug)
		}
	}
	good := asset{ID: 1, Filename: "frpc_linux_" + runtime.GOARCH, OS: "linux", Arch: runtime.GOARCH, Checksum: strings.Repeat("a", 64)}
	if selected, err := selectCoreAsset([]asset{good}); err != nil || selected.ID != 1 {
		t.Fatalf("supported binary: %v", err)
	}
	for _, change := range []func(*asset){
		func(a *asset) { a.Filename = "frps_linux_" + runtime.GOARCH },
		func(a *asset) { a.Arch = "any" },
		func(a *asset) { a.OS = "windows" },
		func(a *asset) { a.Checksum = "" },
		func(a *asset) { a.Filename = "frpc.deb" },
	} {
		bad := good
		change(&bad)
		if _, err := selectCoreAsset([]asset{bad}); err == nil {
			t.Fatalf("unsupported artifact accepted: %+v", bad)
		}
	}
	duplicate := good
	duplicate.ID = 2
	if _, err := selectCoreAsset([]asset{good, duplicate}); err == nil {
		t.Fatal("ambiguous binaries accepted")
	}
}
