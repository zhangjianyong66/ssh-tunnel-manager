# 页面管理 SSH 跳板机配置：技术设计

## 1. 设计目标

本设计在现有 Go 单进程、标准库优先和系统 OpenSSH 唯一协议实现的边界内，增加项目专属 Host 管理、一层跳板、分阶段独立凭据和首次主机指纹确认。

不修改 `~/.ssh/config`，不自行实现 SSH 协议，不把密码、私钥口令或私钥内容写入普通文件。

## 2. 总体架构

新增 `internal/hostconfig` 作为项目 Host 数据的唯一所有者，现有 `internal/sshconfig` 继续负责系统配置解析，并增加基于 `ssh -G` 的有效配置检查。`internal/ssh` 负责按 Host 依赖顺序建立 ControlMaster，`internal/web` 只做 HTTP 契约、运行状态门禁和页面交互。

```text
结构化表单
  -> Web API 解码
  -> hostconfig 统一校验
  -> XDG hosts.json 原子持久化
  -> 合并系统 Host 与项目 Host 的 Catalog
  -> 为本次连接渲染临时 ssh_config
  -> ssh -G 解析有效参数
  -> 先建立跳板 ControlMaster
  -> 目标 ProxyJump 复用跳板 ControlMaster
  -> 端口发现与隧道继续复用目标 ControlMaster
```

### 包职责

| 包 | 新增或调整职责 |
|---|---|
| `internal/configfile` | 提供权限受控、同步并原子重命名的 JSON 写入，供偏好与 Host 配置复用。 |
| `internal/hostconfig` | 项目 Host 模型、版本化 JSON、字段与引用校验、系统/项目 Host 合并、OpenSSH 配置安全渲染。 |
| `internal/sshconfig` | 保留系统 Host/Include 解析；新增 `ssh -G` 有效配置解析，用 OpenSSH 自身判断系统 Host 的用户、地址、身份文件和已有跳板。 |
| `internal/credential` | 继续按 Host、用户名、用途管理 Secret Service；为编辑用户名和删除 Host 提供明确清理结果。 |
| `internal/ssh` | 每个会话保存自己的临时配置路径；所有 check、exit、execute、forward 均使用同一 `-F`；按一层依赖分阶段连接。 |
| `internal/web` | 合并 Host API、CRUD、运行状态门禁、分阶段凭据与指纹挑战、页面状态机。 |
| `cmd/ssh-tunnel-manager` | 初始化 Host Store/Catalog，注入 SSH Manager 与 Web App，保持退出清理顺序。 |

## 3. 数据模型与持久化

### 项目 Host

```go
type Profile struct {
    Alias        string `json:"alias"`
    HostName     string `json:"hostName"`
    Port         uint16 `json:"port"`
    Username     string `json:"username"`
    IdentityFile string `json:"identityFile,omitempty"`
    JumpHost     string `json:"jumpHost,omitempty"`
}
```

持久化文件建议为 `${XDG_CONFIG_HOME:-~/.config}/ssh-tunnel-manager/hosts.json`：

```json
{
  "version": 1,
  "hosts": [
    {
      "alias": "mac_home",
      "hostName": "frp.example.com",
      "port": 60022,
      "username": "<用户填写>"
    },
    {
      "alias": "ssh_ubuntu_home",
      "hostName": "192.0.2.210",
      "port": 22,
      "username": "<用户填写>",
      "jumpHost": "mac_home"
    }
  ]
}
```

约束：

- 最大文件 1 MiB，最多 256 个 Host；JSON 拒绝未知字段、尾随值和未知版本。
- Alias 使用一个共享校验函数，拒绝空值、参数前缀、空白、控制字符和 OpenSSH 通配符。
- HostName 只接受单个域名、IPv4 或 IPv6 地址，不接受 URI、空白和配置分隔字符。
- Port 范围 `1..65535`；Username 必填且拒绝空白、控制字符和配置分隔字符。
- IdentityFile 接受 `~` 或绝对路径，保存前规范化为绝对路径；必须存在、为普通文件且当前进程可访问。只检查元数据，不读取内容。
- JumpHost 必须存在、不能等于自身；项目跳板自身不得再配置 JumpHost。系统跳板通过 `ssh -G` 确认不存在有效 `ProxyJump`/`ProxyCommand`。
- 项目 Alias 与系统显式 Host 冲突时拒绝创建或保存。

Store 在损坏配置时返回可用的只读空快照和 `loadErr`。Web 继续展示系统 Host，但项目 Host CRUD 返回稳定错误，避免覆盖原文件。

### 共享原子写入

提取现有 `internal/preference` 的同目录临时文件、`0600`、文件同步、原子重命名和目录同步逻辑到 `internal/configfile`。偏好 Store 与 Host Store 共用这一实现，JSON 解码与业务校验仍由各自包负责。

## 4. Catalog 与 OpenSSH 配置

Catalog 输出统一的 `HostView`：

