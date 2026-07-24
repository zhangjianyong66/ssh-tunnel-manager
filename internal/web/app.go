// Package web exposes the local-only SSH manager HTTP API and control page.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/portdiscovery"
	sshmanager "github.com/zhangjianyong66/ssh-tunnel-manager/internal/ssh"
	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/sshconfig"
	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/tunnel"
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

type tunnelService interface {
	Create(context.Context, string, uint16) (tunnel.Snapshot, error)
	List() []tunnel.Snapshot
	Logs(string) ([]tunnel.LogEntry, bool)
	Stop(context.Context, string) error
	StopHost(context.Context, string) error
}

type preferenceStore interface {
	AutoRefresh(string) (bool, error)
	SetAutoRefresh(string, bool) error
}

// App owns the SSH configuration snapshot and control-page HTTP handlers.
type App struct {
	mu          sync.RWMutex
	config      sshconfig.Config
	configPath  string
	loader      configLoader
	sessions    sessionManager
	ports       portDiscovery
	tunnels     tunnelService
	preferences preferenceStore
	handler     http.Handler
}

// NewApp loads the initial SSH config and creates the control-page handler.
func NewApp(configPath string, sessions sessionManager, ports portDiscovery, tunnels tunnelService, preferences ...preferenceStore) (*App, error) {
	return newApp(configPath, sshconfig.Loader{}, sessions, ports, tunnels, preferences...)
}

func newApp(configPath string, loader configLoader, sessions sessionManager, ports portDiscovery, tunnels tunnelService, preferences ...preferenceStore) (*App, error) {
	if sessions == nil {
		return nil, errors.New("SSH 会话管理器不能为空")
	}
	if ports == nil {
		return nil, errors.New("端口发现服务不能为空")
	}
	if tunnels == nil {
		return nil, errors.New("隧道服务不能为空")
	}
	app := &App{configPath: configPath, loader: loader, sessions: sessions, ports: ports, tunnels: tunnels}
	if len(preferences) > 0 {
		app.preferences = preferences[0]
	}
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
	mux.HandleFunc("POST /api/tunnels", app.handleCreateTunnel)
	mux.HandleFunc("GET /api/tunnels", app.handleTunnels)
	mux.HandleFunc("GET /api/tunnels/{id}/logs", app.handleTunnelLogs)
	mux.HandleFunc("DELETE /api/tunnels/{id}", app.handleStopTunnel)
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
	if a.preferences != nil {
		if enabled, preferenceErr := a.preferences.AutoRefresh(host); preferenceErr == nil && enabled {
			_, _ = a.ports.SetAutoRefresh(host, true)
		}
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *App) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if !a.hostExists(host) {
		writeError(w, http.StatusNotFound, "host_not_found", "SSH Host 不存在于当前配置")
		return
	}
	tunnelErr := a.tunnels.StopHost(r.Context(), host)
	_, _ = a.ports.SetAutoRefresh(host, false)
	snapshot, err := a.sessions.Disconnect(r.Context(), host)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "disconnect_failed", "停止 SSH 连接失败")
		return
	}
	if tunnelErr != nil {
		writeTunnelError(w, tunnelErr)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

type createTunnelRequest struct {
	Host       string `json:"host"`
	RemotePort *int   `json:"remotePort"`
}

func (a *App) handleCreateTunnel(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var request createTunnelRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF || strings.TrimSpace(request.Host) == "" || request.RemotePort == nil || *request.RemotePort < 1 || *request.RemotePort > 65535 {
		writeError(w, http.StatusBadRequest, "invalid_request", "隧道创建请求格式无效")
		return
	}
	if !a.requireHost(w, request.Host) {
		return
	}
	if a.sessions.Snapshot(request.Host).Status != sshmanager.StatusConnected {
		writeError(w, http.StatusConflict, string(tunnel.ErrorServerNotConnected), "SSH 服务器尚未连接")
		return
	}
	snapshot, err := a.tunnels.Create(r.Context(), request.Host, uint16(*request.RemotePort))
	if err != nil {
		writeTunnelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *App) handleTunnels(w http.ResponseWriter, _ *http.Request) {
	tunnels := a.tunnels.List()
	if tunnels == nil {
		tunnels = []tunnel.Snapshot{}
	}
	writeJSON(w, http.StatusOK, struct {
		Tunnels []tunnel.Snapshot `json:"tunnels"`
	}{Tunnels: tunnels})
}

func (a *App) handleStopTunnel(w http.ResponseWriter, r *http.Request) {
	if err := a.tunnels.Stop(r.Context(), r.PathValue("id")); err != nil {
		writeTunnelError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleTunnelLogs(w http.ResponseWriter, r *http.Request) {
	logs, ok := a.tunnels.Logs(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "tunnel_not_found", "SSH 隧道不存在")
		return
	}
	if logs == nil {
		logs = []tunnel.LogEntry{}
	}
	writeJSON(w, http.StatusOK, struct {
		Logs []tunnel.LogEntry `json:"logs"`
	}{Logs: logs})
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
	if a.preferences != nil {
		if err := a.preferences.SetAutoRefresh(host, *request.Enabled); err != nil {
			writeError(w, http.StatusInternalServerError, "preference_write_failed", "保存自动刷新设置失败")
			return
		}
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

func writeTunnelError(w http.ResponseWriter, err error) {
	var tunnelErr *tunnel.Error
	if !errors.As(err, &tunnelErr) {
		writeError(w, http.StatusBadGateway, string(tunnel.ErrorStartFailed), "SSH 隧道操作失败")
		return
	}
	switch tunnelErr.Code {
	case tunnel.ErrorInvalid:
		writeError(w, http.StatusBadRequest, string(tunnelErr.Code), "隧道目标无效")
	case tunnel.ErrorServerNotConnected:
		writeError(w, http.StatusConflict, string(tunnelErr.Code), "SSH 服务器尚未连接")
	case tunnel.ErrorLocalPortUnavailable:
		writeError(w, http.StatusConflict, string(tunnelErr.Code), "没有可用的本地回环端口")
	case tunnel.ErrorTimeout:
		writeError(w, http.StatusGatewayTimeout, string(tunnelErr.Code), "SSH 隧道操作超时")
	case tunnel.ErrorCancelled:
		writeError(w, http.StatusRequestTimeout, string(tunnelErr.Code), "用户取消了隧道操作")
	case tunnel.ErrorServiceClosed:
		writeError(w, http.StatusServiceUnavailable, string(tunnelErr.Code), "隧道服务已关闭")
	default:
		writeError(w, http.StatusBadGateway, string(tunnel.ErrorStartFailed), "SSH 隧道操作失败")
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
