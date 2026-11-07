package main

import (
	"context"
	"errors"
	"flag"
	"github.com/LoliaFRP/loliafrp-fnos/backend/internal/app"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	var socket, listen string
	flag.StringVar(&socket, "socket", "", "fnOS gateway Unix socket")
	flag.StringVar(&listen, "listen", "", "development TCP address (loopback only)")
	flag.Parse()
	opts := app.FromEnvironment()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	service, err := app.New(ctx, opts)
	if err != nil {
		slog.Error("初始化失败", "error", err)
		os.Exit(1)
	}
	defer service.Close()
	var listener net.Listener
	if listen != "" {
		host, _, parseErr := net.SplitHostPort(listen)
		if !opts.Dev || parseErr != nil || (host != "127.0.0.1" && host != "::1") {
			slog.Error("TCP 只允许开发模式下的回环地址")
			os.Exit(1)
		}
		listener, err = net.Listen("tcp", listen)
	} else {
		if socket == "" {
			socket = filepath.Join(opts.AppDest, "app.sock")
		}
		if err = os.MkdirAll(filepath.Dir(socket), 0o755); err != nil {
			slog.Error("创建 Socket 目录失败", "error", err)
			os.Exit(1)
		}
		if st, statErr := os.Lstat(socket); statErr == nil {
			if st.Mode()&os.ModeSocket == 0 {
				slog.Error("Socket 路径被普通文件占用")
				os.Exit(1)
			}
			_ = os.Remove(socket)
		}
		listener, err = net.Listen("unix", socket)
		if err == nil {
			err = os.Chmod(socket, 0o660)
		}
		defer os.Remove(socket)
	}
	if err != nil {
		slog.Error("监听失败", "error", err)
		os.Exit(1)
	}
	defer listener.Close()
	server := &http.Server{Handler: service.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	slog.Info("LoliaFRP manager 已启动")
	if err = server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("服务失败", "error", err)
	}
}
