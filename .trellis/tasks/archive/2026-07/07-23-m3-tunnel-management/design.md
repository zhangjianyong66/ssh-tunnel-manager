# M3：端口转发与隧道管理设计

## 架构边界

新增 `internal/tunnel`，作为本地端口分配、隧道身份、并发创建、状态快照和精确停止的唯一所有者。它不读取 SSH 配置、不解析 HTTP，也不持有 ControlPath；只通过窄接口请求 SSH 层启动一条本地转发。

`internal/ssh.Manager` 新增 `StartLocalForward`：在目标 Host 的操作锁内确认 ControlMaster 仍为 `connected`，使用系统 OpenSSH 参数数组启动复用连接的长生命周期转发进程，并返回精确 `Process` 句柄。启动前后通过 ControlMaster check 限制主连接消失时的独立连接回退窗口；转发客户端固定使用 `BatchMode=yes`，不会开启新的凭据交互。

`internal/web` 注入隧道管理器，负责 Host 配置校验、严格 JSON、稳定 HTTP 错误映射和页面交互。入口按“隧道 -> 端口发现 -> SSH 主连接 -> HTTP 服务”的顺序清理。

## 数据流

```text
端口表“代理”
  -> POST /api/tunnels {host, remotePort}
  -> web 校验 Host 与请求
  -> tunnel.Manager.Create（同目标 singleflight）
  -> 回环端口分配与进程级预留
  -> ssh.Manager.StartLocalForward（持有 Host 操作锁）
  -> ssh -S <control-path> -N -T -o BatchMode=yes
         -o ExitOnForwardFailure=yes
         -L 127.0.0.1:<local>:127.0.0.1:<remote> -- <host>
  -> 回环监听就绪探测
  -> Tunnel Snapshot -> JSON -> 页面
```

停止路径只按随机隧道 ID 查找管理器保存的 `Process`：先发 `os.Interrupt` 并有界等待，超时后只对该句柄发 `os.Kill`。禁止 `pkill`、进程名匹配和 shell 命令。

## SSH 转发契约

### 新增接口

```go
func (m *Manager) StartLocalForward(
    ctx context.Context,
    host string,
    localPort uint16,
    remotePort uint16,
) (Process, error)
```

- Host 沿用 M1 校验，端口必须为 `1..65535`。
- Host 操作锁覆盖连接状态读取、ControlMaster 前置检查、进程启动和后置检查；显式断开必须等待该临界区完成。
- 转发参数固定绑定本地与远程 `127.0.0.1`，不接受调用方提供任意地址或任意 SSH 参数。
- 前置或后置 ControlMaster check 失败时返回 `server_not_connected`，已启动的转发进程必须立即精确清理。
- 启动后由 `internal/tunnel` 独占调用 `Process.Wait`；SSH 管理器不得同时等待同一进程。
- `RealRunner` 对长生命周期进程的 stderr 使用有界缓冲，诊断不进入隧道 HTTP 快照。

采用长生命周期复用客户端，而不是 `ssh -O forward`，是为了让每条隧道拥有可观察、可精确停止的进程句柄，并为 M4 的失败状态与自动重连提供事件来源。

## 隧道模型

```go
type Status string

const (
    StatusStarting Status = "starting"
    StatusRunning  Status = "running"
    StatusStopping Status = "stopping"
    StatusFailed   Status = "failed"
)

type Snapshot struct {
    ID         string `json:"id"`
    Host       string `json:"host"`
    RemotePort uint16 `json:"remotePort"`
    LocalPort  uint16 `json:"localPort,omitempty"`
    Address    string `json:"address,omitempty"`
    Status     Status `json:"status"`
    LastError  *Error `json:"lastError,omitempty"`
}
```

- ID 使用 `crypto/rand` 生成，只用于应用内精确寻址，不编码 PID、Host 或端口。
- `Address` 只可能是 `127.0.0.1:<localPort>`；浏览器打开 URL 由页面从该地址构造 `http://<address>/`。
- 快照不包含 ControlPath、PID、原始 stderr、凭据、令牌或内部进程对象。
- 列表按 Host、远程端口、本地端口稳定排序，返回非 nil 空数组。
- 失败条目保留安全 `LastError`。再次创建同一目标时串行重建该条目；成功运行或正在启动时直接返回现有快照。

## 端口分配与启动确认

