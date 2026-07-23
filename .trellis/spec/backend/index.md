# 后端项目规范

本目录描述当前 Go 单进程 MVP 的真实目录、代码、运行、测试、部署和安全约定。规范以仓库中的 cmd/、internal/、docs/、README.md 和 AGENTS.md 为依据，不把路线图中的未实现能力当成现状。

## 开发前检查

1. 确认改动属于 cmd/ssh-tunnel-manager 入口还是 internal/ 实现包。
2. 阅读 project-conventions.md，确认回环监听、令牌、OpenSSH 和生命周期边界。
3. 涉及错误或日志时，阅读 error-handling.md 与 logging-guidelines.md。
4. 涉及运行、交付或验证时，阅读 runtime-and-deployment.md 与 quality-guidelines.md。
5. 修改后执行格式化、测试、静态检查和构建命令。

## 规范索引

| 文件 | 内容 |
|---|---|
| [directory-structure.md](./directory-structure.md) | Go 目录、包边界和命名 |
| [quality-guidelines.md](./quality-guidelines.md) | 代码风格、禁止模式和质量门槛 |
| [runtime-and-deployment.md](./runtime-and-deployment.md) | 本地运行、构建、测试和部署方式 |
| [error-handling.md](./error-handling.md) | 错误传播、输入校验和 HTTP 行为 |
| [logging-guidelines.md](./logging-guidelines.md) | 标准库日志和敏感信息脱敏 |
| [project-conventions.md](./project-conventions.md) | 安全、OpenSSH、HTTP 生命周期及文档约定 |

## 质量检查

    gofmt -w ./cmd ./internal
    go test ./...
    go vet ./...
    go build ./cmd/ssh-tunnel-manager

当前仓库没有数据库层；新增持久化实现时应先补充独立设计和对应规范，不要恢复已删除的数据库模板文件。
