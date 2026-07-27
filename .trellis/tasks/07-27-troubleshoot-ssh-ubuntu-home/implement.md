# 页面管理 SSH 跳板机配置：总体实施计划

## 总体目标

按依赖顺序交付项目 Host 存储、OpenSSH 分阶段跳板连接、Web 控制台交互和最终集成。预计工作超过 12 个任务，按项目约定拆成四个里程碑；每个里程碑独立测试、独立提交、独立恢复执行。

## 里程碑

| 顺序 | 里程碑 | 交付结果 | 子计划 |
|---|---|---|---|
| 1 | Host 配置基础 | 版本化存储、合并 Catalog、渲染器与 CRUD API | [milestone-1-host-config.md](./milestone-1-host-config.md) |
| 2 | 跳板连接与安全挑战 | 每会话配置、分阶段 ControlMaster、独立凭据、指纹确认 | [milestone-2-ssh-chain.md](./milestone-2-ssh-chain.md) |
| 3 | Web 控制台 | Host 管理对话框、来源展示、分阶段连接交互 | [milestone-3-web-ui.md](./milestone-3-web-ui.md) |
| 4 | 集成与收尾 | 真实场景验收、全量回归、文档与项目规范更新 | [milestone-4-integration.md](./milestone-4-integration.md) |

## 执行规则

- 默认一次只执行用户确认的一个里程碑，不自动继续后续里程碑。
- 每个里程碑开始前创建或激活对应 Trellis 子任务，并读取 `trellis-before-dev` 与相关后端规范。
- 每个里程碑结束执行 `trellis-check`，完成列出的验证命令并使用中文 Conventional Commit 提交。
- 发现计划与 OpenSSH 实际行为不一致、秘密可能泄漏、竞态无法封闭或质量门禁失败时，停止扩展执行并回到设计阶段。
- 里程碑 2 依赖里程碑 1；里程碑 3 依赖 1 和 2；里程碑 4 在前三项提交后执行。

## 最终验收

```bash
gofmt -w ./cmd ./internal
go test -race ./...
go vet ./...
go build ./cmd/ssh-tunnel-manager
```

最终还需在本地控制台完成 `mac_home -> ssh_ubuntu_home` 的手工连接验收，并确认页面、系统配置和秘密处理符合 PRD。
