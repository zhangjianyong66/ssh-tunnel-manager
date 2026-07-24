# M1 验证记录

验证日期：2026-07-24

## 自动化门禁

```bash
gofmt -w ./cmd ./internal
go test -race ./...
go vet ./...
go build ./cmd/ssh-tunnel-manager
git diff --check
```

以上命令全部通过。测试覆盖 SSH 配置 Include/循环/过滤、多 Host 并发与独立断开、ControlMaster 就绪和退出命令、askpass 命名管道、Secret Service 成功/不存在/不可用、秘密脱敏、HTTP Host 校验及令牌 Cookie。

## 环境验证

```text
OpenSSH_10.2p1 Ubuntu-2ubuntu3.5
dbus-run-session -- true 通过
```

真实进程冒烟测试使用 `127.0.0.1:19876`：健康检查返回 200，未授权业务 API 返回 401，发送 SIGINT 后进程正常退出。当前会话没有已解锁的桌面 Secret Service，真实凭据写入需在 Ubuntu 桌面会话中复验；自动化测试已通过可注入 D-Bus 后端覆盖其契约。
