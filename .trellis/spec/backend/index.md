# 后端项目规范

本目录描述当前 Go 单进程 MVP 的真实目录、代码、运行、测试、部署和安全约定。规范以仓库中的 cmd/、internal/、docs/、README.md 和 AGENTS.md 为依据，不把路线图中的未实现能力当成现状。

## 开发前检查

1. 确认改动属于 cmd/ssh-tunnel-manager 入口还是 internal/ 实现包。
2. 阅读 project-conventions.md，确认回环监听、令牌、OpenSSH 和生命周期边界。
3. 涉及错误或日志时，阅读 error-handling.md 与 logging-guidelines.md。
4. 涉及运行、交付或验证时，阅读 runtime-and-deployment.md 与 quality-guidelines.md。
5. 修改后执行格式化、测试、静态检查和构建命令。
6. 涉及 SSH 配置、认证、ControlMaster、M1 API 或 M2/M3 连接复用时，阅读 ssh-connection.md。
7. 涉及远程 `ss`、端口快照、自动刷新或 M2 API 时，阅读 port-discovery.md。
8. 涉及本地转发、端口分配、隧道 API/页面或 M3 清理顺序时，阅读 tunnel-management.md。
9. 涉及自动重连、重连状态、隧道日志、诊断脱敏或自动刷新偏好时，阅读 reliability-and-preferences.md。
10. 涉及启动参数、桌面退出、安装/卸载、双架构产物或 GitHub Release 时，阅读 release-delivery.md。

## 规范索引

| 文件 | 内容 |
|---|---|
| [directory-structure.md](./directory-structure.md) | Go 目录、包边界和命名 |
| [quality-guidelines.md](./quality-guidelines.md) | 代码风格、禁止模式和质量门槛 |
| [runtime-and-deployment.md](./runtime-and-deployment.md) | 本地运行、构建、测试和部署方式 |
| [error-handling.md](./error-handling.md) | 错误传播、输入校验和 HTTP 行为 |
| [logging-guidelines.md](./logging-guidelines.md) | 标准库日志和敏感信息脱敏 |
| [project-conventions.md](./project-conventions.md) | 安全、OpenSSH、HTTP 生命周期及文档约定 |
| [ssh-connection.md](./ssh-connection.md) | SSH Host、ControlMaster、askpass、Secret Service 和 M1 API 可执行契约 |
| [port-discovery.md](./port-discovery.md) | 受控远程命令、ss 解析、刷新状态和 M2 API 可执行契约 |
| [tunnel-management.md](./tunnel-management.md) | 回环端口分配、精确进程生命周期、M3 API 和控制台契约 |
| [reliability-and-preferences.md](./reliability-and-preferences.md) | M4 自动重连、状态、内存日志、偏好持久化和控制台契约 |
| [release-delivery.md](./release-delivery.md) | M5 启动参数、用户级安装、双架构产物、桌面退出和标签发布契约 |

## 质量检查

    gofmt -w ./cmd ./internal
    go test -race ./...
    go vet ./...
    go build ./cmd/ssh-tunnel-manager

当前仓库没有数据库层；新增持久化实现时应先补充独立设计和对应规范，不要恢复已删除的数据库模板文件。
