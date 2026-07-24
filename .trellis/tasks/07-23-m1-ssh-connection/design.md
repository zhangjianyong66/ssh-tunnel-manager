# M1 技术设计：SSH 配置与连接

## 边界与目录

- `cmd/ssh-tunnel-manager` 只负责启动、依赖组装和 HTTP 路由注册。
- `internal/sshconfig` 负责读取配置文件、递归展开 `Include`、提取显式 Host 别名。
- `internal/ssh` 负责 OpenSSH 参数构造、主连接进程、ControlPath、状态和诊断输出。
- `internal/credential` 定义凭据接口并提供 Linux Secret Service 实现；业务层只依赖接口，不接触具体 D-Bus 类型。
- `internal/web` 或入口中的独立 handler 负责 M1 API 和页面状态映射，不把 SSH 子进程细节暴露给浏览器。

## 数据流与生命周期

1. 启动时读取默认 `~/.ssh/config`（可注入路径供测试），递归解析 `Include`，返回显式别名及来源文件。
2. 用户请求连接时，服务校验别名属于当前配置快照，解析凭据引用；缺少凭据时返回需要输入的稳定错误码，浏览器在已认证回环页面提交一次性秘密。
3. 连接管理器为 Host 创建专用运行时目录和随机 `ControlPath`，以参数数组启动长期 `ssh -M -N -T` 主进程；stdin 关闭，askpass 通过受控临时 helper 从内存/密钥环返回秘密。
4. 连接成功通过 `ssh -S <control-path> -O check <host>` 或进程状态确认；后续 M2/M3 通过同一 ControlPath 复用。
5. 断开先发送 `ssh -S <control-path> -O exit <host>`，超时后只终止保存的进程句柄，最后删除运行时 socket/helper；不会匹配或杀死其他 SSH 进程。

## 配置解析契约

- 只接受 `Host`、`Include` 和注释/空白等枚举所需语法；其他指令原样跳过，由真正的 OpenSSH 连接解释。
- `Include` 支持绝对路径、相对当前配置文件路径和 `~`，使用受限 glob；递归深度和已访问路径集合防止循环。
- 只保留不含 `*`、`?`、`!` 的 Host token；保留首次出现顺序并去重。
- 解析错误不阻塞可读取的其他 Host，但 API 要返回带来源文件和行号的诊断。

## 认证与秘密

- `CredentialStore` 提供按 Host/用户名/用途读取、保存和删除，不暴露秘密到结构化日志。
- Linux 实现使用 Secret Service D-Bus 客户端依赖，属性包含应用名、Host、用户名和用途；依赖缺失或会话不可用时返回明确错误。
- askpass helper 使用随机临时文件、`0700` 目录和最小环境；只接受 SSH 传入的 prompt，匹配密码/口令用途后从内存或密钥环返回，使用后删除。
- 浏览器提交的秘密仅存在请求处理和当前连接生命周期；保存操作显式调用 `CredentialStore.Save`。
- 未知或变更主机密钥不绕过 OpenSSH 检查，不设置 `StrictHostKeyChecking=no`，错误中只提供脱敏指纹和系统核验建议。

## HTTP 契约

- `GET /api/ssh-hosts`：返回显式 Host 别名、来源和解析诊断。
- `POST /api/servers/{host}/connect`：启动或幂等返回已有连接；缺少凭据返回 `credential_required`，请求体秘密不写日志。
- `POST /api/servers/{host}/disconnect`：停止该 Host 主连接及关联资源；重复调用幂等。
- `GET /api/servers/{host}`：返回状态、最近错误、连接时间和脱敏诊断摘要。
- 所有业务路由要求令牌 Cookie；Host 参数必须按配置快照校验，错误使用稳定 `code`、可读 `message` 和可选 `details`。

## 并发、错误与回滚

- 每个 Host 一个锁和状态机，连接/断开串行，状态读取无锁快照；不同 Host 可并行。
- 失败状态区分配置、认证、主机密钥、网络、超时、凭据依赖和用户取消；原始 stderr 只进入脱敏诊断缓冲区。
- M1 只提供显式连接/断开，不实现自动重连；意外断线转为 `failed` 并保留错误供 M4 使用，显式断开转为 `disconnected`。
- 若 ControlPath 或密钥环实现不稳定，可回滚到接口保留、实现禁用的构建状态，不改变 M2/M3 的上层契约。
