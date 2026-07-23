package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"
)

const (
	defaultAddr = "127.0.0.1:8765"
	cookieName  = "stm_token"
)

var pageTemplate = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>SSH 隧道管理器</title>
  <style>
    :root { color-scheme: light; font-family: system-ui, sans-serif; }
    body { max-width: 880px; margin: 48px auto; padding: 0 24px; color: #1f2937; }
    header { border-bottom: 1px solid #e5e7eb; margin-bottom: 28px; }
    h1 { margin-bottom: 8px; }
    .muted { color: #6b7280; }
    .panel { border: 1px solid #e5e7eb; border-radius: 8px; padding: 20px; }
    code { background: #f3f4f6; padding: 2px 5px; border-radius: 4px; }
  </style>
</head>
<body>
  <header>
    <h1>SSH 隧道管理器</h1>
    <p class="muted">本地 Web 控制台基础入口已启动。</p>
  </header>
  <section class="panel">
    <h2>MVP 开发中</h2>
    <p>下一步将读取本机 <code>~/.ssh/config</code>，连接目标服务器并展示 TCP 监听端口。</p>
    <p>当前服务仅监听 <code>127.0.0.1</code>，访问令牌只保存在本次进程内存中。</p>
  </section>
</body>
</html>`))

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

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !authorize(w, r, token) {
			return
		}
		if err := pageTemplate.Execute(w, nil); err != nil {
			log.Printf("渲染页面失败: %v", err)
		}
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	url := fmt.Sprintf("http://%s/?token=%s", *addr, token)
	fmt.Fprintf(os.Stdout, "SSH 隧道管理器已启动\n控制台: %s\n按 Ctrl+C 停止\n", url)
	log.Printf("listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

func authorize(w http.ResponseWriter, r *http.Request, token string) bool {
	if r.URL.Query().Get("token") == token {
		http.SetCookie(w, &http.Cookie{
			Name:     cookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
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
	return strings.HasPrefix(addr, "127.0.0.1:") || strings.HasPrefix(addr, "localhost:") || strings.HasPrefix(addr, "[::1]:")
}
