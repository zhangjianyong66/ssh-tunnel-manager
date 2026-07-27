# 里程碑 4：集成与收尾

## 目标

验证真实 FRP + Mac 跳板 + Ubuntu 场景，完成全量质量门禁、文档、规范和回滚说明。

## 任务

- [ ] M4-T1：在页面建立 `mac_home` 与 `ssh_ubuntu_home`，验证独立凭据、首次指纹、连接、端口发现、本地隧道、断开和重连行为。
- [ ] M4-T2：执行全量竞态测试、静态检查和构建；检查配置权限、损坏文件、Secret Service 不可用、系统 Host 回归及程序退出清理。
- [ ] M4-T3：更新 README、产品设计、架构、路线图、后端规范与根 `AGENTS.md`，记录新的目录、XDG 文件、API 和运行约定。
- [ ] M4-T4：完成最终代码审查、中文提交、Trellis 会话记录与任务归档；保留每个里程碑独立回滚点。

## 关键文件

- `README.md`
- `docs/product-design.md`
- `docs/architecture.md`
- `docs/roadmap.md`
- `.trellis/spec/backend/*`
- `AGENTS.md`

## 验证

```bash
gofmt -w ./cmd ./internal
go test -race ./...
go vet ./...
go build ./cmd/ssh-tunnel-manager
```

手工验收必须记录：实际连接是否成功、哪个阶段需要凭据或指纹、Ubuntu 端口能否发现、隧道是否只监听 `127.0.0.1`、断开跳板时依赖目标是否被阻止、退出后子进程和临时配置是否清理。

## 回滚点

按里程碑提交逆序回滚。任何回滚都不得删除用户 `~/.ssh/config`、`known_hosts`、项目 `hosts.json` 或系统密钥环凭据；必要时保留配置并回退到旧版本只读忽略。
