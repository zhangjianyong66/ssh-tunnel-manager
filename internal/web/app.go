// Package web exposes the local-only SSH manager HTTP API and control page.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"sync"

	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/portdiscovery"
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

type portDiscovery interface {
	Snapshot(string) portdiscovery.Snapshot
	Refresh(context.Context, string) (portdiscovery.Snapshot, error)
	SetAutoRefresh(string, bool) (portdiscovery.Snapshot, error)
}

// App owns the SSH configuration snapshot and control-page HTTP handlers.
type App struct {
	mu         sync.RWMutex
	config     sshconfig.Config
	configPath string
	loader     configLoader
	sessions   sessionManager
	ports      portDiscovery
	handler    http.Handler
}

// NewApp loads the initial SSH config and creates the control-page handler.
func NewApp(configPath string, sessions sessionManager, ports portDiscovery) (*App, error) {
	return newApp(configPath, sshconfig.Loader{}, sessions, ports)
}

func newApp(configPath string, loader configLoader, sessions sessionManager, ports portDiscovery) (*App, error) {
	if sessions == nil {
		return nil, errors.New("SSH 会话管理器不能为空")
	}
	if ports == nil {
		return nil, errors.New("端口发现服务不能为空")
	}
	app := &App{configPath: configPath, loader: loader, sessions: sessions, ports: ports}
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
	mux.HandleFunc("GET /api/servers/{host}/ports", app.handlePorts)
	mux.HandleFunc("POST /api/servers/{host}/ports/refresh", app.handlePortsRefresh)
	mux.HandleFunc("PUT /api/servers/{host}/ports/auto-refresh", app.handleAutoRefresh)
	app.handler = mux
	return app, nil
}

// Handler returns the complete control page and API handler.
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
	if a.ports != nil {
		_, _ = a.ports.SetAutoRefresh(host, false)
	}
	snapshot, err := a.sessions.Disconnect(r.Context(), host)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "disconnect_failed", "停止 SSH 连接失败")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *App) handlePorts(w http.ResponseWriter, r *http.Request) {
	if !a.requireHost(w, r.PathValue("host")) {
		return
	}
	writeJSON(w, http.StatusOK, a.ports.Snapshot(r.PathValue("host")))
}

func (a *App) handlePortsRefresh(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if !a.requireHost(w, host) {
		return
	}
	if a.sessions.Snapshot(host).Status != sshmanager.StatusConnected {
		writeError(w, http.StatusConflict, string(portdiscovery.ErrorServerNotConnected), "SSH 服务器尚未连接")
		return
	}
	snapshot, err := a.ports.Refresh(r.Context(), host)
	if err != nil {
		writeDiscoveryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

type autoRefreshRequest struct {
	Enabled *bool `json:"enabled"`
}

func (a *App) handleAutoRefresh(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if !a.requireHost(w, host) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var request autoRefreshRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF || request.Enabled == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "自动刷新请求格式无效")
		return
	}
	if a.sessions.Snapshot(host).Status != sshmanager.StatusConnected {
		writeError(w, http.StatusConflict, string(portdiscovery.ErrorServerNotConnected), "SSH 服务器尚未连接")
		return
	}
	snapshot, err := a.ports.SetAutoRefresh(host, *request.Enabled)
	if err != nil {
		writeDiscoveryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *App) requireHost(w http.ResponseWriter, host string) bool {
	if a.hostExists(host) {
		return true
	}
	writeError(w, http.StatusNotFound, "host_not_found", "SSH Host 不存在于当前配置")
	return false
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

func writeDiscoveryError(w http.ResponseWriter, err error) {
	var discoveryErr *portdiscovery.Error
	if !errors.As(err, &discoveryErr) {
		writeError(w, http.StatusBadGateway, string(portdiscovery.ErrorFailed), "远程端口探测失败")
		return
	}
	switch discoveryErr.Code {
	case portdiscovery.ErrorServerNotConnected:
		writeError(w, http.StatusConflict, string(discoveryErr.Code), discoveryErr.Message)
	case portdiscovery.ErrorTimeout:
		writeError(w, http.StatusGatewayTimeout, string(discoveryErr.Code), discoveryErr.Message)
	case portdiscovery.ErrorCancelled:
		writeError(w, http.StatusRequestTimeout, string(discoveryErr.Code), discoveryErr.Message)
	case portdiscovery.ErrorClosed:
		writeError(w, http.StatusServiceUnavailable, string(discoveryErr.Code), discoveryErr.Message)
	default:
		writeError(w, http.StatusBadGateway, string(portdiscovery.ErrorFailed), "远程端口探测失败")
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
