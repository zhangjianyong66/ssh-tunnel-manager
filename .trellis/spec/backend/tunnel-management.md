# 本地隧道管理契约

## Scenario: M3 复用 ControlMaster 管理本地端口转发

### 1. Scope / Trigger

- 触发范围：修改 `internal/ssh.Manager.StartLocalForward`、`internal/tunnel`、隧道 HTTP API、控制台隧道操作、Host 断开或程序退出顺序。
- 目标：通过系统 OpenSSH 和既有 ControlMaster 创建仅监听 `127.0.0.1` 的精确可管理转发，同时保证端口分配、并发幂等、错误脱敏和进程清理可验证。
- M3 只负责基础 `starting`、`running`、`stopping`、`failed` 状态；自动重连、退避、运行时长、重连次数和日志属于 M4。

### 2. Signatures

- SSH 转发：`Manager.StartLocalForward(ctx context.Context, host string, localPort, remotePort uint16) (Process, error)`。
- 隧道管理：
  - `tunnel.NewManager(starter tunnel.Starter) *tunnel.Manager`
  - `Manager.Create(context.Context, string, uint16) (tunnel.Snapshot, error)`
  - `Manager.List() []tunnel.Snapshot`
  - `Manager.Stop(context.Context, string) error`
  - `Manager.StopHost(context.Context, string) error`
  - `Manager.Close(context.Context) error`
- HTTP：
  - `POST /api/tunnels`
  - `GET /api/tunnels`
  - `DELETE /api/tunnels/{id}`

### 3. Contracts

- `POST` 请求严格为 `{ "host": string, "remotePort": int }`，正文最多 4 KiB；拒绝未知字段、尾随 JSON、空 Host、缺失端口和 `1..65535` 之外的端口。
- `Snapshot` 只包含 `id`、`host`、`remotePort`、可选 `localPort`/`address`、`status` 和可选安全 `lastError`。不得包含 ControlPath、PID、进程对象或原始 SSH 输出。
- SSH 参数固定为数组：`ssh -S <control-path> -N -T -o BatchMode=yes -o ExitOnForwardFailure=yes -L 127.0.0.1:<local>:127.0.0.1:<remote> -- <host>`。转发前后都执行 `ssh -S <control-path> -O check <host>`；后置检查失败时精确清理已启动进程。
- 本地端口先尝试远程同号，只用 `tcp4` 和 `127.0.0.1` 预检；冲突时申请其他回环端口。进程内预留集合防止并发重复分配，预检与 OpenSSH 绑定之间的外部竞争最多有界重试 5 次。
- `host + remotePort` 是幂等键。同目标并发创建共用一个条目锁并返回同一随机 ID；不同目标不得共享长时间全局锁。
- API 只有在本地监听探测成功且转发进程未退出后才返回 `running`。探测只确认本地 SSH 监听，不保证最终远程服务接受连接。
- 每个转发进程只有 `watchProcess` 创建的监控 goroutine 调用一次 `Wait`。停止只对保存的句柄发送 `os.Interrupt`，有界等待后升级为 `os.Kill`；Go 1.22 中复用停止计时器前必须 `Stop` 并按需排空通道。
- 意外退出转为 `failed` 并保留条目；再次 `Create` 在相同 ID 上重建。`Stop` 对未知 ID 幂等成功，成功停止后从索引移除并释放端口。
- 显式断开顺序为 `StopHost -> SetAutoRefresh(false) -> SSH Disconnect`，即使隧道停止失败也继续后两步。程序退出顺序为 `tunnels.Close -> discovery.Close -> SSH Close -> HTTP Shutdown`。
- 页面每轮只调用一次 `GET /api/tunnels`，按 Host 和远程端口映射到端口表并同时渲染独立总览；页面刷新、关闭和 `pagehide` 不调用停止 API。

### 4. Validation & Error Matrix

| 条件 | 隧道错误码 | HTTP |
|---|---|---:|
| 请求结构、Host 或端口无效 | `invalid_tunnel`；Web 格式错误为 `invalid_request` | 400 |
| Host 不在当前 SSH 配置 | `host_not_found` | 404 |
| ControlMaster 未连接或前后检查失败 | `server_not_connected` | 409 |
| 回环端口耗尽或绑定竞争重试耗尽 | `local_port_unavailable` | 409 |
| 创建或就绪探测超时 | `tunnel_timeout` | 504 |
| 请求上下文取消 | `tunnel_cancelled` | 408 |
| 其他 OpenSSH 启动或意外退出 | `tunnel_start_failed` | 502 |
| 管理器已关闭 | `service_closed` | 503 |

- 只有诊断含明确本地绑定冲突（如 `address already in use`）时才重试；认证失败、依赖缺失和主连接消失不得伪装为端口冲突。
- HTTP 响应使用固定中文安全消息，不回显底层 `err.Error()`、ControlPath 或 stderr。`GET` 空结果必须编码为 `{ "tunnels": [] }`，不得返回 `null`。

### 5. Good/Base/Bad Cases

- Good：远程 8080 且本地同号空闲，返回 `127.0.0.1:8080`；另一 Host 同时代理 8080 时自动使用其他回环端口。
- Good：12 个并发请求创建同一目标，只启动一个 SSH 转发进程并返回同一 ID；失败条目重试后 ID 不变。
- Base：远程端口已从最新发现快照消失，独立隧道总览仍显示并允许停止。
- Bad：使用 `0.0.0.0:<port>`、接受调用方自定义目标地址、拼接 shell、执行 `pkill ssh` 或按进程名模糊停止。
- Bad：停止路径再次调用 `Process.Wait`，或在持有全局管理器锁时等待子进程退出。

### 6. Tests Required

- `internal/ssh`：断言完整转发参数、前后 ControlMaster check、断开等待 Host 操作临界区、后置检查失败精确清理，以及长生命周期 stderr 有界。
- `internal/tunnel`：断言同号优先、冲突分配、外部绑定竞争重试、并发幂等、失败保留/重建、取消/超时、单条停止、`StopHost`、`Close`、唯一 `Wait` 和取消上下文后的 Kill 升级。
- `internal/web`：断言严格 4 KiB JSON、Host/连接校验、全部错误映射、非 `null` 空列表、幂等 DELETE、断开清理顺序和内部诊断不泄漏。
- 控制台模板：断言代理、重试、复制、HTTP 打开、停止、独立总览、每轮单次隧道列表请求和不存在页面关闭清理钩子。
- 入口：断言退出清理顺序；运行时验证同号、端口占用、多 Host/多端口、停止释放、`/healthz`、未认证 401、认证后 API 和 SIGINT 端口释放。
- 交付门禁：`gofmt -w ./cmd ./internal`、`go test -race ./...`、`go vet ./...`、`go build ./cmd/ssh-tunnel-manager`、`git diff --check`。

### 7. Wrong vs Correct

#### Wrong

```go
exec.Command("sh", "-c", "ssh -N -L 0.0.0.0:"+port+":"+target+" "+host)
exec.Command("pkill", "ssh").Run()
```

这会引入 shell 注入、非回环暴露和对用户其他 SSH 会话的误杀风险。

#### Correct

```go
args := []string{
    "-S", controlPath,
    "-N", "-T",
    "-o", "BatchMode=yes",
    "-o", "ExitOnForwardFailure=yes",
    "-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", localPort, remotePort),
    "--", host,
}
process, err := runner.Start(ctx, ssh.CommandSpec{Binary: "ssh", Args: args})
```

调用方保存返回的精确 `Process`，由唯一监控 goroutine `Wait`，停止时只向该句柄发信号。
