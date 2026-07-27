# 里程碑 2：跳板连接与安全挑战

## 目标

让 SSH Manager 使用项目 Host 配置，先建立跳板 ControlMaster，再通过一层 ProxyJump 复用它连接目标，并完成独立凭据与主机指纹挑战。

## 任务

- [ ] M2-T1：为每个 SSH 会话生成并持有 `0600` 临时 OpenSSH 配置；所有 check、exit、execute 和 forward 调用统一携带 `-F`。
- [ ] M2-T2：实现一层依赖解析、跳板 ControlMaster 复用、依赖目标断开门禁、退出拓扑顺序和自动重连接线。
- [ ] M2-T3：扩展连接请求与 SSH 错误结构，按 `stageHost` 分阶段处理并分别保存跳板/目标密码和私钥口令；覆盖用户名变更与删除凭据清理。
- [ ] M2-T4：扩展 askpass 与错误分类，捕获未知主机指纹、严格匹配用户确认，并永久拒绝已变化密钥；补齐秘密脱敏与并发测试。

## 关键文件

- `internal/ssh/manager.go`
- `internal/ssh/askpass.go`
- `internal/ssh/*_test.go`
- `internal/credential/*`
- `internal/tunnel/manager.go`
- `internal/tunnel/manager_test.go`
- `internal/web/app.go`
- `internal/web/app_test.go`

## 验证

```bash
gofmt -w ./cmd ./internal
go test -race ./internal/ssh ./internal/credential ./internal/tunnel ./internal/web
go test -race ./...
go vet ./...
go build ./cmd/ssh-tunnel-manager
```

增加参数断言：任何项目 Host SSH 调用都使用精确 `-F` 与 `ControlPath`；诊断、请求回显、环境和命令参数均不含测试秘密。

## 回滚点

先保留 M1 的配置管理。如果无法证明 ProxyJump 子进程可靠复用指定跳板 ControlPath，停止实现，不得退化为拼接 ProxyCommand shell 字符串或在一个 askpass 中模糊路由两套秘密。
