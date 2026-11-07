package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/LoliaFRP/loliafrp-fnos/backend/internal/repository"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setup(t *testing.T) (*Service, *int) {
	t.Helper()
	polls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/oauth2/device/code":
			if err := r.ParseForm(); err != nil || r.Form.Get("client_id") != "lccnu723pyw1uoc8" {
				t.Error("device authorization must use the built-in public client ID")
			}
			fmt.Fprint(w, `{"device_code":"hidden_device_code","user_code":"ABCD-EFGH","verification_uri":"https://dash.lolia.link/oauth/device","expires_in":600,"interval":5}`)
		case "/api/v1/oauth2/token":
			polls++
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"slow_down"}`)
		case "/api/v1/user/info":
			fmt.Fprint(w, `{"code":200,"data":{"id":1,"username":"lolia","avatar":"https://cdn.example.com/avatars/lolia.png","role":"user"}}`)
		case "/api/v1/user/tunnel":
			fmt.Fprint(w, `{"code":200,"data":{"list":[],"total":0}}`)
		case "/api/v1/user/announcements/top", "/api/v1/user/announcements/board":
			if r.Header.Get("Authorization") != "Bearer oauth2_secret" {
				t.Error("announcement endpoint must receive the user's OAuth access token")
			}
			location := strings.TrimPrefix(r.URL.Path, "/api/v1/user/announcements/")
			fmt.Fprintf(w, `{"code":200,"data":{"items":[{"id":1,"title":"%s notice","content":"line one\nline two","level":"warning","action_label":"Details","action_url":"https://docs.lolia.link"}]}}`, location)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(upstream.Close)
	root := t.TempDir()
	// A legacy installation setting must not override the shipped OAuth app.
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "client-id.txt"), []byte("legacy-client"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), Options{DataDir: filepath.Join(root, "data"), TempDir: filepath.Join(root, "tmp"), ConfigDir: filepath.Join(root, "etc"), AppDest: filepath.Join(root, "app"), APIBase: upstream.URL + "/api/v1", Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	s.opts.Dev = false
	t.Cleanup(func() { s.Close() })
	return s, &polls
}
func TestBuiltInOAuthClientCannotBeOverridden(t *testing.T) {
	t.Setenv("LOLIA_OAUTH_CLIENT_ID", "legacy-environment-client")
	s, _ := setup(t)
	h := s.Handler()
	if w := call(h, "POST", "/auth/login", "1000", "false", `{}`); w.Code != 200 {
		t.Fatalf("login without administrator setup: %s", w.Body.String())
	}
	if w := call(h, "PUT", "/settings", "1000", "true", `{"clientId":"replacement-client"}`); w.Code != 400 {
		t.Fatalf("client ID override should be rejected: %s", w.Body.String())
	}
	if w := call(h, "PUT", "/settings", "1000", "false", `{"bootAutoStart":true}`); w.Code != 200 {
		t.Fatalf("per-user startup preference: %s", w.Body.String())
	}
	w := call(h, "GET", "/settings", "1000", "false", `{}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"bootAutoStart":true`) || strings.Contains(w.Body.String(), "clientId") {
		t.Fatalf("settings should contain user preferences without client setup: %s", w.Body.String())
	}
}
func call(h http.Handler, method, path, uid, admin, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, Prefix+"/api/v1"+path, strings.NewReader(body))
	r.Header.Set("X-Trim-Userid", uid)
	r.Header.Set("X-Trim-Isadmin", admin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-LoliaFRP-Request", "1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestGatewayIsolationAndAdmin(t *testing.T) {
	s, _ := setup(t)
	h := s.Handler()
	if w := call(h, "GET", "/session", "", "", "{}"); w.Code != 401 {
		t.Fatalf("missing gateway identity: %d", w.Code)
	}
	if err := s.store.SaveCredentials(context.Background(), "1000", "lolia", repository.Credentials{AccessToken: "oauth2_secret", RefreshToken: "refresh_secret", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	for _, uid := range []string{"1000", "1001"} {
		w := call(h, "GET", "/session", uid, "false", "{}")
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var payload struct {
			Data struct {
				Authenticated bool `json:"authenticated"`
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Data.Authenticated != (uid == "1000") {
			t.Errorf("wrong user session for %s", uid)
		}
		if uid == "1000" && !strings.Contains(w.Body.String(), `"avatar":"https://cdn.example.com/avatars/lolia.png"`) {
			t.Error("user avatar was not forwarded in the session")
		}
		if strings.Contains(w.Body.String(), "secret") {
			t.Error("credential leaked")
		}
	}
	if w := call(h, "POST", "/core/install", "1000", "false", `{}`); w.Code != 403 {
		t.Fatalf("non-admin installation: %d", w.Code)
	}
	r := httptest.NewRequest("POST", Prefix+"/api/v1/auth/logout", strings.NewReader(`{}`))
	r.Header.Set("X-Trim-Userid", "1000")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Error("mutation guard missing")
	}
	if w := call(h, "POST", "/tunnels/../../etc/start", "1000", "true", `{}`); w.Code == 200 {
		t.Error("accepted unsafe tunnel path")
	}
}
func TestDevicePollingIsPrivateAndThrottled(t *testing.T) {
	s, polls := setup(t)
	h := s.Handler()
	w := call(h, "POST", "/auth/login", "1000", "true", `{}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if strings.Contains(w.Body.String(), "hidden_device_code") {
		t.Error("device_code leaked")
	}
	w = call(h, "POST", "/auth/login/poll", "1000", "true", `{}`)
	if *polls != 0 {
		t.Error("polled upstream before allowed interval")
	}
	if w = call(h, "POST", "/auth/login/poll", "1001", "false", `{}`); w.Code == 200 {
		t.Error("another user accessed device authorization")
	}
	s.mu.Lock()
	s.pending["1000"].Next = time.Now().Add(-time.Second)
	s.mu.Unlock()
	w = call(h, "POST", "/auth/login/poll", "1000", "true", `{}`)
	if *polls != 1 || !strings.Contains(w.Body.String(), `"interval":10`) {
		t.Fatalf("slow_down not honored: %s", w.Body.String())
	}
	w = call(h, "POST", "/auth/login/poll", "1000", "true", `{}`)
	if *polls != 1 {
		t.Error("slow_down polling guard bypassed")
	}
}

func TestTopAnnouncementsAndBoardUseSeparateEndpoints(t *testing.T) {
	s, _ := setup(t)
	h := s.Handler()
	if w := call(h, "GET", "/announcements/top", "1000", "false", `{}`); w.Code != 401 {
		t.Fatalf("unbound account should not access announcements: %d", w.Code)
	}
	if err := s.store.SaveCredentials(context.Background(), "1000", "lolia", repository.Credentials{AccessToken: "oauth2_secret", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	w := call(h, "GET", "/announcements/top", "1000", "false", `{}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "top notice") || !strings.Contains(w.Body.String(), "action_url") {
		t.Fatalf("top announcement fields lost: %s", w.Body.String())
	}
	if w := call(h, "GET", "/announcements/top", "1001", "false", `{}`); w.Code != 401 {
		t.Fatal("another NAS user accessed the bound account's announcements")
	}
	w = call(h, "GET", "/overview", "1000", "false", `{}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "board notice") || strings.Contains(w.Body.String(), "top notice") {
		t.Fatalf("board must be independent of top notices: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"avatar":"https://cdn.example.com/avatars/lolia.png"`) {
		t.Fatal("user avatar was not forwarded in the overview")
	}
}
