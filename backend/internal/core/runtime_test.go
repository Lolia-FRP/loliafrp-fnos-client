package core

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"github.com/BurntSushi/toml"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConfigRetainsExtensionsAndPersistentTLS(t *testing.T) {
	config := []byte(`serverAddr="node.example.com"
serverPort=7000
dnsServer="https://dns.alidns.com/dns-query"
[metadatas]
token="sensitive"
[[proxies]]
name="test"
type="https"
httpRedirect=true
[proxies.plugin]
type="https2http"
[proxies.plugin.autoTLS]
enable=true
cacheDir="./.autotls-cache"
`)
	b, secrets, err := validateConfig(config, "/persistent/tls")
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err = toml.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "/persistent/tls") || !strings.Contains(string(b), "dnsServer") || !strings.Contains(string(b), "httpRedirect") {
		t.Fatalf("extensions lost or cache not rewritten: %s", b)
	}
	if len(secrets) != 1 || secrets[0] != "sensitive" {
		t.Fatal("metadata token redaction missing")
	}
}
func TestArchiveExtractionIgnoresOtherFiles(t *testing.T) {
	archive, err := os.CreateTemp(t.TempDir(), "archive")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	gz := gzip.NewWriter(archive)
	tw := tar.NewWriter(gz)
	for _, file := range []struct{ name, body string }{{"../../outside", "bad"}, {"frp/frps", "server"}, {"frp/frpc", "client"}} {
		if err = tw.WriteHeader(&tar.Header{Name: file.name, Mode: 0o755, Size: int64(len(file.body))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(file.body))
	}
	tw.Close()
	gz.Close()
	archive.Seek(0, 0)
	var out bytes.Buffer
	if err = extract(archive, "release.tar.gz", &out); err != nil || out.String() != "client" {
		t.Fatalf("extraction: %q %v", out.String(), err)
	}
}
func TestConcurrentStopAndUserIsolation(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	binary := filepath.Join(root, "frpc")
	code := `package main
import("fmt";"os";"os/signal";"syscall")
func main(){fmt.Println("ready");ch:=make(chan os.Signal,1);signal.Notify(ch,syscall.SIGTERM);<-ch}
`
	if err := os.WriteFile(source, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", binary, source)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", b, err)
	}
	r := New(context.Background(), filepath.Join(root, "tmp"), filepath.Join(root, "managed"), binary, filepath.Join(root, "cache"))
	config := []byte("serverAddr='example.com'\nserverPort=7000\n")
	if err := r.Start("1000", "tunnel", config); err != nil {
		t.Fatal(err)
	}
	if err := r.Start("1001", "tunnel", config); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer r.StopAll(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r.Statuses("1000")["tunnel"].State == "running" && r.Statuses("1001")["tunnel"].State == "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := r.install(func() error { t.Error("install ran with active processes"); return nil }); err == nil {
		t.Error("installation should be blocked")
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Stop(ctx, "1000", "tunnel"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if r.Statuses("1001")["tunnel"].State != "running" {
		t.Error("stopping one user affected another")
	}
	if _, err := os.Stat(filepath.Join(root, "tmp/runtime/1000/tunnel/active.toml")); !os.IsNotExist(err) {
		t.Error("ephemeral configuration was not removed")
	}
}
