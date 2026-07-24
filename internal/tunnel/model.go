// Package tunnel manages local SSH forwarding processes and their lifecycle.
package tunnel

import (
	"context"

	sshmanager "github.com/zhangjianyong66/ssh-tunnel-manager/internal/ssh"
)

// Status is the lifecycle state of a local SSH tunnel.
type Status string

const (
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusStopping Status = "stopping"
	StatusFailed   Status = "failed"
)

// ErrorCode is a stable tunnel failure classification.
type ErrorCode string

const (
	ErrorInvalid              ErrorCode = "invalid_tunnel"
	ErrorServerNotConnected   ErrorCode = "server_not_connected"
	ErrorLocalPortUnavailable ErrorCode = "local_port_unavailable"
	ErrorTimeout              ErrorCode = "tunnel_timeout"
	ErrorCancelled            ErrorCode = "tunnel_cancelled"
	ErrorStartFailed          ErrorCode = "tunnel_start_failed"
	ErrorServiceClosed        ErrorCode = "service_closed"
)

// Error is a user-safe tunnel failure. It never contains raw SSH diagnostics.
type Error struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Snapshot is a process-free representation of a tunnel.
type Snapshot struct {
	ID         string `json:"id"`
	Host       string `json:"host"`
	RemotePort uint16 `json:"remotePort"`
	LocalPort  uint16 `json:"localPort,omitempty"`
	Address    string `json:"address,omitempty"`
	Status     Status `json:"status"`
	LastError  *Error `json:"lastError,omitempty"`
}

// Starter creates a forwarding process through an existing SSH master.
type Starter interface {
	StartLocalForward(context.Context, string, uint16, uint16) (sshmanager.Process, error)
}
