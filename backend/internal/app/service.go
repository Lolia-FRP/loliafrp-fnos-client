package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/LoliaFRP/loliafrp-fnos/backend/internal/api"
	"github.com/LoliaFRP/loliafrp-fnos/backend/internal/core"
	"github.com/LoliaFRP/loliafrp-fnos/backend/internal/fnos"
	"github.com/LoliaFRP/loliafrp-fnos/backend/internal/oauth"
	"github.com/LoliaFRP/loliafrp-fnos/backend/internal/repository"
	"github.com/LoliaFRP/loliafrp-fnos/backend/internal/secrets"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const Prefix = "/app/loliafrp"

type pending struct {
	Device        oauth.Device
	Expires, Next time.Time
}
type Service struct {
	opts      Options
	api       *api.Client
	store     *repository.Store
	core      *core.Runtime
	mu        sync.Mutex
	locks     map[string]*sync.Mutex
	pending   map[string]*pending
	ctx       context.Context
	cancel    context.CancelFunc
	workers   sync.WaitGroup
	coreMu    sync.Mutex
	coreTask  *coreTask
	coreState CoreStatus
}

func New(ctx context.Context, o Options) (*Service, error) {
	for _, p := range []string{o.DataDir, o.TempDir, o.ConfigDir} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return nil, err
		}
	}
	vault, err := secrets.Open(filepath.Join(o.DataDir, "master.key"))
	if err != nil {
		return nil, err
	}
	store, err := repository.Open(ctx, filepath.Join(o.DataDir, "database.sqlite"), vault)
	if err != nil {
		return nil, err
	}
	client, err := api.New(o.APIBase, o.Dev)
	if err != nil {
		store.Close()
		return nil, err
	}
	serviceCtx, cancel := context.WithCancel(ctx)
	fallback := filepath.Join(o.AppDest, "bin", "frpc")
	if o.Dev && o.CoreBinary != "" {
		fallback = o.CoreBinary
	}
	s := &Service{opts: o, api: client, store: store, ctx: serviceCtx, cancel: cancel, locks: map[string]*sync.Mutex{}, pending: map[string]*pending{}}
	s.core = core.New(serviceCtx, o.TempDir, filepath.Join(o.DataDir, "core", "frpc"), fallback, filepath.Join(o.DataDir, "certificates"))
	s.coreState = CoreStatus{Info: s.core.Info(), State: "missing"}
	if s.coreState.Installed {
		s.coreState.State = "ready"
	}
	s.workers.Add(1)
	go func() { defer s.workers.Done(); s.restore() }()
	return s, nil
}
func (s *Service) Close() error {
	s.coreMu.Lock()
	s.cancel()
	s.coreMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s.core.StopAll(ctx)
	s.workers.Wait()
	return s.store.Close()
}
func (s *Service) lock(uid string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks[uid] == nil {
		s.locks[uid] = &sync.Mutex{}
	}
	return s.locks[uid]
}
func (s *Service) oauth() *oauth.Client {
	return oauth.New(s.opts.APIBase, OAuthClientID)
}
func write(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}
func problem(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var e *api.Error
	if errors.As(err, &e) {
		status = e.Status
		if status < 400 || status > 599 {
			status = 502
		}
	}
	if errors.Is(err, repository.ErrNotFound) {
		status = 401
	}
	var oauthProblem *oauth.Error
	if errors.As(err, &oauthProblem) {
		switch oauthProblem.Code {
		case "invalid_grant", "invalid_client", "access_denied":
			status = 401
		case "server_error", "temporarily_unavailable":
			status = 503
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": err.Error()}})
}
func decode(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return errors.New("请求 JSON 无效")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("请求只允许一个 JSON 对象")
	}
	return nil
}
func (s *Service) route(fn func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := fnos.Current(r.Context()).UID
		lock := s.lock(uid)
		lock.Lock()
		defer lock.Unlock()
		fn(w, r, uid)
	}
}
func (s *Service) token(ctx context.Context, uid string) (string, error) {
	c, err := s.store.Credentials(ctx, uid)
	if err != nil {
		return "", err
	}
	if c.ExpiresAt > time.Now().Add(time.Minute).Unix() {
		return c.AccessToken, nil
	}
	t, err := s.oauth().Refresh(ctx, c.RefreshToken)
	if err != nil {
		return "", err
	}
	if t.Refresh == "" {
		t.Refresh = c.RefreshToken
	}
	if t.Scope == "" {
		t.Scope = c.Scope
	}
	if err = s.save(ctx, uid, "", t); err != nil {
		return "", err
	}
	return t.Access, nil
}
func (s *Service) save(ctx context.Context, uid, name string, t oauth.Token) error {
	return s.store.SaveCredentials(ctx, uid, name, repository.Credentials{AccessToken: t.Access, RefreshToken: t.Refresh, TokenType: t.Type, Scope: t.Scope, ExpiresAt: time.Now().Unix() + int64(t.ExpiresIn)})
}
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	add := func(method, path string, fn func(http.ResponseWriter, *http.Request, string)) {
		mux.HandleFunc(method+" "+Prefix+"/api/v1"+path, s.route(fn))
	}
	add("GET", "/session", s.session)
	add("POST", "/auth/login", s.login)
	add("POST", "/auth/login/poll", s.poll)
	add("POST", "/auth/logout", s.logout)
	add("GET", "/settings", s.settings)
	add("PUT", "/settings", s.updateSettings)
	add("GET", "/overview", s.overview)
	add("GET", "/announcements/top", s.proxy("GET", "/user/announcements/top"))
	add("GET", "/tunnels", s.tunnels)
	add("POST", "/tunnels", s.createTunnel)
	add("PUT", "/tunnels/{name}", s.editTunnel)
	add("DELETE", "/tunnels/{name}", s.deleteTunnel)
	add("POST", "/tunnels/{name}/start", s.start)
	add("POST", "/tunnels/{name}/stop", s.stop)
	add("PUT", "/tunnels/{name}/auto-start", s.autoStart)
	add("GET", "/processes", func(w http.ResponseWriter, r *http.Request, uid string) { write(w, s.core.Statuses(uid)) })
	add("GET", "/nodes", s.nodes)
	add("GET", "/domains", s.proxy("GET", "/user/domain"))
	add("POST", "/domains", s.proxy("POST", "/user/domain"))
	add("POST", "/domains/verify", s.proxy("POST", "/user/domain/verify"))
	add("DELETE", "/domains/{id}", func(w http.ResponseWriter, r *http.Request, uid string) {
		id, err := strconv.ParseUint(r.PathValue("id"), 10, 32)
		if err != nil || id == 0 {
			problem(w, errors.New("无效域名 ID"))
			return
		}
		s.proxy("DELETE", fmt.Sprintf("/user/domain/%d", id))(w, r, uid)
	})
	add("GET", "/core/status", func(w http.ResponseWriter, r *http.Request, uid string) { write(w, s.coreStatus()) })
	add("POST", "/core/setup", s.setupCore)
	add("POST", "/core/update", s.updateCore)
	add("GET", "/core/releases", s.proxy("GET", "/releases"))
	add("GET", "/core/assets", s.assets)
	add("POST", "/core/install", s.install)
	mux.HandleFunc("GET "+Prefix+"/api/v1/health", func(w http.ResponseWriter, r *http.Request) { write(w, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET "+Prefix+"/api/v1/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("GET "+Prefix+"/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(s.opts.AppDest, "web", "index.html"))
	})
	mux.Handle("GET "+Prefix+"/", http.StripPrefix(Prefix+"/", http.FileServer(http.Dir(filepath.Join(s.opts.AppDest, "web")))))
	mux.HandleFunc("GET "+Prefix, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, Prefix+"/", http.StatusTemporaryRedirect)
	})
	guard := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if r.Method != "GET" && r.Method != "HEAD" {
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || r.Header.Get("X-LoliaFRP-Request") != "1" {
				http.Error(w, "forbidden", 403)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
	return fnos.Middleware(s.opts.Dev, guard)
}
func (s *Service) session(w http.ResponseWriter, r *http.Request, uid string) {
	token, err := s.token(r.Context(), uid)
	var expired *oauth.Error
	if errors.Is(err, repository.ErrNotFound) || (errors.As(err, &expired) && (expired.Code == "invalid_grant" || expired.Code == "invalid_client")) {
		write(w, map[string]any{"user": fnos.Current(r.Context()), "authenticated": false})
		return
	}
	if err != nil {
		problem(w, err)
		return
	}
	u, err := s.api.User(r.Context(), token)
	var upstream *api.Error
	if errors.As(err, &upstream) && upstream.Status == 401 {
		write(w, map[string]any{"user": fnos.Current(r.Context()), "authenticated": false})
		return
	}
	if err != nil {
		problem(w, err)
		return
	}
	s.ensureCore(uid, token, false, false)
	write(w, map[string]any{"user": fnos.Current(r.Context()), "authenticated": true, "account": u})
}
func (s *Service) login(w http.ResponseWriter, r *http.Request, uid string) {
	s.mu.Lock()
	p := s.pending[uid]
	s.mu.Unlock()
	if p == nil || time.Now().After(p.Expires) {
		d, err := s.oauth().Device(r.Context())
		if err != nil {
			problem(w, err)
			return
		}
		p = &pending{Device: d, Expires: time.Now().Add(time.Duration(d.ExpiresIn) * time.Second), Next: time.Now().Add(time.Duration(d.Interval) * time.Second)}
		s.mu.Lock()
		s.pending[uid] = p
		s.mu.Unlock()
	}
	write(w, map[string]any{"userCode": p.Device.UserCode, "verificationUri": p.Device.VerificationURI, "verificationUriComplete": p.Device.VerificationComplete, "interval": p.Device.Interval, "expiresIn": max(0, int(time.Until(p.Expires).Seconds()))})
}
func (s *Service) poll(w http.ResponseWriter, r *http.Request, uid string) {
	s.mu.Lock()
	p := s.pending[uid]
	s.mu.Unlock()
	if p == nil {
		problem(w, errors.New("请重新申请设备码"))
		return
	}
	clear := func() { s.mu.Lock(); delete(s.pending, uid); s.mu.Unlock() }
	if time.Now().After(p.Expires) {
		clear()
		write(w, map[string]any{"status": "expired_token"})
		return
	}
	if time.Now().Before(p.Next) {
		write(w, map[string]any{"status": "authorization_pending", "interval": p.Device.Interval})
		return
	}
	p.Next = time.Now().Add(time.Duration(p.Device.Interval) * time.Second)
	t, err := s.oauth().Poll(r.Context(), p.Device.DeviceCode)
	if err != nil {
		var e *oauth.Error
		if errors.As(err, &e) {
			switch e.Code {
			case "authorization_pending":
			case "slow_down":
				p.Device.Interval += 5
				p.Next = time.Now().Add(time.Duration(p.Device.Interval) * time.Second)
			case "access_denied", "expired_token":
				clear()
			case "server_error", "temporarily_unavailable":
				problem(w, err)
				return
			default:
				clear()
				problem(w, err)
				return
			}
			write(w, map[string]any{"status": e.Code, "interval": p.Device.Interval})
			return
		}
		problem(w, err)
		return
	}
	u, err := s.api.User(r.Context(), t.Access)
	if err != nil {
		clear()
		problem(w, err)
		return
	}
	if err = s.core.StopUser(r.Context(), uid); err != nil {
		problem(w, err)
		return
	}
	if err = s.store.DeleteAccount(r.Context(), uid); err != nil {
		problem(w, err)
		return
	}
	if err = s.save(r.Context(), uid, u.Username, t); err != nil {
		problem(w, err)
		return
	}
	clear()
	s.ensureCore(uid, t.Access, true, false)
	write(w, map[string]string{"status": "authenticated"})
}
func (s *Service) logout(w http.ResponseWriter, r *http.Request, uid string) {
	s.cancelCoreSetup(uid)
	if err := s.core.StopUser(r.Context(), uid); err != nil {
		problem(w, err)
		return
	}
	s.mu.Lock()
	delete(s.pending, uid)
	s.mu.Unlock()
	c, _ := s.store.Credentials(r.Context(), uid)
	warning := ""
	client := s.oauth()
	for _, item := range [][2]string{{c.RefreshToken, "refresh_token"}, {c.AccessToken, "access_token"}} {
		if item[0] != "" {
			if err := client.Revoke(r.Context(), item[0], item[1]); err != nil {
				warning = "本地账号已移除；远程撤销失败，可在 LoliaFRP 控制台撤销授权"
			}
		}
	}
	if err := s.store.DeleteAccount(r.Context(), uid); err != nil {
		problem(w, err)
		return
	}
	write(w, map[string]string{"warning": warning})
}
func (s *Service) settings(w http.ResponseWriter, r *http.Request, uid string) {
	boot, err := s.store.BootAutoStart(r.Context(), uid)
	if err != nil {
		problem(w, err)
		return
	}
	write(w, map[string]any{"bootAutoStart": boot, "version": Version, "arch": runtime.GOARCH})
}
func (s *Service) updateSettings(w http.ResponseWriter, r *http.Request, uid string) {
	var input struct {
		Boot *bool `json:"bootAutoStart"`
	}
	if err := decode(w, r, &input); err != nil {
		problem(w, err)
		return
	}
	if input.Boot != nil {
		if err := s.store.SetBootAutoStart(r.Context(), uid, *input.Boot); err != nil {
			problem(w, err)
			return
		}
	}
	write(w, map[string]bool{"saved": true})
}
func (s *Service) overview(w http.ResponseWriter, r *http.Request, uid string) {
	token, err := s.token(r.Context(), uid)
	if err != nil {
		problem(w, err)
		return
	}
	u, err := s.api.User(r.Context(), token)
	if err != nil {
		problem(w, err)
		return
	}
	result := map[string]any{"account": u}
	warnings := map[string]string{}
	for key, path := range map[string]string{"stats": "/user/traffic/stats", "summary": "/user/traffic/summary?days=7", "daily": "/user/traffic/daily?days=7", "announcements": "/user/announcements/board"} {
		var data any
		if err = s.api.Do(r.Context(), token, "GET", path, nil, &data); err != nil {
			warnings[key] = err.Error()
		} else {
			result[key] = data
		}
	}
	result["warnings"] = warnings
	write(w, result)
}
func (s *Service) tunnels(w http.ResponseWriter, r *http.Request, uid string) {
	token, err := s.token(r.Context(), uid)
	if err != nil {
		problem(w, err)
		return
	}
	items, err := s.api.Tunnels(r.Context(), token)
	if err != nil {
		problem(w, err)
		return
	}
	prefs, err := s.store.Preferences(r.Context(), uid)
	if err != nil {
		problem(w, err)
		return
	}
	write(w, map[string]any{"items": items, "preferences": prefs, "processes": s.core.Statuses(uid)})
}
func (s *Service) nodes(w http.ResponseWriter, r *http.Request, uid string) {
	token, err := s.token(r.Context(), uid)
	if err != nil {
		problem(w, err)
		return
	}
	nodes := []any{}
	for page := 1; page <= 1000; page++ {
		var data struct {
			Nodes []any `json:"nodes"`
			Total int   `json:"total"`
		}
		if err = s.api.Do(r.Context(), token, "POST", "/user/nodes", map[string]int{"page": page, "limit": 1000}, &data); err != nil {
			problem(w, err)
			return
		}
		nodes = append(nodes, data.Nodes...)
		if len(data.Nodes) == 0 || len(nodes) >= data.Total {
			write(w, map[string]any{"nodes": nodes, "total": data.Total})
			return
		}
	}
	problem(w, errors.New("节点分页超过限制"))
}

var tunnelName = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)

