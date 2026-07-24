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

端口转发使用等价于以下命令的参数：

```bash
ssh -N -T \
  -o ExitOnForwardFailure=yes \
  -o ServerAliveInterval=30 \
  -o ServerAliveCountMax=3 \
  -L 127.0.0.1:<local-port>:127.0.0.1:<remote-port> \
  <host-alias>
```

真实实现必须使用参数数组，不得拼接 shell 字符串；主机别名、端口和路径都要经过类型校验。

M1 已实现每台服务器一个长期 ControlMaster 主连接：

```bash
ssh -M -N -T \
  -o ControlMaster=yes \
  -o ControlPersist=no \
  -o ControlPath=<程序专用运行时路径> \
  <host-alias>
```

连接只有在 `ssh -S <control-path> -O check <host-alias>` 成功后才进入已连接状态。断开时优先执行精确的 `-O exit`，随后只通过程序保存的进程句柄进行有界清理。程序不设置 `StrictHostKeyChecking=no`，未知或变化的主机密钥继续由用户的 OpenSSH 配置和 `known_hosts` 策略处理。

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

## 5. 状态模型

- `Server`: SSH Host 别名、连接状态、最近错误、端口列表。
- `RemotePort`: 远程端口、进程名、最后探测时间。
- `Tunnel`: 服务器别名、远程端口、本地端口、SSH 进程句柄、状态、重连信息。
- `CredentialRef`: 密钥环服务名和账户标识，不保存秘密内容。
- `Preference`: Web 端口、刷新策略、端口分配策略和界面偏好。

## 6. 数据持久化

- Linux Secret Service：服务器密码、私钥口令。
- `${XDG_CONFIG_HOME:-~/.config}/ssh-tunnel-manager/config.json`：非敏感偏好。
- `${XDG_STATE_HOME:-~/.local/state}/ssh-tunnel-manager/`：运行日志和诊断信息，权限收紧为用户可读写。
- 不复制或重写 `~/.ssh/config`，不存储完整私钥内容。

## 7. HTTP API 草案

API 仅监听回环地址，所有业务路由都要求令牌 Cookie：

```text
GET  /api/ssh-hosts
POST /api/ssh-hosts/refresh
POST /api/servers/:host/connect
POST /api/servers/:host/disconnect
GET  /api/servers/:host
GET  /api/servers/:host/ports
POST /api/servers/:host/ports/refresh
POST /api/tunnels
GET  /api/tunnels
DELETE /api/tunnels/:id
GET  /api/tunnels/:id/logs
```

写操作应支持请求幂等标识，错误返回结构化错误码；日志 API 必须脱敏。

M1 已实现 Host 刷新、连接、断开和状态查询。端口及隧道相关路由仍属于后续里程碑。
