# 技术架构

## 1. 总体结构

```text
浏览器
  │ HTTP（仅 127.0.0.1，随机令牌）
  ▼
本地 Web API / 静态页面
  │
  ├── SSH 配置读取器
  ├── SSH 会话管理器
  ├── 远程端口探测器
  ├── 隧道生命周期管理器
  ├── 本地端口分配器
  ├── 自动重连协调器
  ├── 凭据存储（Secret Service）
  └── 偏好设置存储（XDG 配置目录）
       │
       ▼
     系统 OpenSSH 子进程
       │
       ▼
     远程 Linux 服务器
```

## 2. 进程模型

第一版使用单进程模型：Go 进程同时负责 HTTP 服务、状态管理和 SSH 子进程。每个服务器连接与端口隧道都由明确的句柄管理，不使用全局 `pkill` 或模糊进程匹配。

浏览器刷新或关闭不会影响 Go 进程。程序退出时执行有界的优雅清理，并在异常退出后依靠操作系统回收子进程和监听端口。

## 3. OpenSSH 调用

系统 OpenSSH 是唯一的 SSH 协议实现，原因是它天然兼容用户现有配置：

- `~/.ssh/config`；
- `IdentityFile` 和 `ssh-agent`；
- `ProxyJump`；
- `known_hosts`；
- 服务器密码与私钥口令交互。

M3 的端口转发复用已经就绪的 ControlMaster，使用等价于以下命令的参数：

```bash
ssh -S <control-path> -N -T \
  -o BatchMode=yes \
  -o ExitOnForwardFailure=yes \
  -L 127.0.0.1:<local-port>:127.0.0.1:<remote-port> \
  -- <host-alias>
```

真实实现必须使用参数数组，不得拼接 shell 字符串；主机别名、端口和路径都要经过类型校验。转发前后均执行精确的 ControlMaster check，避免主连接消失时退化为新的独立 SSH 连接。

M1 已实现每台服务器一个长期 ControlMaster 主连接：

```bash
ssh -M -N -T \
  -o ControlMaster=yes \
  -o ControlPersist=no \
  -o ControlPath=<程序专用运行时路径> \
  <host-alias>
```

连接只有在 `ssh -S <control-path> -O check <host-alias>` 成功后才进入已连接状态。断开时优先执行精确的 `-O exit`，随后只通过程序保存的进程句柄进行有界清理。程序不设置 `StrictHostKeyChecking=no`，未知或变化的主机密钥继续由用户的 OpenSSH 配置和 `known_hosts` 策略处理。

M2 的远程命令只复用已经连接的 ControlMaster：

```bash
ssh -S <control-path> -T -o BatchMode=yes -- <host-alias> ss -ltnp
```

`--` 位于 Host 之前，只用于终止本地 OpenSSH 选项解析。真实实现限制远程参数字符并使用参数数组，捕获的标准输出和错误各自最多保留 1 MiB。

密码和私钥口令通过受限权限的临时 askpass helper 与命名管道传递，不出现在命令参数和环境变量中，也不写入普通文件。用户明确选择保存时，通过内嵌 D-Bus 客户端写入 Linux Secret Service；密钥环不可用时安全失败，不降级为明文文件。

## 4. 远程端口探测

优先执行：

```bash
ss -ltnp
```

如果没有权限查看进程信息，退化为：

```bash
ss -ltn
```

解析结果时只保留 TCP `LISTEN` 项，展示端口和尽力获取的进程名。MVP 默认代理目标统一使用远程 `127.0.0.1:<port>`，复杂地址作为后续高级能力。

端口发现状态仅保存在内存中。同一 Host 的并发刷新共享一次正在进行的探测，不同 Host 可以并行；失败不会清空最后一次成功列表。自动刷新默认关闭，用户启用后由服务端每 10 秒执行一次，浏览器关闭不改变该状态，程序退出时先取消刷新任务再关闭 SSH 主连接。

## 5. 本地隧道管理

`internal/tunnel` 是隧道 ID、目标幂等键、本地端口预留、进程句柄和状态快照的唯一所有者。本地端口先尝试远程同号端口；不可用时由操作系统选择其他 `127.0.0.1` 端口，并在进程内预留以避免并发创建互相争用。OpenSSH 实际绑定与预检之间仍可能发生外部竞争，只有明确的本地绑定冲突会触发有界重试。

同一 Host 和远程端口的并发创建收敛到同一条隧道，不同目标可以并行。每条隧道只由一个监控 goroutine 调用 `Wait`；停止时先对保存的精确进程句柄发送中断，有界等待后才升级为强制终止。

隧道意外退出后保留相同 ID，并按 1、2、4、8、16 秒执行最多 5 次自动重连。转发进程单独退出且主连接仍有效时只重建该转发；ControlMaster 已断开时，同一 Host 的隧道共享一次无交互主连接恢复。认证、主机密钥、配置、依赖或缺少交互凭据等错误立即转为人工处理。恢复后稳定运行满 60 秒，下一次故障才重新获得完整额度。

每条隧道在内存中保留最多 100 条、文本总量不超过 64 KiB 的生命周期事件和脱敏 SSH 诊断。停止隧道或程序退出后删除，不写磁盘。控制台通过独立日志 API 按需读取，普通隧道列表不内联诊断。

页面每轮只获取一次隧道列表，再按 Host 和远程端口映射到端口表，同时保留独立隧道总览。浏览器刷新或关闭不改变服务端隧道。显式断开 Host 时按“停止该 Host 隧道 → 关闭自动刷新 → 断开 ControlMaster”执行；程序退出按“全部隧道 → 端口发现 → SSH → HTTP”清理。

## 6. 状态模型

- `Server`: SSH Host 别名、连接状态、最近错误、端口列表。
- `RemotePort`: 远程端口、进程名、最后探测时间。
- `Tunnel`: 随机 ID、服务器别名、远程端口、本地端口、SSH 进程句柄、状态、安全错误、连续运行起始时间、累计重连次数、下一次重试时间和有界内存日志。
- `CredentialRef`: 密钥环服务名和账户标识，不保存秘密内容。
- `Preference`: 当前只保存每个 Host 的端口自动刷新开关。

## 7. 数据持久化

- Linux Secret Service：服务器密码、私钥口令。
- `${XDG_CONFIG_HOME:-~/.config}/ssh-tunnel-manager/config.json`：版本化的非敏感自动刷新偏好，目录 `0700`、文件 `0600`、原子写入。
- 隧道事件和 SSH 诊断仅在当前进程内存中有界保存，不写入 XDG state 目录。
- 不复制或重写 `~/.ssh/config`，不存储完整私钥内容。

## 8. HTTP API

API 仅监听回环地址，所有业务路由都要求令牌 Cookie：

```text
GET  /api/ssh-hosts
POST /api/ssh-hosts/refresh
POST /api/servers/{host}/connect
POST /api/servers/{host}/disconnect
GET  /api/servers/{host}
GET  /api/servers/{host}/ports
POST /api/servers/{host}/ports/refresh
PUT  /api/servers/{host}/ports/auto-refresh
POST /api/tunnels
GET  /api/tunnels
DELETE /api/tunnels/{id}
GET  /api/tunnels/{id}/logs
```

写操作应支持请求幂等标识，错误返回结构化错误码；日志 API 必须脱敏。

M1 已实现 Host 刷新、连接、断开和状态查询；M2 已实现端口查询、手动刷新和自动刷新切换；M3 已实现隧道创建、列表和幂等停止；M4 已实现有界自动重连、状态时间、偏好持久化和脱敏日志 API。客户端请求幂等标识仍属于后续工作。