```go
type HostView struct {
    Alias        string `json:"alias"`
    Source       string `json:"source"` // system | managed
    Editable     bool   `json:"editable"`
    HostName     string `json:"hostName,omitempty"`
    Port         uint16 `json:"port,omitempty"`
    Username     string `json:"username,omitempty"`
    IdentityFile string `json:"identityFile,omitempty"`
    JumpHost     string `json:"jumpHost,omitempty"`
    Valid        bool   `json:"valid"`
    Issue        string `json:"issue,omitempty"`
}
```

系统 Host 保留当前来源文件信息，但不可编辑。项目 Host 返回结构化字段。Catalog 是别名冲突、引用有效性和 Web `hostExists` 的唯一事实来源，页面不得自行重建规则。

每次建立 ControlMaster 时，在该会话 `0700` 运行目录内生成 `0600` 的 `ssh_config`，并始终以绝对路径传给 `-F`：

1. 写入所有项目 Host 的明确 `Host`、`HostName`、`Port`、`User`、可选 `IdentityFile` 和 `ProxyJump`。
2. 对未使用跳板的项目 Host 明确写入 `ProxyJump none`，防止系统 `Host *` 意外注入跳板。
3. 最后使用绝对且安全编码的路径 `Include ~/.ssh/config`，继续继承系统全局选项与系统 Host。
4. 项目 Host 位于 Include 之前，利用 OpenSSH“首个获得的值生效”的规则固定结构化字段；重复别名已在 Store 层拒绝。

配置渲染器只接受已验证的类型，不接收 HTTP 原始字符串；双引号和反斜线由单一编码函数处理。连接前执行 `ssh -G -F <session-config> -- <alias>`，解析 OpenSSH 实际采用的 HostName、Port、User、IdentityFile、ProxyJump 和 ProxyCommand，作为连接前最后一道配置检查。

## 5. 分阶段跳板连接

### 连接顺序

```text
POST connect(ssh_ubuntu_home)
  -> resolve: jump=mac_home, target=ssh_ubuntu_home
  -> Connect(mac_home)
       -> 未知指纹挑战 / 缺少凭据挑战 / connected
  -> 取得 mac_home ControlPath
  -> 为目标会话生成 overlay：mac_home 使用现有 ControlPath
  -> Connect(ssh_ubuntu_home via ProxyJump mac_home)
       -> 未知指纹挑战 / 缺少凭据挑战 / connected
  -> 返回 ssh_ubuntu_home connected
```

跳板 Host 使用普通 ControlMaster 连接，因此其凭据、指纹、状态和诊断与普通服务器一致。目标会话的临时配置在跳板 Host 段前置以下覆盖值：

- `ControlMaster auto`
- `ControlPath <已连接跳板的精确 control path>`

目标 OpenSSH 的 ProxyJump 子进程因此复用已认证的跳板 ControlMaster，不再次询问跳板凭据；目标阶段只处理目标凭据。不得生成含用户原始字符串的 ProxyCommand shell 命令。

规划阶段已用 OpenSSH 9.6 的不可达本地地址验证：ProxyJump 子进程继承同一绝对 `-F` 配置，并执行 `auto-mux` 查找配置中的跳板 ControlPath。里程碑 2 仍需以自动化参数测试固化该契约。

如果跳板已连接则直接复用。目标失败后跳板保持连接并显示在列表中，用户可继续排查或供其他目标复用。显式断开跳板时，如仍有已连接目标依赖它，返回 `host_in_use`；程序退出按依赖目标在前、跳板在后的顺序关闭会话。

自动重连目标时沿用同一依赖流程：先恢复跳板，再恢复目标。任何阶段出现认证、主机密钥或配置挑战时，停止自动退避并转人工处理。

## 6. 凭据挑战

连接请求每次只携带当前阶段的一组秘密：

```json
{
  "credentials": {
    "host": "mac_home",
    "username": "alice",
    "password": "...",
    "passphrase": "...",
    "savePassword": true,
    "savePassphrase": false
  }
}
```

初次请求体可为 `{}`。若 OpenSSH 在当前阶段返回认证提示且没有可用凭据，响应使用 `credential_required`，并只返回安全字段 `stageHost`、有效用户名和需要的凭据类型。页面提交该阶段凭据后重试同一目标；跳板成功后才可能进入目标阶段的第二次挑战。

现有 askpass 仍保持单阶段的一份密码与一份私钥口令，秘密通过 `0600` 命名管道提供。因为每个 SSH 进程只认证当前阶段，不需要根据提示猜测秘密属于跳板还是目标。

Username 变更或 Host 删除时，先查找并删除旧用户名下的 `password` 与 `passphrase`。`ErrNotFound` 视为已清理；Secret Service 不可用或部分删除失败时保留 Host 配置并返回可重试错误。若秘密已部分删除，响应明确提示需要重新输入，不回显秘密。

## 7. 主机密钥挑战

扩展 askpass helper 处理 OpenSSH 的未知主机确认提示：

