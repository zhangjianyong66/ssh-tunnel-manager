# 项目重要约定

## 安全边界

- 控制台令牌由 crypto/rand 生成，仅在当前进程内存中有效；首次 URL 访问换取 HttpOnly、SameSite=Strict Cookie（cmd/ssh-tunnel-manager/main.go）。
- /healthz 是当前唯一不要求 token 的健康检查路由；业务页面和未来业务 API 默认需要令牌 Cookie。
- ~/.ssh/config 只读，不由程序直接修改；普通偏好与秘密存储必须分离。
- 默认远程目标地址是 127.0.0.1，第一版不要求处理复杂监听地址。

## OpenSSH 与隧道

- 系统 ssh 是唯一 SSH 协议实现，必须复用用户现有 OpenSSH 配置能力。
- 端口转发复用既有 ControlMaster，参数应等价于 `ssh -S <control-path> -N -T -o BatchMode=yes -o ExitOnForwardFailure=yes -L 127.0.0.1:<local>:127.0.0.1:<remote> -- <host-alias>`，实际执行必须使用参数数组。自动重连由 `internal/tunnel` 按 Host 协调既有 SSH Manager，不新增独立 SSH 协议或无限 Keepalive 循环。
- 每个连接和隧道由明确句柄管理；禁止用全局进程名匹配停止其他 SSH 会话。
- SSH 断开后的自动重连固定最多 5 次并记录状态；认证、主机密钥、配置、依赖和交互凭据错误立即转人工处理，不得因一个隧道故障退出整个 Web 服务。

## HTTP 与生命周期

- Web 服务、本地代理端口和控制台 URL 默认只绑定回环地址。
- 页面刷新或关闭不会停止 Go 进程；只有用户显式停止或程序退出才清理隧道。
- `POST /api/shutdown` 是入口拥有的已认证进程退出边界，成功先返回 202 再取消停止上下文；控制台按钮必须有明确确认，资源仍由 `shutdownRuntime` 按既有顺序清理。
- 写操作未来应支持请求幂等标识，错误使用稳定的结构化错误码；日志 API 必须脱敏。

## 文档与变更

- 产品范围和用户流程以 docs/product-design.md 为准，技术边界以 docs/architecture.md 为准，阶段计划以 docs/roadmap.md 为准。
- 规范只记录当前可复用的事实和已确认约定；路线图中的未实现能力要明确标注为未来行为。
- 计划、设计和提交说明使用中文；新增运行方式、目录结构或部署约定时同步更新根目录 AGENTS.md。
