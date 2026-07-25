# SSH 隧道管理器 MVP 整体验收清单

## 1. 任务与文档一致性

- [x] 核对 M1 至 M5 均已归档为 `completed`，所有子任务 PRD 验收项已完成。
- [x] 对照最终代码检查 README、产品设计、架构、路线图、AGENTS 和 Trellis 规范。
- [x] 修复“桌面自动打开浏览器”和 `POST /api/shutdown` 等 M5 后文档漂移。

## 2. 全量质量与交付门禁

- [x] `gofmt -w ./cmd ./internal`，并确认 `gofmt -l` 无输出。
- [x] `go test -race ./...`。
- [x] `go vet ./...`。
- [x] `go build ./cmd/ssh-tunnel-manager`。
- [x] `./scripts/test-packaging.sh`。
- [x] `./scripts/test-release.sh`。
- [x] 使用 `actionlint` 校验标签触发、权限、官方 Action 固定提交和草稿发布顺序。
- [x] 使用 `shfmt -d` 和 `sh -n` 校验全部交付脚本。

## 3. 运行时与安全验收

- [x] 验证 `/healthz` 为 200、未认证业务 API 为 401、带令牌页面可访问。
- [x] 验证认证退出为 202，服务进程结束且监听端口释放；不连接真实远程服务器。
- [x] 审查回环监听、OpenSSH 参数数组、精确进程清理、凭据/令牌/ControlPath 脱敏和发布包内容。
- [x] 确认 Git 工作区不包含 `dist/`、本地二进制、秘密或带令牌 URL。

## 4. 收尾

- [x] 使用 `trellis-check` 完成父任务全量复核，所有 PRD 验收项标记完成。
- [x] 创建中文 Conventional Commit 记录整体验收修正，不推送、不打标签、不发布 Release。
- [x] 归档父 MVP 任务并记录开发 journal。

## 回滚点

- 任一核心门禁失败时停止归档，修正后从对应层级重跑，不能用文档说明替代失败测试。
- 若真实外部服务器或 Secret Service 会话不可用，不进行有副作用的试连；以子任务可控测试和当前本地运行边界作为验收证据。
- 若安全扫描发现秘密或未忽略二进制，先移除并重新扫描，再允许提交。
