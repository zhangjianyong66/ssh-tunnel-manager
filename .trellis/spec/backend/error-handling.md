# 错误处理

错误处理应让命令行用户得到可读提示，让 HTTP 客户端得到正确状态码，同时避免泄露访问令牌、密码、私钥口令和完整凭据内容。

## 当前入口模式

- 启动阶段的致命错误使用 log.Fatalf，例如 newToken() 失败或请求监听非回环地址（cmd/ssh-tunnel-manager/main.go）。这类错误发生在服务启动前，程序应直接退出。
- HTTP 请求中的授权失败使用 http.Error 返回 401 Unauthorized，正文保持简短，不回显用户输入或令牌。
- 页面模板渲染失败记录错误，但不把内部错误堆栈写入响应；参考 pageTemplate.Execute 的处理。
- HTTP 服务先使用 net.Listen 同步绑定，失败时在输出或打开令牌 URL 前退出；成功后由可关闭的 http.Server.Serve 运行，正常退出产生的 http.ErrServerClosed 不作为异常记录。

## 错误传播规则

- 底层函数返回原始 error，在边界处补充上下文，例如 fmt.Errorf("读取 SSH 配置: %w", err)；不要无理由丢弃错误。
- 只有明确安全的情况才允许忽略返回值。当前代码对 http.ResponseWriter.Write 使用 _ =，因为响应已经无法再恢复；新增代码应说明同类理由。
- 不用 panic 处理用户输入、网络失败、子进程退出或端口冲突；这些都应转换为返回错误或 HTTP 状态码。
- 对外错误与内部诊断分离：响应返回稳定的可读消息，日志保留必要上下文但必须脱敏。

## 输入校验

- -addr 必须通过 isLoopbackAddr 校验，只接受 127.0.0.1:<port>、localhost:<port> 或 [::1]:<port> 形式；拒绝公网和局域网地址。
- 主机别名、端口、文件路径等来自用户或配置的值，进入 exec.Command 前应完成类型和边界校验。
- OpenSSH 调用必须传递参数数组，不得把输入拼接进 shell 命令字符串（见 docs/architecture.md）。

## 常见错误

- 不要将 token、密码、私钥口令放进错误文本、命令行参数、环境变量或日志。
- 不要把 err.Error() 原样返回给浏览器，除非已确认不含敏感信息；优先使用稳定错误码或脱敏消息。
- 不要用 log.Fatal 处理单个 HTTP 请求或可恢复的隧道故障，否则会连带终止整个管理器。