- 默认记录非敏感提示到 `0700` 临时目录中的 `0600` 文件并回答 `no`。
- Manager 解析并校验 `SHA256:<base64>` 指纹，返回 `host_key_confirmation_required`、`stageHost` 和 `fingerprint`。
- 用户确认后重试并携带 `stageHost + fingerprint`；helper 仅在本次单 Host 阶段的新提示包含完全相同指纹时回答 `yes`。
- 指纹不匹配继续回答 `no`，防止确认与重试之间的目标变化被静默接受。
- `REMOTE HOST IDENTIFICATION HAS CHANGED` 始终映射为 `host_key_changed`，不进入确认流程。

跳板与目标按连接顺序分别确认。临时提示文件随 askpass 目录删除，不进入日志和普通配置。

## 8. HTTP API

### Host 管理

| 方法 | 路径 | 行为 |
|---|---|---|
| `GET` | `/api/ssh-hosts` | 返回合并 Catalog、诊断和配置 Store 状态。 |
| `POST` | `/api/ssh-hosts` | 新增项目 Host；拒绝未知字段和重名。 |
| `PUT` | `/api/ssh-hosts/{alias}` | 编辑可变字段；请求体不含 Alias。 |
| `DELETE` | `/api/ssh-hosts/{alias}` | 删除项目 Host并清理凭据；只读 Host 返回 405/稳定业务错误。 |
| `POST` | `/api/ssh-hosts/refresh` | 重读系统配置并重新计算引用有效性。 |

创建成功返回 `201`，编辑返回 `200`，删除返回 `204`。所有写请求限制正文大小、拒绝尾随 JSON 和未知字段。稳定错误至少包括：`host_conflict`、`invalid_host_config`、`invalid_jump_host`、`host_reference_broken`、`host_in_use`、`managed_config_unavailable`、`credential_cleanup_failed`。

### 连接挑战

保留 `POST /api/servers/{host}/connect`，扩展请求体为当前阶段凭据或指纹确认。挑战错误响应增加安全的 `details` 对象，不把原始 SSH 提示和秘密返回浏览器。

稳定代码增加 `host_key_confirmation_required` 与 `host_key_changed`；原有 `credential_required`、`authentication_failed`、网络、超时、取消和依赖错误继续兼容。

## 9. 并发与生命周期门禁

Web App 对 Host 配置写操作、连接开始、断开和隧道创建使用统一的 Host 操作协调锁，避免“检查后删除”和“删除后开始连接”的竞态。Store 自身仍有独立互斥锁保证内存快照与文件一致。

编辑或删除前由服务端检查：

- 本 Host 是否 connecting、connected 或 disconnecting；
- 本 Host 是否存在 starting、running、waiting_reconnect、reconnecting 或 stopping 隧道；
- 本 Host 是否被项目配置引用为跳板；
- 作为跳板时是否存在已连接依赖目标。

页面禁用按钮仅用于体验，服务端检查才是安全边界。

## 10. 页面交互

- 服务器表增加“来源、目标、跳板”列；系统 Host 标为“系统”，项目 Host 标为“项目”。
- 顶部提供“添加主机”命令；项目 Host 行提供编辑和删除操作，系统 Host 不显示写操作。
- 新增/编辑使用结构化对话框：别名（编辑时只读）、地址、端口、用户名、私钥路径、使用跳板开关与 Host 下拉框。
- 跳板下拉框过滤自身、无效 Host 和已经使用跳板的 Host，并标明来源。
- 删除对话框明确说明会清理系统密钥环凭据。
- 连接状态机按 `stageHost` 显示凭据或指纹对话框；用户取消只取消当前连接，不影响已有跳板或隧道。
- 所有错误使用服务端稳定代码映射成简短中文，不展示原始 JSON、堆栈或未脱敏 SSH 输出。

## 11. 兼容、迁移与回滚

- 没有 `hosts.json` 时行为与当前版本相同，只展示系统 Host。
- 现有自动刷新偏好文件格式不变；原子写入辅助函数重构必须通过原有测试。
- 现有系统 Host API 字段保持兼容，只增加来源和可编辑等字段。
- 运行中的会话冻结各自临时配置，配置变更不会改变已经启动的 SSH 进程。
- 回滚到旧版本不会读取 `hosts.json`，也不会损坏它；用户系统 SSH 配置和 `known_hosts` 保持 OpenSSH 原生格式。

## 12. 主要取舍

- 选择项目专属 JSON + 每会话临时 OpenSSH 配置，而不是改写 `~/.ssh/config`：牺牲终端别名自动可用，换取可回滚和不破坏手写配置。
- 选择先连接跳板 ControlMaster 再连接目标，而不是一个进程同时处理两套凭据：多一个明确生命周期，但凭据、指纹和错误归属可靠。
- 选择单层跳板而不是任意链：满足当前场景并限制循环、断开顺序和自动重连复杂度。
- 选择 `ssh -G` 解析系统有效配置，而不是在 Go 中复刻完整 OpenSSH 匹配语义。
