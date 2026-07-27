# 项目管理 SSH Host 配置契约

## Scenario: 结构化 Host 存储、合并 Catalog 与 CRUD API

### 1. Scope / Trigger

- 触发范围：修改 `internal/configfile`、`internal/hostconfig`、`internal/sshconfig` 有效配置解析、Host CRUD API 或入口 Host Store 注入。
- 目标：在不改写 `~/.ssh/config`、不保存秘密的前提下，安全管理项目专属 Host，并为后续 OpenSSH 连接阶段提供受控配置。
- 当前边界：本契约覆盖 Host 配置基础；项目 Host 的 ControlMaster、分阶段跳板认证和页面表单由后续里程碑接入。

### 2. Signatures

- 路径：`hostconfig.DefaultPath() (string, error)`。
- Store：`NewFileStore(path) (*FileStore, error)`、`List()`、`Create(Profile)`、`Update(alias, Profile)`、`Delete(alias)`。
- Catalog：`NewCatalog(ctx, systemPath, store)`、`Refresh(ctx)`、`Snapshot()`、`Has(alias)`、`Managed(alias)`、`ReferencedBy(alias)`、CRUD 和 `Render()`。
- OpenSSH：`sshconfig.EffectiveResolver.Resolve(ctx, configPath, alias)`，等价执行参数数组 `ssh -G -F <path> -- <alias>`，不发起网络连接。
- HTTP：`GET/POST /api/ssh-hosts`、`POST /api/ssh-hosts/refresh`、`PUT/DELETE /api/ssh-hosts/{alias}`。

### 3. Contracts

- `${XDG_CONFIG_HOME:-~/.config}/ssh-tunnel-manager/hosts.json` 为版本 1 JSON，目录 `0700`、文件 `0600`，最大 1 MiB、最多 256 个 Host。
- `Profile` 只含 `alias`、`hostName`、`port`、`username`、可选 `identityFile` 和可选 `jumpHost`；不得加入密码、私钥口令或私钥内容。
- JSON 拒绝未知字段、尾随值、未知版本、重复 Alias 和非法字段；损坏文件保持原样，Store 转为只读不可用，Catalog 仍返回系统 Host。
- Alias 创建后不可修改，不得与系统显式 Host 或其他项目 Host 重名。项目跳板自身不得再使用跳板；系统跳板必须由 `ssh -G` 证明没有有效 `ProxyJump` 或 `ProxyCommand`。
- Catalog 是系统/项目来源、引用有效性和 Host 存在性的唯一事实来源；页面和 HTTP handler 不得各自复制引用规则。
- OpenSSH 配置渲染只接收已验证的 Profile。路径和普通值使用配置引号编码；`ProxyJump` 只接受已验证 Alias 或 `none`，必须不带引号输出。
- 编辑和删除由服务端检查 SSH 状态、活跃隧道和跳板引用。用户名变更或删除 Host 前清理旧用户名下的 `password` 与 `passphrase`；失败则保留 Host 并返回错误。
- 所有业务路由继续受本地令牌 Cookie 保护；程序启动只加载配置，不连接、不探测、不恢复隧道。

### 4. Validation & Error Matrix

| 条件 | HTTP / 行为 |
|---|---|
| 请求 JSON 非法、未知字段、尾随值 | `400 invalid_request` |
| Host 字段、端口或私钥路径无效 | `400 invalid_host_config` |
| 跳板不存在、嵌套或系统跳板已有代理 | `400 invalid_jump_host` |
| Alias 与系统或项目 Host 冲突 | `409 host_conflict` |
| SSH 正在使用、活跃隧道或仍被引用 | `409 host_in_use` |
| 对系统 Host 执行 PUT/DELETE | `405 system_host_read_only` |
| 项目 Host 不存在 | `404 managed_host_not_found` |
| `hosts.json` 损坏、过大或 XDG 路径不可用 | `503 managed_config_unavailable`，原文件不覆盖 |
| Secret Service 清理失败 | `503 credential_cleanup_failed`，Host 保留 |

### 5. Good/Base/Bad Cases

- Good：系统 `Host ecs2` 与项目 `mac_home` 同时展示且来源明确；项目 Alias 冲突被拒绝，系统配置没有任何写入。
- Good：两个目标引用同一无跳板系统 Host，Catalog 只执行一次有效配置检查并将结果用于两个目标。
- Base：`hosts.json` 不存在时使用空项目配置，现有系统 Host 行为保持不变。
- Base：项目配置损坏时系统 Host 仍可查询，项目 CRUD 统一只读失败。
- Bad：把 HTTP 原始字段拼进 OpenSSH 文本、允许项目覆盖系统 Alias、损坏 JSON 后用空配置覆盖原文件，或只在页面禁用按钮而不做服务端门禁。

### 6. Tests Required