- 管理器先尝试远程同号端口。若回环绑定预检失败，向操作系统申请一个临时回环端口，读取端口号后关闭预检监听器。
- 管理器内部维护“分配中/运行中”端口集合，防止本进程内并发创建在预检关闭与 OpenSSH 监听之间拿到同一端口。
- 外部进程仍可能在预检后抢占端口。OpenSSH 使用 `ExitOnForwardFailure=yes`；若诊断明确为本地绑定冲突，则释放预留并重新分配，最多执行固定次数的有界重试。
- 每次启动在固定超时内轮询 `127.0.0.1:<localPort>`。监听可连接且进程未退出后才标记 `running`；超时或请求取消时精确停止进程并返回安全错误。
- 就绪探测只验证本地 TCP 监听，不要求远程最终服务接受连接；OpenSSH 文档明确 `ExitOnForwardFailure` 不覆盖最终目标连接失败。

## 并发与生命周期

- `host + remotePort` 是幂等键。同目标并发创建共享一个条目操作锁，不同目标不共用全局长临界区。
- 全局锁只保护 ID/目标索引、关闭状态和本地端口预留集合；进程启动、就绪等待与停止在条目锁下执行。
- `Stop(id)` 对不存在或已移除的 ID 成功返回，使 DELETE 可安全重试；活动条目停止后从两个索引和端口预留中移除。
- `StopHost(host)` 复制目标条目后逐条精确停止，不在全局锁内等待子进程。
- `Close(ctx)` 原子拒绝新建，再停止全部条目。失败条目没有运行进程，但仍由停止操作从列表移除。
- 进程监控 goroutine 是 `Wait` 的唯一调用方。非停止路径退出时写入 `failed` 和安全错误；停止路径只负责等待其完成，不重复调用 `Wait`。

## HTTP 契约

所有路由继续由入口令牌 Cookie 保护：

| 方法与路径 | 请求/成功 | 主要失败 |
| --- | --- | --- |
| `POST /api/tunnels` | 严格 `{ "host": string, "remotePort": int }`；`200` Snapshot | `400 invalid_request`、`404 host_not_found`、`409 server_not_connected`、`409 local_port_unavailable`、`504 tunnel_timeout`、`502 tunnel_start_failed` |
| `GET /api/tunnels` | `200 { "tunnels": [] }` | 无业务失败 |
| `DELETE /api/tunnels/{id}` | `204`，重复或未知 ID 仍成功 | `503 service_closed` |

- POST 请求体上限 4 KiB，拒绝缺失字段、未知字段、尾随 JSON、空 Host 和越界端口。
- Web 层先用当前 SSH 配置快照校验 Host；服务层再次依赖 SSH 层连接状态校验，避免检查与启动之间的竞态。
- 用户断开 Host 时先 `StopHost`，再关闭端口自动刷新，最后断开 ControlMaster。

## 控制台设计

- 端口表新增“本地映射”和“操作”列。无隧道时显示“代理”；启动/运行/失败分别显示稳定状态和适用操作。
- 运行条目显示裸地址，提供复制、打开和停止操作；打开按钮使用新标签页访问 `http://127.0.0.1:<port>/`，不会在隧道创建时自动打开。
- 失败条目显示安全错误并提供重新代理和清除；复制与打开只在 `running` 时可用。
- 页面每轮加载只请求一次隧道列表，再按 `host + remotePort` 映射到各端口行；刷新页面不会改变服务端隧道。
- 远程端口从最新发现列表消失但隧道仍运行时，在独立“活动隧道”表中继续展示，避免用户失去停止入口。
- 窄屏使用可滚动表格或紧凑操作布局，固定操作尺寸，避免状态、地址与按钮重叠。

## 错误与安全

- 稳定错误码：`invalid_tunnel`、`server_not_connected`、`local_port_unavailable`、`tunnel_timeout`、`tunnel_cancelled`、`tunnel_start_failed`、`service_closed`。
- 只有本地绑定冲突触发端口重试；认证、主连接消失、依赖缺失和其他进程失败不得伪装成端口冲突。
- stderr 只用于进程退出后的内部分类，限制长度并转成安全中文错误；HTTP 和普通日志不返回原始内容。
- 不新增持久化、网络依赖、远程命令、sudo 或非回环监听。

## 兼容、回滚与 M4 接口

- 变更只新增内部接口、包、认证 API 和页面能力；M1/M2 现有响应字段与路由不变。
- 回滚顺序为页面操作、Web 路由、入口服务、`internal/tunnel`、SSH 转发接口；移除后 M1/M2 仍可独立运行。
- M4 可在 `failed` 监控事件上增加有界重连和计数，不需要替换 M3 的 ID、目标键、进程句柄或错误模型。
