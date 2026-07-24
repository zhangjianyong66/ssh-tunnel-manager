# M1 实施计划：SSH 配置与连接

## 顺序清单

1. 将入口拆分为 `internal/sshconfig`、`internal/ssh`、`internal/credential` 和 HTTP handler，保留现有回环认证行为。
2. 实现 SSH 配置读取：默认路径、Include 展开、显式 Host 过滤、去重、来源和解析诊断；补充纯单元测试。
3. 定义连接/凭据/诊断接口和状态机，先用可控的 fake runner、fake credential store 覆盖并发、幂等和清理行为。
4. 实现 Linux Secret Service 适配器与最小权限 D-Bus 调用；增加不可用服务和保存/读取/删除测试替身。
5. 实现 askpass helper、秘密生命周期和 SSH 参数数组构造；测试命令行、环境、日志中不出现秘密。
6. 实现长期 ControlMaster 主连接、连接状态和有界断开清理；使用 fake ssh runner 覆盖成功、认证失败、主机密钥失败、超时和进程异常退出。
7. 接入 M1 HTTP API 和最小页面状态，验证业务路由认证、Host 校验、稳定错误码和幂等断开。
8. 更新 README/架构文档，记录配置解析、密钥环依赖、ControlPath 运行时目录和严格主机密钥策略。

## 验证命令

```bash
gofmt -w ./cmd ./internal
go test ./...
go vet ./...
go build ./cmd/ssh-tunnel-manager
```

需要 Linux 集成环境时，额外验证：

```bash
ssh -V
dbus-run-session -- true
```

不得在测试或日志中输出真实密码、私钥口令、访问令牌或完整凭据。

## 风险与回滚点

- 配置解析：先合并纯函数解析和测试，再接入文件系统；Include 循环或权限错误不能导致进程崩溃。
- Secret Service：外部 D-Bus 依赖是主要环境风险；接口和 fake 必须先稳定，适配器失败只影响需要秘密的连接。
- ControlMaster：socket 路径过长、进程退出和权限是主要风险；运行时目录使用随机名并在所有退出路径清理。
- HTTP 接入：若页面改造超出 M1，保留 API/状态层，页面只提供最小连接列表，避免把 M2/M3 UI 提前耦合。

## 开始实现前的复核门槛

- [x] PRD 中所有开放问题已关闭，且与本设计一致。
- [x] `internal` 包边界和秘密不落盘约束已获得审阅。
- [x] 确认 M1 完成后才执行 `task.py start`，不与 M2–M5 并行修改同一连接层。

## 完成记录

- [x] 8 个实施步骤均已完成。
- [x] 竞态测试、格式化、单元测试、静态检查和构建通过。
- [x] 本地真实进程冒烟测试验证 `/healthz` 为 200、未授权业务 API 为 401、退出信号可正常清理。
- [x] Secret Service 使用内嵌 D-Bus 客户端；当前环境未提供已解锁的桌面密钥环，使用接口替身覆盖成功、不存在和服务不可用分支。
