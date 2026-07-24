# M4：可靠性与控制台体验设计

## 架构边界

`internal/tunnel` 继续是隧道 ID、期望运行状态、本地端口、进程句柄、重连状态和近期事件日志的唯一所有者。它通过现有 SSH 转发接口启动进程，并新增窄连接接口读取 Host 状态和调用无交互 `Connect`；不读取 SSH 配置、不持有 ControlPath、不直接访问密钥环。

`internal/ssh` 继续独占 ControlMaster 和认证过程。自动恢复只调用现有 `Connect(ctx, host, ConnectOptions{})`，因此只能使用 ssh-agent、无口令密钥、SSH 配置或 Secret Service 中已保存的凭据。返回给隧道层的诊断必须已经脱敏并隐藏 ControlPath。

`internal/preference` 新增只负责非敏感 JSON 配置的文件存储。`internal/web` 在用户显式连接成功后读取该 Host 的自动刷新偏好，在切换开关时先持久化用户选择，再更新 `internal/portdiscovery`。入口负责计算 XDG 配置路径并注入存储。

`internal/web` 只消费隧道快照和日志契约，负责稳定 HTTP 映射与页面显示；不得在浏览器中维护重连真相或持久化隧道状态。

## 自动重连数据流

```text
运行中的转发进程意外退出
  -> 唯一 watch goroutine 收集已脱敏诊断并释放本地端口
  -> entry 进入 waiting_reconnect，记录事件和 nextRetryAt
  -> 等待 1/2/4/8/16 秒（可被 Stop/StopHost/Close 取消）
  -> 累加该隧道 ReconnectCount，进入 reconnecting
  -> 若 Host 已 connected：直接重建转发
  -> 若 Host 未连接：加入 Host 级共享恢复任务
       -> 一次 Connect(ctx, host, empty options)
       -> 同 Host 等待者共享同一成功或失败结果
  -> 使用原 ID、原远程端口重建转发
  -> 成功：running，RunningSince 重新计时，保留累计次数
  -> 失败：分类后继续退避，或转入 failed 等待人工处理
```

Host 级恢复任务按 Host 建立 singleflight，并记录仍在等待的隧道。停止单条隧道会取消该条目的等待；最后一个等待者离开时取消尚未完成的 Host 恢复。`StopHost` 和 `Close` 必须先取消对应恢复任务，再沿用 M3 精确停止流程，确保显式操作不会在稍后重新拉起连接或监听端口。

## 状态与快照契约

隧道状态扩展为：

```go
const (
    StatusStarting         Status = "starting"
    StatusRunning          Status = "running"
    StatusWaitingReconnect Status = "waiting_reconnect"
    StatusReconnecting     Status = "reconnecting"
    StatusStopping         Status = "stopping"
    StatusFailed           Status = "failed"
)
```

`Snapshot` 新增：

- `runningSince`：当前连续运行周期开始时间，仅运行状态非零；页面基于当前时间计算时长。
- `reconnectCount`：条目生命周期内实际自动重连尝试的累计次数。
- `nextRetryAt`：等待重连时的下一次尝试时间。

重连成功清除当前 `LastError` 并刷新 `RunningSince`，但不清零 `ReconnectCount`。本次连续故障使用独立的 `failureAttempts`：运行满 60 秒后再次故障才从第 1 次恢复；不足 60 秒再次退出时延续剩余额度，防止抖动产生无限循环。手动 `Create` 重试失败条目视为新的人工恢复周期，但保留 ID 和累计次数。

不可重试分类包括 SSH 认证、主机密钥、配置、本地依赖和需要交互凭据；取消及服务关闭直接结束后台任务；网络、超时、主连接消失和一般进程失败可在剩余额度内重试。只对真正开始的自动尝试增加 `ReconnectCount`。

## 日志契约

每条隧道维护一个内存环形事件列表：

```go
type LogEntry struct {
    Time       time.Time `json:"time"`
    Level      string    `json:"level"`
    Message    string    `json:"message"`
    Diagnostic string    `json:"diagnostic,omitempty"`
}
```

