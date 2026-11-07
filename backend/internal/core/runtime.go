package core

import (
	"bufio"
	"bytes"
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"github.com/BurntSushi/toml"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Status struct {
	Name     string   `json:"name"`
	State    string   `json:"state"`
	PID      int      `json:"pid"`
	Restarts int      `json:"restarts"`
	Error    string   `json:"error,omitempty"`
	Logs     []string `json:"logs"`
}
type instance struct {
	status Status
	cancel context.CancelFunc
	done   chan struct{}
}
type Runtime struct {
	mu                            sync.Mutex
	binaryMu                      sync.RWMutex
	instances                     map[string]*instance
	root                          context.Context
	temp, binary, fallback, cache string
}

func New(ctx context.Context, temp, binary, fallback, cache string) *Runtime {
	return &Runtime{root: ctx, temp: temp, binary: binary, fallback: fallback, cache: cache, instances: map[string]*instance{}}
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (r *Runtime) Binary() string {
	if _, err := os.Stat(r.binary); err == nil {
		return r.binary
	}
	return r.fallback
}
func ValidateBinary(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return errors.New("内核文件不是有效的 Linux ELF 可执行文件")
	}
	defer f.Close()
	expected := elf.EM_X86_64
	if runtime.GOARCH == "arm64" {
		expected = elf.EM_AARCH64
	}
	if f.Machine != expected || (f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN) {
		return errors.New("内核文件架构不匹配")
	}
	return nil
}
func validateConfig(data []byte, cacheDir string) ([]byte, []string, error) {
	var config map[string]any
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, nil, errors.New("服务端配置不是有效 TOML")
	}
	addr, _ := config["serverAddr"].(string)
	port, _ := config["serverPort"].(int64)
	if addr == "" || port < 1 || port > 65535 {
		return nil, nil, errors.New("frpc 配置缺少有效 serverAddr/serverPort")
	}
	// Force logs into bounded process output instead of arbitrary filesystem paths.
	config["log"] = map[string]any{"to": "console", "level": "info", "disablePrintColor": true}
	delete(config, "webServer")
	secrets := []string{}
	if auth, ok := config["auth"].(map[string]any); ok {
		if token, ok := auth["token"].(string); ok && token != "" {
			secrets = append(secrets, token)
		}
	}
	if metadata, ok := config["metadatas"].(map[string]any); ok {
		if token, ok := metadata["token"].(string); ok && token != "" {
			secrets = append(secrets, token)
		}
	}
	if proxies, ok := config["proxies"].([]map[string]any); ok {
		for _, proxy := range proxies {
			if plugin, ok := proxy["plugin"].(map[string]any); ok {
				if tls, ok := plugin["autoTLS"].(map[string]any); ok {
					tls["cacheDir"] = cacheDir
				}
			}
		}
	}
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(config); err != nil {
		return nil, nil, err
	}
	return b.Bytes(), secrets, nil
}
func (r *Runtime) Start(uid, name string, data []byte) error {
	if !safeName.MatchString(uid) || !safeName.MatchString(name) {
		return errors.New("invalid process identifier")
	}
	r.binaryMu.RLock()
	defer r.binaryMu.RUnlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	key := uid + ":" + name
	if old := r.instances[key]; old != nil {
		select {
		case <-old.done:
		default:
			if old.status.State == "stopping" {
				return errors.New("隧道正在停止，请稍后重试")
			}
			return nil
		}
	}
	binary := r.Binary()
	if err := ValidateBinary(binary); err != nil {
		return fmt.Errorf("请先安装兼容的 LoliaFRP 内核：%w", err)
	}
	cacheDir := filepath.Join(r.cache, uid, name)
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return err
	}
	normalized, secret, err := validateConfig(data, cacheDir)
	if err != nil {
		return err
	}
	dir := filepath.Join(r.temp, "runtime", uid, name)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	config := filepath.Join(dir, "active.toml")
	if err = os.WriteFile(config, normalized, 0o600); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(r.root)
	inst := &instance{status: Status{Name: name, State: "starting", Logs: []string{}}, cancel: cancel, done: make(chan struct{})}
	r.instances[key] = inst
	go r.run(ctx, inst, binary, dir, secret)
	return nil
}
func (r *Runtime) run(ctx context.Context, i *instance, binary, dir string, secrets []string) {
	defer func() {
		_ = os.RemoveAll(dir)
		r.mu.Lock()
		i.status.PID = 0
		if ctx.Err() != nil {
			i.status.State = "stopped"
		}
		close(i.done)
		r.mu.Unlock()
	}()
	for attempt := 0; attempt < 5; attempt++ {
		if ctx.Err() != nil {
			return
		}
		cmd := exec.Command(binary, "-c", filepath.Join(dir, "active.toml"))
		cmd.Dir = dir
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
		// Do not pass fnOS API credentials or host-specific environment to the child.
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "LANG=C.UTF-8"}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			r.failed(i, err)
			return
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			r.failed(i, err)
			return
		}
		if err = cmd.Start(); err != nil {
			r.failed(i, err)
			return
		}
		r.mu.Lock()
		i.status.State = "running"
		i.status.PID = cmd.Process.Pid
		i.status.Restarts = attempt
		i.status.Error = ""
		r.mu.Unlock()
		var readers sync.WaitGroup
		readers.Add(2)
		capture := func(reader io.Reader) {
			defer readers.Done()
			scan := bufio.NewScanner(reader)
			scan.Buffer(make([]byte, 4096), 256<<10)
			for scan.Scan() {
				line := scan.Text()
				for _, secret := range secrets {
					line = strings.ReplaceAll(line, secret, "[redacted]")
				}
				if len(line) > 4096 {
					line = line[:4096]
				}
				r.mu.Lock()
				i.status.Logs = append(i.status.Logs, line)
				if len(i.status.Logs) > 300 {
					i.status.Logs = append([]string(nil), i.status.Logs[len(i.status.Logs)-300:]...)
				}
				r.mu.Unlock()
			}
		}
		go capture(stdout)
		go capture(stderr)
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		select {
		case <-ctx.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			select {
			case <-exited:
			case <-time.After(8 * time.Second):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				<-exited
			}
			readers.Wait()
			return
		case err = <-exited:
			readers.Wait()
			if err == nil {
				err = errors.New("内核提前退出")
			}
			r.mu.Lock()
			i.status.PID = 0
			i.status.Error = err.Error()
			i.status.State = "starting"
			r.mu.Unlock()
		}
		if attempt == 4 {
			r.failed(i, errors.New("内核连续退出 5 次，请检查日志后重新启动"))
			return
		}
		timer := time.NewTimer(time.Second << attempt)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (r *Runtime) failed(i *instance, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i.status.State = "failed"
	i.status.Error = err.Error()
}
func (r *Runtime) Stop(ctx context.Context, uid, name string) error {
	r.mu.Lock()
	i := r.instances[uid+":"+name]
	if i == nil {
		r.mu.Unlock()
		return nil
	}
	select {
	case <-i.done:
		r.mu.Unlock()
		return nil
	default:
	}
	i.status.State = "stopping"
	i.cancel()
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-i.done:
		return nil
	}
}
func (r *Runtime) StopUser(ctx context.Context, uid string) error {
	for name := range r.Statuses(uid) {
		if err := r.Stop(ctx, uid, name); err != nil {
			return err
		}
	}
	return nil
}
func (r *Runtime) StopAll(ctx context.Context) {
	r.mu.Lock()
	items := []string{}
	for key := range r.instances {
		items = append(items, key)
	}
	r.mu.Unlock()
	for _, key := range items {
		parts := strings.SplitN(key, ":", 2)
		_ = r.Stop(ctx, parts[0], parts[1])
	}
}
func (r *Runtime) Statuses(uid string) map[string]Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := map[string]Status{}
	for key, i := range r.instances {
		if strings.HasPrefix(key, uid+":") {
			v := i.status
			v.Logs = append([]string{}, v.Logs...)
			result[v.Name] = v
		}
	}
	return result
}
func (r *Runtime) install(fn func() error) error {
	r.binaryMu.Lock()
	defer r.binaryMu.Unlock()
	r.mu.Lock()
	for _, i := range r.instances {
		select {
		case <-i.done:
		default:
			r.mu.Unlock()
			return errors.New("请先停止所有用户的隧道再更新共享内核")
		}
	}
	r.mu.Unlock()
	return fn()
}
