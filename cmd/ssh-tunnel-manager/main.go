package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/credential"
	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/portdiscovery"
	sshmanager "github.com/zhangjianyong66/ssh-tunnel-manager/internal/ssh"
	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/tunnel"
	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/web"
)

const (
	defaultAddr = "127.0.0.1:8765"
	cookieName  = "stm_token"
)

func main() {
	addr := flag.String("addr", defaultAddr, "本地 Web 服务监听地址（必须是回环地址）")
	flag.Parse()
	if !isLoopbackAddr(*addr) {
		log.Fatalf("拒绝监听非回环地址: %s", *addr)
	}
	token, err := newToken()
	if err != nil {
		log.Fatalf("生成访问令牌失败: %v", err)
	}
	runtimeDir, err := createRuntimeDir()
	if err != nil {
		log.Fatalf("创建 SSH 运行目录失败: %v", err)
	}
	defer os.RemoveAll(runtimeDir)
	manager, err := sshmanager.NewManager(sshmanager.RealRunner{}, credential.NewSecretServiceStore(), runtimeDir)
	if err != nil {
		log.Fatalf("初始化 SSH 管理器失败: %v", err)
	}
	discovery, err := portdiscovery.NewService(manager)
	if err != nil {
		log.Fatalf("初始化端口发现服务失败: %v", err)
	}
	tunnels := tunnel.NewManager(manager)
	configPath := filepath.Join(userHomeDir(), ".ssh", "config")
	app, err := web.NewApp(configPath, manager, discovery, tunnels)
	if err != nil {
		log.Fatalf("初始化 Web 控制台失败: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authorize(w, r, token) {
			return
		}
		app.Handler().ServeHTTP(w, r)
	}))
	server := &http.Server{Addr: *addr, Handler: mux}
	stopContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()
	fmt.Fprintf(os.Stdout, "SSH 隧道管理器已启动\n控制台: http://%s/?token=%s\n按 Ctrl+C 停止\n", *addr, token)
	log.Printf("listening on %s", *addr)

	select {
	case err := <-serverErr:
		if err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP 服务停止: %v", err)
		}
	case <-stopContext.Done():
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownRuntime(shutdownContext, tunnels, discovery, manager, server)
}

type tunnelCloser interface {
	Close(context.Context) error
}

type discoveryCloser interface {
	Close() error
}

type sshCloser interface {
	Close(context.Context) error
}

type httpCloser interface {
	Shutdown(context.Context) error
}

func shutdownRuntime(ctx context.Context, tunnels tunnelCloser, discovery discoveryCloser, manager sshCloser, server httpCloser) {
	if err := tunnels.Close(ctx); err != nil {
		log.Printf("停止 SSH 隧道失败: %v", err)
	}
	if err := discovery.Close(); err != nil {
		log.Printf("停止端口发现服务失败: %v", err)
	}
	if err := manager.Close(ctx); err != nil {
		log.Printf("清理 SSH 连接失败: %v", err)
	}
	if err := server.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		log.Printf("关闭 HTTP 服务失败: %v", err)
	}
}

func authorize(w http.ResponseWriter, r *http.Request, token string) bool {
	if r.URL.Query().Get("token") == token {
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/", http.StatusFound)
			return false
		}
		return true
	}
	cookie, err := r.Cookie(cookieName)
	if err == nil && cookie.Value == token {
		return true
	}
	http.Error(w, "未授权：请使用程序输出的带 token 地址访问", http.StatusUnauthorized)
	return false
}

func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func isLoopbackAddr(addr string) bool {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		return false
	}
	port, err := strconv.Atoi(portText)
	return err == nil && port > 0 && port <= 65535
}

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	return home
}

func createRuntimeDir() (string, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	return os.MkdirTemp(base, "ssh-tunnel-manager-")
}