- `internal/configfile`：原子 JSON、目录/文件权限和空路径。
- `internal/hostconfig` Store：严格 JSON、版本、大小、数量、路径规范化、CRUD、并发、不可变 Alias、一层引用、损坏不覆盖。
- Catalog：来源合并、系统冲突、引用失效、系统跳板有效配置、共享检查结果和系统 Host 兜底。
- 渲染器：特殊路径引用、无跳板显式 `ProxyJump none`，并把结果交给真实 `ssh -G` 断言 HostName、Port、User 和 ProxyJump。
- Web：严格正文、稳定错误码、运行/隧道/引用门禁、用户名变更与删除凭据清理、系统 Host 只读。
- 全量：`gofmt -w ./cmd ./internal`、`go test -race ./...`、`go vet ./...`、`go build ./cmd/ssh-tunnel-manager`。

### 7. Wrong vs Correct

#### Wrong

```go
writeOption(output, "ProxyJump", profile.JumpHost) // 输出 ProxyJump "jump"
```

OpenSSH 9.6 的 `ssh -G` 会把这里的引号保留在 ProxyJump 有效值中，导致后续按错误 Alias 解析。

#### Correct

```go
// JumpHost 已通过 ValidateAlias，不含空白、通配符或参数前缀。
output.WriteString("    ProxyJump ")
output.WriteString(profile.JumpHost)
output.WriteByte('\n')
```

配置渲染测试必须继续经过真实 `ssh -G`；字符串包含断言只能验证文本形状，不能替代 OpenSSH 语义验证。

## Scenario: M3 Web Host 管理与连接挑战

### 1. Scope / Trigger

- 触发范围：修改 `internal/web/page.go` 的 Host 表格、结构化对话框或连接挑战状态机。
- 目标：页面只消费 Catalog 和 SSH API 的稳定字段，完成项目 Host CRUD 与跳板/目标分阶段交互。

### 2. Signatures

- 页面读取：`GET /api/ssh-hosts`、`GET /api/servers/{host}`。
- 页面写入：`POST /api/ssh-hosts`、`PUT /api/ssh-hosts/{alias}`、`DELETE /api/ssh-hosts/{alias}`。
- 页面连接：`POST /api/servers/{host}/connect`，请求可含 `stageHost`、`confirmFingerprint`、当前阶段凭据。

### 3. Contracts

- Host 表格展示 `source`、`hostName/port`、`jumpHost`、`valid/issue`；系统 Host 不显示编辑和删除入口，项目 Alias 编辑时只读。
- 跳板选择只允许 `valid=true`、不是当前 Host 且没有 `jumpHost` 的 Catalog 项；服务端仍是最终校验边界。
- `credential_required` 挑战只打开当前 `details.stageHost` 的凭据对话框；`host_key_confirmation_required` 只展示 `details.stageHost` 与 `details.fingerprint`。
- 每次重试只提交当前阶段字段；若同一阶段先后出现凭据和指纹挑战，必须保留尚未保存的凭据在内存请求体中，不能写入页面持久化存储。
- 取消挑战结束当前连接循环并刷新页面，不触发断开已存在的跳板或隧道；`host_key_changed` 和引用失效直接显示服务端稳定错误。
- 删除确认必须明确说明会清理该 Host 的密码和私钥口令；删除 API 失败时保留页面状态并展示错误。

### 4. Validation & Error Matrix

| 条件 | 页面行为 |
|---|---|
| 表单缺少地址、端口、用户名或端口越界 | 浏览器表单阻止提交 |
| `host_conflict`、`invalid_host_config`、`invalid_jump_host` | 保留服务端中文错误，不重试 |
| `credential_required` | 按 `stageHost` 询问凭据后重试 |
| `host_key_confirmation_required` | 显示指纹，确认后以 `confirmFingerprint` 重试 |
| `host_key_changed`、`host_reference_broken`、`host_in_use` | 直接提示并结束当前操作 |

### 5. Good/Base/Bad Cases

- Good：先为 `mac_home` 保存公网地址，再为 `ssh_ubuntu_home` 选择 `mac_home`，页面分别处理两阶段挑战。
- Base：只有系统 Host 时仍可连接、探测端口和创建隧道，页面不出现项目写操作。
- Bad：页面自行判断跳板循环、把秘密放入 `localStorage`、把原始 SSH 诊断插入 DOM，或在页面关闭时调用断开接口。

### 6. Tests Required

- 页面模板契约断言新增/编辑/删除控件、来源/目标/跳板列、单层筛选和 `stageHost`/指纹字段。
- Web API 测试断言挑战响应只含 `stageHost`/指纹，不含密码、口令或诊断；未知字段和尾随 JSON 仍返回 `400 invalid_request`。
- 使用 Chrome 桌面与移动视口检查表格横向滚动、对话框按钮和错误文本没有重叠；页面关闭不得产生断开请求。
- 继续执行 `gofmt -w ./cmd ./internal`、`go test -race ./...`、`go vet ./...`、`go build ./cmd/ssh-tunnel-manager`。

### 7. Wrong vs Correct

#### Wrong

```javascript
localStorage.setItem('sshPassword', password);
payload = { confirmFingerprint: fingerprint };
```

前者把秘密持久化，后者在凭据后出现指纹挑战时丢失当前阶段凭据。

#### Correct

```javascript
payload = Object.assign({}, payload, {
  stageHost: details.stageHost,
  confirmFingerprint: fingerprint,
});
```

秘密只留在当前 Promise 和请求体内存中，并与阶段指纹确认一起提交。
