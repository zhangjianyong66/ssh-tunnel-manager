// Package web exposes the local-only SSH manager HTTP API and control page.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"sync"

	sshmanager "github.com/zhangjianyong66/ssh-tunnel-manager/internal/ssh"
	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/sshconfig"
)

type configLoader interface {
	Load(string) (sshconfig.Config, error)
}

type sessionManager interface {
	Connect(context.Context, string, sshmanager.ConnectOptions) (sshmanager.Snapshot, error)
	Disconnect(context.Context, string) (sshmanager.Snapshot, error)
	Snapshot(string) sshmanager.Snapshot
}

// App owns the SSH configuration snapshot and M1 HTTP handlers.
type App struct {
	mu         sync.RWMutex
	config     sshconfig.Config
	configPath string
	loader     configLoader
	sessions   sessionManager
	handler    http.Handler
}

// NewApp loads the initial SSH config and creates the M1 handler.
func NewApp(configPath string, sessions sessionManager) (*App, error) {
	return newApp(configPath, sshconfig.Loader{}, sessions)
}

func newApp(configPath string, loader configLoader, sessions sessionManager) (*App, error) {
	if sessions == nil {
		return nil, errors.New("SSH 会话管理器不能为空")
	}
	app := &App{configPath: configPath, loader: loader, sessions: sessions}
	if err := app.refresh(); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", app.handlePage)
	mux.HandleFunc("GET /api/ssh-hosts", app.handleHosts)
	mux.HandleFunc("POST /api/ssh-hosts/refresh", app.handleRefresh)
	mux.HandleFunc("GET /api/servers/{host}", app.handleServer)
	mux.HandleFunc("POST /api/servers/{host}/connect", app.handleConnect)
	mux.HandleFunc("POST /api/servers/{host}/disconnect", app.handleDisconnect)
	app.handler = mux
	return app, nil
}

// Handler returns the complete M1 control page and API handler.
func (a *App) Handler() http.Handler { return a.handler }

func (a *App) refresh() error {
	config, err := a.loader.Load(a.configPath)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.config = config
	a.mu.Unlock()
	return nil
}

func (a *App) hostExists(host string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, candidate := range a.config.Hosts {
		if candidate.Alias == host {
			return true
		}
	}
	return false
}

func (a *App) handlePage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTemplate.Execute(w, nil); err != nil {
		http.Error(w, "页面渲染失败", http.StatusInternalServerError)
	}
}

func (a *App) handleHosts(w http.ResponseWriter, _ *http.Request) {
	a.mu.RLock()
	config := a.config
	config.Hosts = append([]sshconfig.Host(nil), config.Hosts...)
	config.Diagnostics = append([]sshconfig.Diagnostic(nil), config.Diagnostics...)
	a.mu.RUnlock()
	writeJSON(w, http.StatusOK, config)
}

func (a *App) handleRefresh(w http.ResponseWriter, _ *http.Request) {
	if err := a.refresh(); err != nil {
		writeError(w, http.StatusInternalServerError, "config_read_failed", "读取 SSH 配置失败")
		return
	}
	a.handleHosts(w, nil)
}

func (a *App) handleServer(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if !a.hostExists(host) {
		writeError(w, http.StatusNotFound, "host_not_found", "SSH Host 不存在于当前配置")
		return
	}
	writeJSON(w, http.StatusOK, a.sessions.Snapshot(host))
}

type connectRequest struct {
	Username       string `json:"username"`
	Password       string `json:"password"`
	Passphrase     string `json:"passphrase"`
	SavePassword   bool   `json:"savePassword"`
	SavePassphrase bool   `json:"savePassphrase"`
}

func (a *App) handleConnect(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if !a.hostExists(host) {
		writeError(w, http.StatusNotFound, "host_not_found", "SSH Host 不存在于当前配置")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var request connectRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "连接请求格式无效")
		return
	}
	snapshot, err := a.sessions.Connect(r.Context(), host, sshmanager.ConnectOptions{
		Username:       request.Username,
		Password:       request.Password,
		Passphrase:     request.Passphrase,
		SavePassword:   request.SavePassword,
		SavePassphrase: request.SavePassphrase,
	})
	if err != nil {
		var sshErr *sshmanager.Error
		if errors.As(err, &sshErr) {
			writeError(w, statusForSSHError(sshErr.Code), string(sshErr.Code), sshErr.Message)
			return
		}
		writeError(w, http.StatusInternalServerError, "connect_failed", "启动 SSH 连接失败")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *App) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if !a.hostExists(host) {
		writeError(w, http.StatusNotFound, "host_not_found", "SSH Host 不存在于当前配置")
		return
	}
	snapshot, err := a.sessions.Disconnect(r.Context(), host)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "disconnect_failed", "停止 SSH 连接失败")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func statusForSSHError(code sshmanager.ErrorCode) int {
	switch code {
	case sshmanager.ErrorConfiguration:
		return http.StatusBadRequest
	case sshmanager.ErrorAuthentication, sshmanager.ErrorCredentialRequired:
		return http.StatusUnprocessableEntity
	case sshmanager.ErrorHostKey:
		return http.StatusConflict
	case sshmanager.ErrorTimeout:
		return http.StatusGatewayTimeout
	case sshmanager.ErrorDependency:
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Code: code, Message: message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

var pageTemplate = template.Must(template.New("index").Parse(pageHTML))
