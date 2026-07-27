# 里程碑 1：Host 配置基础

## 目标

建立项目 Host 的单一数据源、合并视图和安全 CRUD，但本里程碑不改变 SSH 连接命令，也不制作完整页面表单。

## 任务

- [x] M1-T1：提取可复用的 XDG 原子文件写入辅助逻辑，保持 `internal/preference` 行为和测试不变。
- [x] M1-T2：新增 `internal/hostconfig` 模型与版本化 `hosts.json` Store，覆盖权限、大小、版本、未知字段、路径和损坏文件保护。
- [x] M1-T3：实现系统/项目 Host Catalog、别名与一层引用校验、安全 OpenSSH 配置渲染，以及 `ssh -G` 有效系统 Host 检查。
- [x] M1-T4：扩展 Web Host API 的合并查询与项目 Host 新增、编辑、删除，加入运行状态和引用门禁；补齐 API 测试。

## 关键文件

- `internal/configfile/*`
- `internal/hostconfig/*`
- `internal/preference/store.go`
- `internal/sshconfig/*`
- `internal/web/app.go`
- `internal/web/app_test.go`
- `cmd/ssh-tunnel-manager/main.go`

## 验证

```bash
gofmt -w ./cmd ./internal
go test -race ./internal/configfile ./internal/preference ./internal/hostconfig ./internal/sshconfig ./internal/web
go vet ./...
go build ./cmd/ssh-tunnel-manager
```

## 回滚点

本里程碑提交前，系统 Host 查询必须仍可在没有 `hosts.json` 或该文件损坏时工作。若 Catalog 无法保持原 API 兼容，回滚 Web 接线，保留独立 Store 测试后重新设计。