func name(r *http.Request) (string, error) {
	v := r.PathValue("name")
	if !tunnelName.MatchString(v) {
		return "", errors.New("无效隧道名称")
	}
	return v, nil
}
func (s *Service) startTunnel(ctx context.Context, uid, n string) error {
	token, err := s.token(ctx, uid)
	if err != nil {
		return err
	}
	b, err := s.api.Config(ctx, token, n)
	if err != nil {
		return err
	}
	return s.core.Start(uid, n, b)
}
func (s *Service) start(w http.ResponseWriter, r *http.Request, uid string) {
	if !s.coreStatus().Installed {
		problem(w, &api.Error{Status: http.StatusConflict, Message: "隧道内核尚未就绪，请等待自动下载完成或重试下载"})
		return
	}
	n, err := name(r)
	if err == nil {
		err = s.startTunnel(r.Context(), uid, n)
	}
	if err != nil {
		problem(w, err)
		return
	}
	write(w, map[string]bool{"started": true})
}
func (s *Service) stop(w http.ResponseWriter, r *http.Request, uid string) {
	n, err := name(r)
	if err == nil {
		err = s.core.Stop(r.Context(), uid, n)
	}
	if err != nil {
		problem(w, err)
		return
	}
	write(w, map[string]bool{"stopped": true})
}
func (s *Service) autoStart(w http.ResponseWriter, r *http.Request, uid string) {
	n, err := name(r)
	if err != nil {
		problem(w, err)
		return
	}
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if err = decode(w, r, &input); err != nil {
		problem(w, err)
		return
	}
	token, err := s.token(r.Context(), uid)
	if err == nil {
		var t api.Tunnel
		err = s.api.Do(r.Context(), token, "GET", "/user/tunnel/"+n, nil, &t)
	}
	if err == nil {
		err = s.store.SetAutoStart(r.Context(), uid, n, input.Enabled)
	}
	if err != nil {
		problem(w, err)
		return
	}
	write(w, map[string]bool{"saved": true})
}
func (s *Service) createTunnel(w http.ResponseWriter, r *http.Request, uid string) {
	token, err := s.token(r.Context(), uid)
	if err != nil {
		problem(w, err)
		return
	}
	var input map[string]any
	if err = decode(w, r, &input); err != nil {
		problem(w, err)
		return
	}
	var t api.Tunnel
	err = s.api.Do(r.Context(), token, "POST", "/user/tunnel", input, &t)
	if err != nil {
		problem(w, err)
		return
	}
	write(w, t)
}
func (s *Service) editTunnel(w http.ResponseWriter, r *http.Request, uid string) {
	s.changeTunnel(w, r, uid, "PUT")
}
func (s *Service) deleteTunnel(w http.ResponseWriter, r *http.Request, uid string) {
	s.changeTunnel(w, r, uid, "DELETE")
}
func (s *Service) changeTunnel(w http.ResponseWriter, r *http.Request, uid, method string) {
	n, err := name(r)
	if err != nil {
		problem(w, err)
		return
	}
	token, err := s.token(r.Context(), uid)
	if err != nil {
		problem(w, err)
		return
	}
	var input any
	if method == "PUT" {
		var m map[string]any
		if err = decode(w, r, &m); err != nil {
			problem(w, err)
			return
		}
		input = m
	}
	// Ownership is checked by center before any process or preference mutation.
	var out api.Tunnel
	if err = s.api.Do(r.Context(), token, method, "/user/tunnel/"+n, input, &out); err != nil {
		problem(w, err)
		return
	}
	if err = s.core.Stop(r.Context(), uid, n); err != nil {
		problem(w, err)
		return
	}
	if method == "DELETE" {
		if err = s.store.SetAutoStart(r.Context(), uid, n, false); err != nil {
			problem(w, err)
			return
		}
	}
	write(w, map[string]string{"message": "操作完成；修改后的隧道需要重新启动"})
}
func (s *Service) proxy(method, path string) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, uid string) {
		token, err := s.token(r.Context(), uid)
		if err != nil {
			problem(w, err)
			return
		}
		target := path
		if method == "GET" {
			q := url.Values{}
			for _, key := range []string{"page", "limit"} {
				if v := r.URL.Query().Get(key); v != "" {
					q.Set(key, v)
				}
			}
			if len(q) > 0 {
				target += "?" + q.Encode()
			}
		}
		var input any
		if method == "POST" {
			var m map[string]any
			if err = decode(w, r, &m); err != nil {
				problem(w, err)
				return
			}
			input = m
		}
		var out any
		if err = s.api.Do(r.Context(), token, method, target, input, &out); err != nil {
			problem(w, err)
			return
		}
		write(w, out)
	}
}

