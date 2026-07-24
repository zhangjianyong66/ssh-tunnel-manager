# M2：远程端口发现设计

## 架构边界

新增 `internal/portdiscovery` 作为远程 `ss` 输出解析、发现快照和自动刷新生命周期的唯一所有者。它不保存 SSH 进程句柄、不处理 HTTP，也不解析浏览器请求。

`internal/ssh` 扩展一个面向内部调用方的受控单次命令能力：只允许在当前 `connected` 的 Host 上，使用该 Host 的私有 `ControlPath` 调用系统 `ssh`。命令仍由参数数组构造，且不接受 shell 字符串。该能力返回受限长度的标准输出和脱敏诊断，供端口发现服务使用；`ControlPath` 继续不出现在 JSON 中。

`internal/web` 持有端口发现服务并新增端口查询、手动刷新和自动刷新切换路由，同时把已连接 Host 的端口列表、刷新按钮与自动刷新开关呈现在现有控制台。入口负责构造服务，并在 SIGINT/SIGTERM 清理阶段先停止发现服务再关闭 SSH 会话。

## 数据流与契约

```text
浏览器刷新/自动定时器
  -> web API
  -> portdiscovery.Service（同 Host 串行）
  -> ssh.Manager.Execute（确认 connected，取私有 ControlPath）
  -> ssh -S <control-path> -- <host> ss -ltnp
  -> ss 解析器
  -> PortSnapshot（内存）
  -> JSON / 页面
```

### SSH 单次命令

- 远程命令先尝试 `ss -ltnp`；SSH 调用等价于 `ssh -S <control-path> -- <host> ss -ltnp`，实际实现以参数数组传递；`--` 只结束本地 OpenSSH 选项解析，远程命令只接受受控参数。
- 第一轮因 `ss` 的进程信息权限不足、`-p` 选项不可用或命令返回非零而失败时，使用同一 ControlMaster 仅重试一次 `ss -ltn`。
- 每次发现具有固定的请求级超时；请求取消直接结束当前命令，不将其当作可恢复的自动刷新错误。
- 未连接 Host 不能执行远程命令，返回端口发现的 `server_not_connected` 错误；未知 Host 仍由 web 层在配置快照处返回 `host_not_found`。

### 端口模型

```go
type Port struct {
    Number  uint16 `json:"number"`
    Process string `json:"process,omitempty"`
}

type Snapshot struct {
    Host          string    `json:"host"`
    Ports         []Port    `json:"ports"`
    RefreshedAt   time.Time `json:"refreshedAt,omitempty"`
    AutoRefresh   bool      `json:"autoRefresh"`
    Refreshing    bool      `json:"refreshing"`
    Diagnostics   []string  `json:"diagnostics,omitempty"`
    LastError     *Error    `json:"lastError,omitempty"`
}
```

- 解析器接受 `ss -ltn` 和 `ss -ltnp` 的表格输出，只接受状态列为 `LISTEN` 的项目。
- 本地地址字段用于提取端口，支持 IPv4、`*:<port>`、`[::]:<port>` 与 IPv6 格式；端口必须落在 `1..65535`。
- 同一个端口只保留一项，优先保留带进程名的数据；结果按端口升序排列。畸形行或无法理解的进程元数据只产生不含原始行内容的非致命诊断，不中断整个刷新。
- 发现到的所有 TCP 监听端口都展示；M3 仍固定尝试远程 `127.0.0.1:<port>`，监听地址选择暂不开放为用户配置。

### 刷新与并发

- `Service.Refresh(ctx, host)` 为每个 Host 保存一把操作锁：相同 Host 的手动刷新和定时刷新复用一个进行中的结果，不会产生并发远程 `ss`；不同 Host 互不阻塞。
- 成功刷新原子替换端口、时间和错误状态。失败只更新 `LastError`，不得清空最后一次成功的端口和 `RefreshedAt`。
- `SetAutoRefresh(host, enabled)` 在 `enabled=true` 时启动一个带取消函数的 10 秒循环；重复启用是幂等的。关闭、SSH 断开后的下一次刷新失败、或 `Close` 都取消循环并等待其退出。
- 自动刷新单次失败保留快照并等待下一周期重试；用户请求取消不写入 `LastError`。

## HTTP 与页面

所有路由受 M1 Cookie 认证中间件保护：

| 方法与路径 | 成功响应 | 失败响应 |
| --- | --- | --- |
| `GET /api/servers/{host}/ports` | `200`，该 Host 的 `Snapshot` | `404 host_not_found` |
| `POST /api/servers/{host}/ports/refresh` | `200`，刷新后的 `Snapshot` | `404 host_not_found`、`409 server_not_connected`、`504 discovery_timeout`、`502 discovery_failed` |
| `PUT /api/servers/{host}/ports/auto-refresh` | `200`，更新后的 `Snapshot` | `400 invalid_request`、`404 host_not_found`、`409 server_not_connected` |

自动刷新请求体精确为 `{ "enabled": true }`，限制大小并拒绝未知字段。页面在一次成功连接后立即请求手动刷新；已连接行展示端口、进程名、刷新状态、手动刷新控件和自动刷新复选框。页面不会因为浏览器关闭改变自动刷新或 SSH 生命周期。

## 错误、安全与兼容

- 对外错误使用稳定的端口发现错误码和中文安全消息；不把 `ssh`、`ss` 原始输出、ControlPath、令牌或秘密返回到 HTTP 响应。
- 捕获的输出只驻留在内存，截断到合理上限；错误诊断沿用 SSH 层的秘密脱敏策略。
- 不使用 `sudo`、shell、`pkill`、模糊进程匹配或新的持久化文件。自动刷新状态仅在运行内存中存在，重启后默认为关闭。
- M2 不创建本地监听端口、不进行端口转发、不实现重连、日志查看或偏好持久化；这些属于后续里程碑。

## 回滚与兼容策略

该变更只新增内部包、SSH 管理器受控执行接口、受认证 API 和页面内容，不改变 M1 的连接命令、凭据路径或既有 API 响应。若需回滚，移除 M2 路由和服务构造即可恢复 M1 行为；控制台与 SSH 会话仍可正常关闭。
