# SSH 配置与主连接契约

## Scenario: M1 SSH Host 与 ControlMaster 生命周期

### 1. Scope / Trigger

- 触发范围：修改 `internal/sshconfig`、`internal/credential`、`internal/ssh`、M1 HTTP API，或让 M2/M3 复用现有 SSH 主连接。
- 目标：保持系统 OpenSSH 为唯一协议实现，严格遵循用户配置和主机密钥策略，同时确保秘密与子进程都由明确边界管理。

### 2. Signatures

- 配置：`sshconfig.Loader.Load(path string) (sshconfig.Config, error)`。
- 连接：`Manager.Connect(ctx, host, ConnectOptions) (Snapshot, error)`。
- 断开：`Manager.Disconnect(ctx, host) (Snapshot, error)`。
- 清理：`Manager.Close(ctx) error`。
- 凭据：`Store.Lookup/Save/Delete(context.Context, credential.Ref)`；`Save` 的秘密值只通过函数参数进入实现。
- HTTP：
  - `GET /api/ssh-hosts`
  - `POST /api/ssh-hosts/refresh`
  - `POST /api/servers/{host}/connect`
  - `POST /api/servers/{host}/disconnect`
  - `GET /api/servers/{host}`

### 3. Contracts

- `ConnectOptions` 请求字段：`username`、`password`、`passphrase`、`savePassword`、`savePassphrase`；密码和口令不得出现在响应中。
- `Snapshot` 响应字段：`host`、`status`、`connectedAt`、`lastError`、脱敏 `diagnostic`；`ControlPath` 只供 Go 内部 M2/M3 复用，JSON 中必须隐藏。
- 主连接参数固定使用参数数组：`ssh -M -N -T -o ControlMaster=yes -o ControlPersist=no -o ControlPath=<path> <host>`。
- 只有 `ssh -S <path> -O check <host>` 成功后才标记 `connected`；断开先执行精确的 `-O exit`，再通过保存的进程句柄发信号。
- askpass 环境只能包含 `SSH_ASKPASS=<helper-path>`、`SSH_ASKPASS_REQUIRE=force`、`DISPLAY=stm`，不得包含秘密值。helper 位于 `0700` 临时目录，秘密通过 `0600` 命名管道按 prompt 类型读取。
- Secret Service 适配器通过 Go D-Bus 客户端访问 GNOME Keyring；查询结果只返回内存。缺少会话时不得写普通文件。
- 所有 M1 业务路由仍由入口令牌 Cookie 保护；`/healthz` 是唯一公开路由。

### 4. Validation & Error Matrix

- Host 不在当前配置快照 -> HTTP `404 host_not_found`。
- Host 为空、以 `-` 开头、包含空白/控制字符或 `*?!` -> `configuration`。
- 无凭据且 OpenSSH 返回 `Permission denied` -> `credential_required`。
- 已提供凭据仍认证失败 -> `authentication_failed`。
- 未知或变化主机密钥 -> `host_key_verification`，不得自动接受。
- DNS、拒绝连接、无路由 -> `network_unreachable`。
- ControlMaster 在 15 秒内未就绪 -> `timeout`。
- 缺少 `ssh`、运行目录失败、用户明确保存但 Secret Service 会话不可用 -> `local_dependency_missing`。
- 请求上下文取消 -> `user_cancelled`。

### 5. Good/Base/Bad Cases

- Good：配置含递归 `Include`、重复别名和通配 Host，API 只返回按首次出现顺序去重的显式别名及非致命诊断。
- Base：只使用 ssh-agent 或无口令私钥，连接请求体为 `{}`，不创建 askpass helper。
- Good：输入密码但不保存，秘密只在连接生命周期的内存与命名管道中存在；显式勾选保存才调用 `secret-tool store`。
- Bad：在命令参数、环境变量、普通 JSON、日志或 HTTP 错误中拼入密码、口令或令牌。
- Bad：主进程刚 `Start` 就返回 `connected`，或用 `pkill ssh`/进程名匹配断开连接。

### 6. Tests Required

- `internal/sshconfig`：Include 展开、循环、内联注释、通配/排除过滤、去重和缺失配置。
- `internal/ssh`：同 Host 并发连接只启动一次、不同 Host 可独立管理、重复断开幂等、`-O check`/`-O exit` 参数、错误分类和秘密脱敏。
- askpass：目录 `0700`、脚本不含秘密、密码/口令 prompt 选择正确命名管道、关闭后删除目录。
- `internal/credential`：保存/读取/删除接口；D-Bus 适配器替身覆盖不存在与服务不可用分支。
- `internal/web`：Host 快照校验、未知字段拒绝、稳定状态码、响应不回显秘密。
- 入口：令牌 Cookie 为 HttpOnly/SameSite=Strict，未授权业务 API 返回 401。
- 交付门禁：`gofmt -w ./cmd ./internal`、`go test -race ./...`、`go vet ./...`、`go build ./cmd/ssh-tunnel-manager`。

### 7. Wrong vs Correct

#### Wrong

```go
exec.Command("sh", "-c", "ssh -M -N "+host)
cmd.Env = append(os.Environ(), "SSH_PASSWORD="+password)
```

这会引入 shell 注入面，并让秘密进入环境。

#### Correct

```go
spec := CommandSpec{
    Binary: "ssh",
    Args: []string{"-M", "-N", "-T", "-o", "ControlPath=" + controlPath, host},
    Env: askpass.Env(),
}
process, err := runner.Start(ctx, spec)
```

Host 已在配置快照和连接层校验，秘密只由 askpass 的私有命名管道提供。