- 最多 100 条，同时限制序列化前文本总量为 64 KiB；超限时从最旧记录开始淘汰。
- 事件覆盖初次启动、意外退出、等待重试、开始重试、恢复成功、可重试失败和最终失败。
- 普通列表快照只包含安全 `LastError`，不内联日志或诊断。
- `GET /api/tunnels/{id}/logs` 返回 `{ "logs": [] }`；未知或已停止 ID 返回 `404 tunnel_not_found`。
- 日志只存在于对应 entry；`Stop`/`StopHost` 删除 entry 后不可再读取，进程退出时随内存释放。

SSH 层必须保证提供给隧道层的 `Process.Diagnostics()` 已执行有界收集和脱敏，至少过滤认证秘密及 ControlPath。Web 层不得再次拼接内部错误字符串作为诊断。

## 偏好文件契约

默认路径为 `${XDG_CONFIG_HOME:-~/.config}/ssh-tunnel-manager/config.json`，父目录权限收紧为 `0700`，文件权限为 `0600`。格式带版本号以便拒绝未知结构：

```json
{
  "version": 1,
  "hosts": {
    "server-a": { "autoRefresh": true }
  }
}
```

- 只接受合法 Host 键和布尔值；未知字段、尾随 JSON、过大文件或版本不支持均视为损坏。
- 缺失文件等价于全部关闭。损坏文件不覆盖、不自动删除，进程使用内存默认值并在启动日志给出可读警告。
- 更新在存储锁内执行“同目录临时文件 -> `Sync` -> 原子重命名”，防止并发开关写入和半文件。
- 写入失败时 API 返回稳定 `preference_write_failed`；已连接 SSH 和现有隧道继续运行。
- 程序启动只加载配置，不执行连接、探测或自动刷新。用户手动连接成功后，Web 层才把保存的 `true` 应用到端口发现服务。

## HTTP 契约

保留 M1-M3 路由和字段，只做兼容性新增：

| 方法与路径 | 成功 | 主要失败 |
| --- | --- | --- |
| `GET /api/tunnels` | 新增时间戳和重连计数字段 | 无业务失败 |
| `GET /api/tunnels/{id}/logs` | `200 { "logs": [] }` | `404 tunnel_not_found` |
| `PUT /api/servers/{host}/ports/auto-refresh` | 原快照；同步保存偏好 | `500 preference_write_failed` 及原 M2 错误 |

连接 API 成功后恢复自动刷新属于附带动作；偏好读取或自动刷新恢复失败不得把已经成功的 SSH 连接伪装成连接失败。失败通过安全警告和后续状态呈现，用户仍可手动切换开关。

## 控制台设计

- 隧道总览增加“运行时长”和“重连”列；等待状态显示下一次尝试倒计时，其他状态使用固定中文标签。
- 运行时长根据服务端 `runningSince` 在页面渲染，不把浏览器计时结果写回服务端。
- 每条隧道提供“日志”文本命令，按需调用日志 API，在当前行下方使用原生可展开区域展示时间、级别、事件和可选诊断；默认折叠。
- 手动重试继续调用已有创建 API，停止继续调用 DELETE；等待或重连中仍允许停止，复制与打开只在运行中可用。
- 保持现有 10 秒整体轮询；日志展开时刷新当前日志，不为每条折叠日志单独轮询。
- 表格使用稳定列宽、横向滚动和紧凑操作区，窄屏不压缩到文本重叠。

## 并发、清理与兼容

- 每个转发进程仍只有一个 goroutine 调用 `Wait`。重建产生新的 watch；旧 watch 通过身份检查不得覆盖新状态。
- entry 锁保护单条状态、进程和日志；Manager 锁只保护索引、端口预留、关闭标志和 Host 恢复索引，不在全局锁下等待网络或进程。
- 所有退避等待使用可取消计时器；测试注入退避、稳定窗口和时钟相关函数，生产值只有一处定义。
- M1-M3 JSON 字段和行为保持兼容；新增字段使用 `omitempty` 或零值语义，旧页面仍能读取基础状态。
- 回滚可按“页面/日志路由 -> 偏好接入 -> 自动重连状态 -> SSH 诊断收紧”逆序进行，不改变已有 SSH 配置、凭据或远端状态。

## 安全与运维

- 不新增网络依赖、数据库、守护进程、sudo、非回环监听或磁盘诊断日志。
- 自动重连不能自动接受新主机密钥，也不能把秘密放入参数、环境、JSON、普通日志或偏好文件。
- 运行时退出顺序保持“隧道 -> 端口发现 -> SSH -> HTTP”；隧道关闭阶段先取消全部退避和恢复任务。
