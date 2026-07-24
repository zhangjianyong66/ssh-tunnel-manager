# 可靠性、日志与偏好契约

## Scenario: M4 有界自动重连、近期日志和自动刷新偏好

### 1. Scope / Trigger

- 触发范围：修改 `internal/tunnel` 自动重连或状态、`internal/ssh` 重连凭据/诊断、`internal/preference`、隧道日志 API、端口自动刷新偏好或控制台状态展示。
- 目标：只恢复当前进程中此前运行的隧道，短暂故障可自动恢复，永久故障有界停止；诊断可见但不落盘，偏好持久化但不触发自动连接。

### 2. Signatures

- 隧道构造：`tunnel.NewManager(starter tunnel.Starter, connectors ...tunnel.Connector) *tunnel.Manager`；生产入口必须同时注入 `ssh.Manager` 作为 Starter 和 Connector。
- 连接恢复：`Connector.Snapshot(host)` 与 `Connector.Connect(ctx, host, ssh.ConnectOptions{})`。
- 日志：`Manager.Logs(id string) ([]tunnel.LogEntry, bool)`。
- 偏好：
  - `preference.DefaultPath() (string, error)`
  - `preference.NewFileStore(path) (*FileStore, error)`
  - `Store.AutoRefresh(host) (bool, error)`
  - `Store.SetAutoRefresh(host, enabled) error`
- HTTP：`GET /api/tunnels/{id}/logs`；既有 `PUT /api/servers/{host}/ports/auto-refresh` 同步写偏好。

### 3. Contracts

- 自动重连只由运行中转发的意外退出触发，退避固定为 1、2、4、8、16 秒，单个连续故障周期最多 5 次；稳定运行满 60 秒才恢复完整额度。
- `waiting_reconnect` 暴露 `nextRetryAt`，`reconnecting` 表示已开始一次实际尝试；每次实际尝试累加 `reconnectCount`。成功后更新 `runningSince`、清除当前错误，但不清零累计次数。
- 同一 Host 的并发隧道恢复共享一次 ControlMaster Connect 结果；转发进程只有一个 watch goroutine 调用 `Wait`。`Stop`、`StopHost`、`Close` 必须取消退避、Host 恢复和重建。
- 自动 Connect 使用空 `ConnectOptions`，SSH session 必须在内存中复用首次连接的 credential username，才能读取同一 Secret Service 条目；不得复用未保存的密码/口令。
- 认证、需要交互凭据、主机密钥、配置和依赖错误不可重试；网络、超时、主连接消失和一般进程故障可在额度内重试。
- 每条隧道日志最多 100 条且文本总量最多 64 KiB，只保存在 entry 内存中。普通隧道列表不内联日志；停止后日志 API 返回不存在。
- `Process.Diagnostics()` 进入隧道层前必须已经有界且脱敏；真实转发进程和 ControlMaster 诊断都要隐藏 ControlPath，SSH monitor 继续隐藏密码与口令。
- 偏好文件固定为 `${XDG_CONFIG_HOME:-~/.config}/ssh-tunnel-manager/config.json`，版本 1，只包含 `hosts.<alias>.autoRefresh`。应用目录 `0700`、文件 `0600`，同目录临时文件写入并 `Sync` 后原子重命名。
- 配置缺失默认关闭；损坏、过大、未知字段、非法 Host 或未知版本不覆盖原文件。启动记录安全警告并继续基础功能；程序启动不得因保存值执行连接、探测或恢复隧道。
- 用户手动连接成功后才应用已保存的自动刷新 `true`；显式断开只停止当前循环，不把保存值改成 false。

### 4. Validation & Error Matrix

| 条件 | 行为 / HTTP |
|---|---|
| 自动重连达到第 5 次仍失败 | 隧道 `failed`，安全提示需要手动重试 |
| 认证、凭据、主机密钥、配置或依赖失败 | 首次自动尝试后立即 `failed`，不继续退避 |
| Stop/StopHost/Close 发生在等待或连接中 | 取消后台任务，删除目标 entry，不得稍后重启 |
| 日志 ID 不存在或已停止 | `404 tunnel_not_found` |
| 日志为空 | `200 {"logs":[]}`，不得为 `null` |
| 自动刷新偏好写入失败 | `500 preference_write_failed`，当前刷新状态不改变 |
| 偏好文件损坏或超过 1 MiB | 使用默认关闭并保留原文件，SSH/隧道仍可使用 |
| Host 非法或配置版本未知 | 拒绝读取/写入，不执行远程操作 |

### 5. Good/Base/Bad Cases

- Good：同一 Host 两条隧道随 ControlMaster 退出，一次 Connect 成功后分别以原 ID 重建，累计次数各加 1。
- Good：连接使用用户名 `alice` 保存凭据，自动重连空参数仍按 `alice` 查询密钥环；用户名不进入 SSH 命令或 HTTP 响应。
- Base：转发进程退出但 ControlMaster 仍 connected，只重建转发，不启动新的主连接。
- Base：保存了自动刷新 true，重启后服务器仍 disconnected；用户手动连接成功后才启动刷新循环。
- Bad：每条隧道独立调用 Connect、无限重试、成功一次就立即恢复额度，或 Stop 后仍由计时器重新监听端口。
- Bad：把诊断写入 XDG state、把 ControlPath/凭据放进日志 API，或因偏好损坏让程序启动失败。

### 6. Tests Required

- `internal/tunnel`：单隧道恢复、同 Host 多隧道共享 Connect、5 次耗尽、不可重试短路、60 秒稳定窗口、停止取消、Close 竞态、旧 watch 隔离、日志数量/字节上限和停止清除。
- `internal/ssh`：空参数重连复用 credential username；真实进程诊断隐藏 ControlPath；密码/口令继续脱敏。
- `internal/preference`：缺失默认、读写往返、严格 JSON、过大文件、损坏不覆盖、并发更新、目录/文件权限和原子结果。
- `internal/web`：日志成功/404/非 null；偏好写失败不改变运行态；启动不恢复、手动连接后恢复；新快照字段和稳定错误码。
- 控制台：六种状态中文映射、时长、重连次数、倒计时、按需日志、等待时可停止、桌面/移动无重叠和无控制台错误。
- 全量：`gofmt -w ./cmd ./internal`、`go test -race ./...`、`go vet ./...`、`go build ./cmd/ssh-tunnel-manager`、`git diff --check`。

### 7. Wrong vs Correct

#### Wrong

```go
go func() {
    for {
        time.Sleep(time.Second)
        _, _ = ssh.Connect(context.Background(), host, ssh.ConnectOptions{})
    }
}()
```

这会让每条隧道无限重连，无法被 Stop 取消，也会对同一 Host 重复发起连接。

#### Correct

```go
manager := tunnel.NewManager(sshManager, sshManager)
// entry 使用可取消 context 和固定退避；Manager 按 Host 共享 Connect flight。
// StopHost 先取消 entry/flight，再精确停止转发进程。
```

状态、重连额度、Host singleflight 和日志都由 `internal/tunnel` 单一持有；SSH 层只负责安全恢复 ControlMaster。
