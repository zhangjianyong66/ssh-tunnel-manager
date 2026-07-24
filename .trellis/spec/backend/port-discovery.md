# 远程端口发现契约

## Scenario: M2 复用 ControlMaster 探测 TCP 监听端口

### 1. Scope / Trigger

- 触发范围：修改 `internal/ssh.Manager.Execute`、`internal/portdiscovery`、端口发现 HTTP API、控制台端口列表，或让 M3 消费 M2 端口快照。
- 目标：只通过已经就绪的 OpenSSH ControlMaster 执行受控 `ss` 命令，向页面提供稳定端口快照，同时保证并发刷新、失败保留和后台生命周期可验证。

### 2. Signatures

- SSH 受控执行：`Manager.Execute(ctx context.Context, host string, command []string) (ssh.CommandOutput, error)`。
- 发现服务：
  - `portdiscovery.NewService(executor) (*Service, error)`
  - `Service.Snapshot(host string) portdiscovery.Snapshot`
  - `Service.Refresh(ctx, host) (portdiscovery.Snapshot, error)`
  - `Service.SetAutoRefresh(host, enabled) (portdiscovery.Snapshot, error)`
  - `Service.Close() error`
- HTTP：
  - `GET /api/servers/{host}/ports`
  - `POST /api/servers/{host}/ports/refresh`
  - `PUT /api/servers/{host}/ports/auto-refresh`

### 3. Contracts

- SSH 参数固定以数组构造：`ssh -S <control-path> -T -o BatchMode=yes -- <host> ss -ltnp`。`--` 必须位于 Host 前，只用于结束本地 OpenSSH 选项解析；远程命令 token 拒绝空值、控制字符、空白和 shell 元字符。
- `Execute` 只允许状态为 `connected` 且 ControlPath 非空的 Host。执行期间持有该 Host 的操作锁，使断开操作等待有界远程命令结束；标准输出和错误各最多捕获 1 MiB。
- 首选 `ss -ltnp`。该命令失败时回退一次 `ss -ltn`；首选结果非空但全部缺少进程信息时也回退。超时或取消不回退；空监听结果直接视为成功。
- `Port` JSON 只包含 `number` 和可选 `process`；监听 `Address` 供 Go 内部后续能力使用，JSON 中隐藏。端口必须在 `1..65535`，同端口去重并按升序返回，优先保留带进程名的记录。
- `Snapshot` 字段：`host`、非空数组 `ports`、可选 `refreshedAt`、`autoRefresh`、`refreshing`、可选安全 `diagnostics`、可选 `lastError`。诊断只描述行号和错误类别，不回显远程原始行。
- 同一 Host 的并发刷新共享一个独立 flight 结果，不重复执行远程命令；不同 Host 可并行。失败保留上次成功的 `ports` 和 `refreshedAt`，超时等失败更新 `lastError`；用户取消和服务关闭不覆盖原 `lastError`。
- 自动刷新默认关闭，启用后固定每 10 秒执行一次；重复启用/关闭幂等。SSH 断开时自动循环停止，显式断开前 Web 层主动关闭循环；程序退出先 `Service.Close()`，再关闭 SSH 管理器。
- 自动刷新请求体严格为 `{ "enabled": <bool> }`，大小上限 4 KiB，`enabled` 必须显式存在，并拒绝未知字段和尾随 JSON。所有端口业务路由继续由入口令牌 Cookie 保护。

### 4. Validation & Error Matrix

- Host 不在当前 SSH 配置快照 -> HTTP `404 host_not_found`。
- Host 存在但 SSH 未连接 -> HTTP `409 server_not_connected`，不执行 `ss`。
- 远程探测超时 -> HTTP `504 discovery_timeout`，保留上次成功快照并记录 `lastError`。
- 请求取消 -> `discovery_cancelled`；不写入 `lastError`，若仍可写响应则使用 HTTP `408`。
- SSH/远程 `ss` 两轮均失败或输出执行失败 -> HTTP `502 discovery_failed`，不得把 stderr 原文返回浏览器。
- 服务已经关闭 -> HTTP `503 service_closed`。
- 自动刷新请求缺少 `enabled`、JSON 语法错误、包含未知字段或尾随值 -> HTTP `400 invalid_request`。
- `ss` 单行端口缺失、为 0、超过 65535 或格式无法识别 -> 跳过该行并添加非致命安全诊断，其他有效行仍成功。

### 5. Good/Base/Bad Cases

- Good：`ss -ltnp` 同时输出 IPv4、`*:<port>`、`[::]:<port>` 和重复端口，解析器按端口去重排序，并保留可见进程名。
- Base：远程没有 TCP 监听项，第一次命令成功返回空列表，不为获取不存在的进程信息再执行回退。
- Good：多个浏览器请求同时刷新同一 Host，只启动一个远程命令，等待者读取该 flight 的同一快照和错误；另一 Host 不受阻塞。
- Good：一次刷新失败后页面仍显示旧端口和旧 `refreshedAt`，同时显示安全 `lastError`。
- Bad：把远程完整输出、ControlPath、密码、口令或令牌放进端口 JSON、诊断或日志。
- Bad：自动刷新 goroutine 在 SSH 断开或服务关闭后继续执行，或页面关闭时擅自改变服务端刷新状态。

### 6. Tests Required

- `internal/ssh`：连接后 `Execute` 参数中 `--`/Host/命令顺序准确；未连接和非法命令被拒绝；输出不进入连接快照。
- 解析器：IPv4/IPv6/通配地址、端口边界、重复端口、排序、进程名优先、畸形行和诊断上限。
- 服务：首选成功、无进程回退、两轮失败、超时不回退、失败保留旧快照、刷新中状态、同 Host singleflight、跨 Host 并行。
- 自动刷新：10 秒默认值由构造契约固定；测试使用短注入间隔覆盖幂等启停、关闭后不再执行、SSH 断开自动停止和 `Close` 取消在途命令。
- Web：GET/POST/PUT 路由、未知 Host、未连接、严格 JSON、超时/失败状态码以及内部错误不回显。
- 全量门禁：`gofmt -w ./cmd ./internal`、`go test -race ./...`、`go vet ./...`、`go build ./cmd/ssh-tunnel-manager`、`git diff --check`。

### 7. Wrong vs Correct

#### Wrong

```go
// Host 后的 -- 会成为远程命令的一部分；任意字符串还可能进入远程 shell。
args := []string{"-S", controlPath, host, "--", userCommand}

// 等待者醒来后读取共享字段，可能读到下一轮刷新覆盖的结果。
<-state.done
return state.snapshot, state.operationError
```

#### Correct

```go
args := []string{"-S", controlPath, "-T", "-o", "BatchMode=yes", "--", host}
args = append(args, validatedCommandTokens...)

flight := state.flight
<-flight.done
return cloneSnapshot(flight.snapshot), flight.err
```

`--` 终止本地选项解析，受控 token 防止远程命令注入；每个 flight 保存自己的最终结果，避免下一轮刷新污染等待者。