type asset struct {
	ID       int    `json:"id"`
	Filename string `json:"filename"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Checksum string `json:"checksum"`
	Size     int64  `json:"size"`
	Sources  []struct {
		Source        string `json:"source"`
		Name          string `json:"name"`
		Enabled       bool   `json:"enabled"`
		GlobalEnabled bool   `json:"global_enabled"`
		SortOrder     int    `json:"sort_order"`
	} `json:"available_sources"`
}

func (s *Service) getAssets(ctx context.Context, token string, id int) (map[string]any, []asset, error) {
	var version coreVersion
	if err := s.api.Do(ctx, token, "GET", fmt.Sprintf("/releases/versions/latest?release_id=%d", id), nil, &version); err != nil {
		return nil, nil, err
	}
	var data struct {
		Items []asset `json:"items"`
	}
	err := s.api.Do(ctx, token, "GET", fmt.Sprintf("/releases/versions/%d/assets?release_id=%d&os=linux&arch=%s", version.ID, id, runtime.GOARCH), nil, &data)
	return map[string]any{"version": version, "items": data.Items}, data.Items, err
}
func (s *Service) assets(w http.ResponseWriter, r *http.Request, uid string) {
	id, err := strconv.Atoi(r.URL.Query().Get("release_id"))
	if err != nil || id < 1 {
		problem(w, errors.New("请选择软件"))
		return
	}
	token, err := s.token(r.Context(), uid)
	if err != nil {
		problem(w, err)
		return
	}
	data, _, err := s.getAssets(r.Context(), token, id)
	if err != nil {
		problem(w, err)
		return
	}
	write(w, data)
}
func (s *Service) install(w http.ResponseWriter, r *http.Request, uid string) {
	if !fnos.Current(r.Context()).IsAdmin {
		http.Error(w, "administrator required", 403)
		return
	}
	var input struct {
		Release int    `json:"releaseId"`
		Asset   int    `json:"assetId"`
		Source  string `json:"source"`
	}
	if err := decode(w, r, &input); err != nil {
		problem(w, err)
		return
	}
	if input.Release < 1 || input.Asset < 1 || input.Source == "" {
		problem(w, errors.New("请选择内核文件与下载源"))
		return
	}
	token, err := s.token(r.Context(), uid)
	if err != nil {
		problem(w, err)
		return
	}
	_, items, err := s.getAssets(r.Context(), token, input.Release)
	if err != nil {
		problem(w, err)
		return
	}
	valid := false
	for _, a := range items {
		if a.ID == input.Asset && a.OS == "linux" && a.Arch == runtime.GOARCH && !strings.Contains(strings.ToLower(a.Filename), "frps") {
			for _, source := range a.Sources {
				if source.Source == input.Source {
					valid = true
				}
			}
		}
	}
	if !valid {
		problem(w, errors.New("内核文件或下载源不可用，或与本机 Linux 架构不匹配"))
		return
	}
	s.coreMu.Lock()
	if s.coreTask != nil {
		s.coreMu.Unlock()
		problem(w, errors.New("内核下载任务正在进行，请稍后重试"))
		return
	}
	// Serialize the administrator's explicit installer with automatic setup.
	s.coreTask = &coreTask{uid: uid}
	s.coreMu.Unlock()
	defer func() {
		info := s.core.Info()
		s.coreMu.Lock()
		s.coreState = CoreStatus{Info: info, State: "missing"}
		if info.Installed {
			s.coreState.State = "ready"
		}
		s.coreTask = nil
		s.coreMu.Unlock()
	}()
	var d core.Download
	path := fmt.Sprintf("/releases/download?release_id=%d&asset_id=%d&source=%s", input.Release, input.Asset, url.QueryEscape(input.Source))
	if err = s.api.Do(r.Context(), token, "GET", path, nil, &d); err == nil {
		err = s.core.Install(r.Context(), d)
	}
	if err != nil {
		problem(w, err)
		return
	}
	write(w, s.core.Info())
}
func (s *Service) restore() {
	prefs, err := s.store.AutoStartAccounts(s.ctx)
	if err != nil {
		return
	}
	for uid, names := range prefs {
		lock := s.lock(uid)
		lock.Lock()
		enabled, _ := s.store.BootAutoStart(s.ctx, uid)
		if enabled {
			for _, n := range names {
				if s.ctx.Err() != nil {
					break
				}
				if err := s.startTunnel(s.ctx, uid, n); err != nil {
					slog.Warn("自动恢复隧道失败", "uid", uid, "tunnel", n, "error", err)
				}
			}
		}
		lock.Unlock()
	}
}
