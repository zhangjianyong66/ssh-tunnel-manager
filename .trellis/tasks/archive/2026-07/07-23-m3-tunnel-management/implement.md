# M3：端口转发与隧道管理执行计划

## 子任务 1：SSH 转发边界与隧道核心

- [x] 在 `internal/ssh` 新增受控 `StartLocalForward`，固定回环地址、参数数组、BatchMode、ControlMaster 前后检查和 `ExitOnForwardFailure`。
- [x] 将长生命周期进程诊断限制为有界缓冲，保持 M1 既有连接行为不变。
- [x] 新建 `internal/tunnel`，实现随机 ID、状态/错误模型、同号优先与冲突自动分配、内部端口预留和有界外部竞争重试。
- [x] 实现同目标幂等并发创建、就绪探测、唯一 Wait 监控、失败保留、重建、精确停止、按 Host 停止和 Close。
- [x] 添加 SSH 参数/主连接竞态测试，以及隧道分配、并发、启动失败、取消、停止和清理测试。

完成门禁：`go test -race ./internal/ssh ./internal/tunnel`，确认无数据竞争、无 shell、无非回环地址、无模糊进程操作。

## 子任务 2：HTTP API 与全局生命周期

- [x] 扩展 `internal/web.App` 注入隧道服务，新增 POST/GET/DELETE 路由和严格请求解码。
- [x] 实现 Host、连接状态和隧道错误到稳定 HTTP 状态码的映射，响应不暴露内部诊断。
- [x] 调整断开流程为“停止 Host 隧道 -> 关闭自动刷新 -> 断开 SSH”。
- [x] 在入口构造隧道服务，并按“隧道 -> 发现 -> SSH -> HTTP”执行有界退出清理。
- [x] 覆盖创建、列表、幂等、非法请求、未知 Host、未连接、停止和断开清理的 Web/入口测试。

完成门禁：`go test -race ./internal/web ./cmd/ssh-tunnel-manager`，确认原 M1/M2 路由回归通过。

## 子任务 3：控制台隧道操作

- [x] 扩展端口表的映射、状态与操作列，支持代理、重试、复制裸地址、HTTP 打开和停止。
- [x] 新增活动隧道总览，保证远程端口快照变化后仍有停止入口。
- [x] 每轮页面加载只取一次隧道列表，并按 Host/远程端口映射；页面刷新或关闭不触发停止。
- [x] 完善按钮忙碌/失败状态和窄屏布局，避免重复点击产生视觉竞态或控件重叠。
- [x] 添加模板关键契约测试，并用无头浏览器检查桌面与窄屏交互和控制台错误。

完成门禁：`go test -race ./internal/web`，并保存临时截图做人工检查，不向仓库提交生成物。

## 子任务 4：集成、规范与交付检查

- [x] 使用本地可控 SSH/端口占用场景验证同号成功、冲突分配、多 Host/多端口、停止释放和 SIGINT 清理。
- [x] 新增 `.trellis/spec/backend/tunnel-management.md`，同步 backend 索引、README、产品设计、架构和路线图。
- [x] 如发现新的项目级目录、运行或清理约定，同步根 `AGENTS.md`。
- [x] 执行完整质量门禁与差异审查，重点检查秘密、ControlPath、非回环监听、goroutine/子进程泄漏和接口兼容。
- [x] 按中文 Conventional Commits 提交 M3；不推送远端，除非用户另行要求。

## 全量验证门禁

1. `gofmt -w ./cmd ./internal`
2. `go test -race ./...`
3. `go vet ./...`
4. `go build -o /tmp/stm-m3-release ./cmd/ssh-tunnel-manager`
5. `git diff --check`
6. 运行时冒烟：`/healthz` 为 `200`、无 Cookie 业务 API 为 `401`、认证后隧道 API 可用、SIGINT 后构建产物退出且监听端口释放。

## 审查重点与回滚点

- 子任务 1 是首个回滚点：若复用 ControlMaster 的长生命周期客户端无法稳定确认监听，暂停上层接入并回到设计阶段评估精确 `-O forward/-O cancel`。
- 子任务 2 不得改变 M1/M2 JSON 字段或认证边界；如有回归，先撤除隧道路由和断开钩子，保留核心包测试。
- 子任务 3 只消费 API 快照，不在浏览器保存隧道真相；页面状态异常时可单独回滚模板而不影响运行隧道。
- 最终检查所有 `Process.Wait` 只有一个调用者，所有停止路径都有超时升级，所有端口只绑定 `127.0.0.1`。
