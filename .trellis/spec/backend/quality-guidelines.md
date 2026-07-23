# 代码风格与质量

## 基础工具链

- 使用 Go 1.22 或更高版本（go.mod 的 go 1.22）。
- 所有 Go 修改后运行 gofmt -w ./cmd ./internal；不要手工维护格式。
- 依赖优先使用标准库。当前入口使用 crypto/rand、html/template、net/http 等标准库，没有第三方运行时依赖。
- 导入按 gofmt 结果保持分组；错误变量使用 err，布尔判断直接表达意图。

## 当前代码模式

- 常量集中声明在文件顶部，例如 defaultAddr 和 cookieName。
- 小而确定的纯逻辑抽成函数，例如 newToken、isLoopbackAddr 和 authorize；函数名表达动作或判断。
- 启动参数在 flag 层解析，校验失败后再创建服务资源；不要让无效地址进入监听。
- HTTP 路由使用标准 http.NewServeMux，处理函数完成授权、业务动作和响应写入。
- HTML 使用 html/template，不要拼接未转义的 HTML。

## 禁止模式

- 不得监听 0.0.0.0、局域网地址或公网地址；Web 控制台和本地代理端口都只绑定回环地址。
- 不得拼接 shell 字符串执行 SSH；使用参数数组调用系统 ssh，并在边界校验主机、端口和路径。
- 不得使用全局 pkill 或模糊进程匹配清理隧道，必须保存并精确管理子进程句柄（docs/architecture.md）。
- 不得把密码、私钥口令或完整私钥写入 JSON、SQLite、命令行、环境变量或日志。
- 不得为未来功能预先引入数据库、复杂框架或未使用的抽象；先遵循当前 MVP 的实际边界。

## 修改检查

- 新增导出 API 时补充 Go 文档注释，并说明错误和生命周期语义。
- 修改认证、监听地址、子进程生命周期或秘密处理时，必须同时检查 README.md、docs/architecture.md 和本目录相关规范。
- 变更完成后至少运行格式化、测试、静态检查和构建命令；命令见 runtime-and-deployment.md。

## 参考文件

- cmd/ssh-tunnel-manager/main.go：当前入口的命名、路由、错误和模板风格。
- AGENTS.md：项目级安全边界与验证约定。
