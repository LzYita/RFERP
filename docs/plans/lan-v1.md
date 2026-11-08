# RFERP 局域网初版方案（v1.0）

**状态**：已定稿（待实施）
**日期**：2026-10-29
**基线**：`main` @ `646b5f0`，schema v17，API v1
**关联**：`docs/plans/lan-v1-implementation.md`（实施记录）、`docs/plans/av-false-positive-remediation.md`（杀软误报修复流程）

> 本文件是执行基线：**所有决策点按 2026-10-29 评审建议锁定**；过程稿里的备选项不再展开。
> 后续如需变更，在 §11 决策台账里追加变更记录，不直接改口径。

---

## 0. 摘要

局域网初版面向 **3 台 / 5 台设备** 的小微客户：

- 客户用一台普通 Windows 设备作**主机**，跑 `RFERP-server` + SQLite；
- 同一局域网内的其他设备装 `RFERP.exe`，作为**客户端**连接主机；
- 授权按**客户端设备数**计，主机豁免；
- 不做 VPN、公网、浏览器版、MySQL、TLS。

目标是在 **4–5 周开发 + 2 周试点** 内交付一个可复制安装、可授权、可备份恢复的局域网版本。

---

## 1. 目标与范围

### 1.1 目标

1. 3 台设备（1 主机 + 2 客户端）和 5 台设备（1 主机 + 4 客户端）能在同一局域网稳定协同。
2. 客户管理员可在额度内管理账号；不能改授权额度。
3. 主机安装可由非开发人员按文档完成；客户端首启能连上主机。
4. 每日自动备份；可从备份恢复。
5. 公网无法访问主机 8080。

### 1.2 成功标准

- 3 台设备日常入库/出库/追溯/审计无阻塞。
- 第 3 个客户端被拒绝，撤销一个后可注册。
- 主机重启后服务自动启动，客户端能重连。
- 备份/恢复演练通过，数据一致。
- 客户按文档 30 分钟内完成主机 + 1 台客户端安装。

### 1.3 不做的范围

- VPN、公网访问、TLS/Caddy、域名/备案。
- 浏览器版、手机 App。
- MySQL、多实例、HA、读写分离。
- 多子网/VLAN 自动支持（按定制处理）。
- mDNS/自动发现（先手填 IP）。
- 远程支持通道（后置）。
- 多租户 SaaS、离线写入/冲突合并。
- 订阅制计费（字段预留，不启用）。

---

## 2. 锁定口径

| 项 | 锁定值 |
|---|---|
| 3 台版 | 1 主机 + 2 客户端，`maxClients=2` |
| 5 台版 | 1 主机 + 4 客户端，`maxClients=4` |
| 主机 | **不计入授权**；主机 UI 连 `127.0.0.1` 也按 host |
| 授权单位 | **客户端设备**；账号数不设硬上限 |
| 数据库 | **SQLite 唯一**；本版不做 MySQL |
| 服务器形态 | 无头 `RFERP-server` 服务 + 客户端 UI |
| 服务包装 | **SCM 原生**（`internal/svcrun`；2026-11-06 由 WinSW v2 变更） |
| 监听 | `0.0.0.0:8080`；冲突时提示改 `18080` |
| 传输 | 受控局域网 HTTP；TLS 后置 |
| 网络 | 同一网段；关闭 AP 隔离；防火墙只放行 `LocalSubnet` |
| 地址 | 路由器 DHCP 保留 + 客户端手填 IP；不做 mDNS |
| 会话 | 内存会话；服务重启后客户端重新登录 |
| 备份 | 每日自动 + 手动；保留 7 日 + 4 周；外部盘手工复制 |
| 恢复 | 仅主机 CLI/停服务；客户端不开放 |
| 日志 | 保留 180 天 |
| 授权模式 | 永久授权 + 1 年支持；trial 30 天；订阅不启用 |
| 无授权时 | 仅主机可登录；客户端被拒，直到导入授权 |
| 超额 | 已登记设备可登录，新设备拒绝 |
| 到期 | 仅 trial/订阅到期转只读；永久授权不因支持到期停用 |
| 试用 | 销售签发 3 客户端/30 天 |
| 支持 | 无远程接入；客户提供日志/截图；支持 VPN 后置 |
| 主机在线 | 不承诺 24×7；客户负责供电/网络/主机 |

---

## 3. 架构

### 3.1 拓扑

```
主机（客户 Windows 设备，Windows 10/11 64 位）
  RFERP-server（Windows 服务，SCM 原生）
    ├─ SQLite: <dataDir>\rferp.db
    ├─ 监听 0.0.0.0:8080（防火墙仅 LocalSubnet）
    ├─ 每日自动备份到 <dataDir>\备份\auto
    └─ 日志 <dataDir>\日志

客户端 A ──┐
客户端 B ──┼── 同一路由器/交换机 ── http://192.168.1.10:8080
客户端 C ──┘
```

### 3.2 组件

| 组件 | 职责 |
|---|---|
| `RFERP-server.exe` | API、SQLite、迁移、授权、设备管理、备份、日志 |
| `RFERP.exe` | Wails 客户端；`ModeClient` 时用 `api.Client` 连主机 |
| `internal/svcrun` | 让同一入口既能被 SCM 当服务启动，也能当控制台程序跑 |
| `rferp-license.exe` | 内部签发工具；不随客户交付 |
| 主机客户端 | 主机上可选装；连 `127.0.0.1:8080`，按 host 处理，不占授权 |

### 3.3 数据流

```
RFERP.exe UI
  → 本地 127.0.0.1 内嵌 API（带启动 token）
  → api.Client（带 X-RFERP-Device-* 头）
  → http://主机IP:8080/api/...
  → RFERP-server
  → SQLite
```

### 3.4 配置隔离（必须）

- 服务器 config 与客户端 config **分开文件**：
  - 服务器：`-config C:\ProgramData\RFERP\server.json`
  - 客户端：`-config %APPDATA%\RFERP\client.json`（或 `RFERP_CONFIG` 环境变量）
- 服务器选库只看 `storage`，**不看 `mode`**；客户端 `mode=client` 不应影响服务器启动。
- 主机上同时装服务端和客户端时，不得让两者读同一份 config。

---

## 4. 功能需求

### 4.1 主机服务

| ID | 需求 | 验收 |
|---|---|---|
| FR-01 | 以 Windows 服务运行，开机自启、失败重启 | 重启主机后自动恢复 |
| FR-02 | SQLite 开启 WAL、`busy_timeout=5000`、`foreign_keys=ON` | 两客户端并发入库不锁死 |
| FR-03 | 启动时执行迁移；迁移加锁；单实例锁 | 两个进程只有一个能启动 |
| FR-04 | 启动日志打印所有 LAN IPv4 与访问地址 | 客户端能按日志地址连 |
| FR-05 | `/healthz` 存活、`/readyz` 检查 DB | 安装器/监控可探活 |
| FR-06 | `/` 返回欢迎 JSON（服务名/版本/提示） | 不含业务数据 |
| FR-07 | 每日自动备份 + 手动备份；保留 7 日 + 4 周 | 备份文件可下载/恢复 |
| FR-08 | 恢复/清空/改目录仅主机 CLI 或停服务操作 | 客户端 403 |
| FR-09 | 日志写 `<dataDir>\日志`，保留 180 天 | 日志可查 |

### 4.2 客户端模式

| ID | 需求 | 验收 |
|---|---|---|
| FR-10 | 首启可选“本机使用 / 连接局域网主机” | 两种模式都能进入 |
| FR-11 | 输入主机 IP，`api.Probe` 测试连接 | 地址错/端口不通有提示 |
| FR-12 | 连接后登录；请求头带设备信息 | 设备自动登记 |
| FR-13 | 断线提示、自动重连、禁止写入 | 恢复后能继续操作 |
| FR-14 | 远端 401 回登录页；403/409 显示服务端文案 | 不出现 500 |
| FR-15 | 版本不兼容阻断 | `APIVersion`/`minClientVersion` 检查 |
| FR-16 | 远程形态隐藏恢复/清空/改目录；备份改下载 | 界面不出现盘符 |
| FR-17 | 登录页显示服务器地址、可切换 | 换地址需确认 |

### 4.3 授权

| ID | 需求 | 验收 |
|---|---|---|
| FR-18 | License 为 Ed25519 签名的 JSON envelope | 篡改签名被拒 |
| FR-19 | 字段：`licenseId/customer/edition/kind/maxClients/issuedAt/supportUntil/expiresAt/features` | 导入后页面显示 |
| FR-20 | 永久授权不因 `supportUntil` 停用；trial 过期转只读 | 行为符合 |
| FR-21 | 无授权时仅主机可登录；客户端拒绝 | 导入后客户端可登录 |
| FR-22 | 超额：已登记可登录，新设备拒绝 | 提示「授权客户端数已满」 |
| FR-23 | 管理员可导入新授权；普通用户不可见 | 权限正确 |
| FR-24 | 设备数下降不自动踢设备，仅阻止新设备 | 超额只影响新增 |

### 4.4 设备管理

| ID | 需求 | 验收 |
|---|---|---|
| FR-25 | 主机启动时登记 `kind=host`，不占额度 | 主机始终可登录 |
| FR-26 | 客户端首次登录登记 `kind=client`，占 `maxClients` | 第 3 个被拒 |
| FR-27 | 设备指纹 = SHA-256(MachineGuid + 系统盘序列 + CPU) | 重装可撤销恢复 |
| FR-28 | 会话绑定设备；token 换机器使用被拒 | 401 |
| FR-29 | 管理员可撤销设备；撤销后会话立即失效 | 设备列表 |
| FR-30 | 主机 CLI `-devices list/revoke` 兜底 | 管理员被锁时可恢复 |
| FR-31 | 设备列表显示名称、类型、最后在线、状态 | 可读 |

### 4.5 备份与恢复

| ID | 需求 | 验收 |
|---|---|---|
| FR-32 | 每日自动备份（服务启动/定时器）；失败写日志 | 每天一份 |
| FR-33 | 备份分类：manual/auto/safety；保留策略 | 目录正确 |
| FR-34 | 客户端管理员可下载备份 | 文件名安全校验 |
| FR-35 | 恢复：停服务 → 替换/恢复 DB → 启动；生成 `pre_restore_` 副本 | 数据一致 |
| FR-36 | 恢复后用户、授权、设备记录保留（SQLite 快照随库） | 可登录 |

### 4.6 设置与错误

| ID | 需求 | 验收 |
|---|---|---|
| FR-37 | 设置页「授权/设备」显示额度、已用、列表、撤销、导入 | 管理员可见 |
| FR-38 | 登录页错误分网络/TLS/版本/密码四类 | 文案不同 |
| FR-39 | 连接状态条：已连接/重连中/断开 | 断线禁用写操作 |
| FR-40 | 关于页显示客户端版本、服务器版本、部署形态 | 可核对 |

### 4.7 安装与 CLI

| ID | 需求 | 验收 |
|---|---|---|
| FR-41 | 主机安装脚本：检查权限/端口/数据目录 → 装服务 → 防火墙 → 启动 → 打印 IP/令牌 | 非开发人员可完成 |
| FR-42 | 防火墙规则仅 `RemoteAddress=LocalSubnet` | 公网 8080 不通 |
| FR-43 | 客户端安装包；首启连接向导 | 可交付 |
| FR-44 | 服务端 CLI：`-version/-healthcheck/-config/-devices/-license/-restore/-create-admin` | 可运维 |

---

## 5. 非功能需求

| ID | 维度 | 目标 |
|---|---|---|
| NFR-01 | 性能 | 2–4 客户端；1 万行数据；列表 p95 <300ms；写 p95 <500ms |
| NFR-02 | 安全 | 登录限流/锁定；强密码（服务器模式 ≥10 位）；初始化令牌；设备绑定；防火墙 LocalSubnet |
| NFR-03 | 可靠性 | 服务自启/失败重启；每日备份；恢复演练；单实例/迁移锁 |
| NFR-04 | 可维护 | 日志 180 天；CLI 可查授权/设备；安装器可重复执行 |
| NFR-05 | 兼容 | Windows 10/11 64 位；WebView2；SQLite 文件可迁移 |
| NFR-06 | 数据 | SQLite 不放在网络共享/U 盘；`integrity_check` 可用 |
| NFR-07 | 时间 | 主机开 NTP；客户端/服务器同时区 |
| NFR-08 | 升级 | 客户端自动更新；服务端手动升级，升级前备份；`minClientVersion` 阻断旧版 |

---

## 6. 数据模型与迁移

### 6.1 新增表

**`devices`（v18）**

```sql
CREATE TABLE IF NOT EXISTS devices (
  id             INTEGER PRIMARY KEY,
  device_id_hash TEXT    NOT NULL UNIQUE,
  name           TEXT    NOT NULL DEFAULT '',
  kind           TEXT    NOT NULL,              -- host / client
  status         TEXT    NOT NULL DEFAULT 'active', -- active / revoked
  user_id        INTEGER,
  app_version    TEXT    NOT NULL DEFAULT '',
  os             TEXT    NOT NULL DEFAULT '',
  first_seen_at  DATETIME NOT NULL,
  last_seen_at   DATETIME NOT NULL,
  last_ip        TEXT    NOT NULL DEFAULT '',
  revoked_at     DATETIME,
  revoked_by     TEXT    NOT NULL DEFAULT ''
);
```

**`license`（v19，单行）**

```sql
CREATE TABLE IF NOT EXISTS license (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  payload     TEXT NOT NULL,   -- Base64 原始字节
  signature   TEXT NOT NULL,   -- Base64 Ed25519
  key_id      TEXT NOT NULL DEFAULT '',
  imported_at DATETIME NOT NULL,
  imported_by TEXT NOT NULL DEFAULT ''
);
```

**`security_events`（v19）**

```sql
CREATE TABLE IF NOT EXISTS security_events (
  id         INTEGER PRIMARY KEY,
  event_type TEXT NOT NULL,   -- license_import / device_register / device_revoke / login_fail / ...
  actor      TEXT NOT NULL DEFAULT '',
  detail     TEXT NOT NULL DEFAULT '',
  ip         TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL
);
```

### 6.2 迁移规则

- v18/v19 必须同时实现 MySQL 与 SQLite（D-015），即使本版只用 SQLite。
- `license`、`devices`、`security_events` 加入 `preservedTables`（MySQL 整库恢复时保留现场）。
- SQLite 整库恢复替换整个文件，授权/设备记录随快照走；文档写明。
- 升级前自动安全备份（沿用现有机制）。

### 6.3 现有单机数据升级

| 来源 | 目标 | 做法 |
|---|---|---|
| 单机 SQLite | 局域网 SQLite | 停单机 → 把 `rferp.db` 放到主机 dataDir → 起服务；迁移自动执行 |
| 单机 MySQL | 局域网 SQLite | **本版不支持**；需要迁移工具（后续），或继续用单机 |
| 新客户 | 局域网 SQLite | 空库初始化 → 建管理员 → 导入授权 |

---

## 7. API 契约

### 7.1 请求头

| 头 | 用途 |
|---|---|
| `X-RFERP-Device-ID` | 设备指纹哈希；登录与所有已认证请求必带 |
| `X-RFERP-Device-Name` | 显示名（计算机名） |
| `X-RFERP-Client-Version` | 客户端版本；低于 `minClientVersion` 返回 426 |
| `Authorization: Bearer <token>` | 会话 |

### 7.2 新增/调整端点

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | `/api/v1/serverinfo` | 匿名 | 版本、API、schema、DB ID、storage、deployment、minClientVersion |
| GET | `/api/setup/status` | 匿名 | `{needsSetup: bool}` |
| POST | `/api/bootstrap-admin` | 匿名 + `X-Bootstrap-Token` | 空库建管理员 |
| POST | `/api/login` | 匿名 + 设备头 | 返回 token/user；设备登记/校验 |
| GET | `/api/license` | admin | 授权状态 |
| POST | `/api/license/import` | admin | 导入签名 envelope |
| GET | `/api/devices` | admin | 设备列表 |
| POST | `/api/devices/revoke` | admin | 撤销设备 |
| GET | `/api/backup/download` | admin | 下载备份文件 |
| GET | `/api/datadir` | 已登录 | 远程返回 `{remote:true}`，不含路径 |

### 7.3 错误契约

```json
{ "error": "授权客户端数已满（2/2）", "code": "device_limit" }
```

| code | HTTP | 场景 |
|---|---:|---|
| `device_limit` | 403 | 新设备超额 |
| `device_revoked` | 403 | 设备被撤销 |
| `license_missing` | 403 | 无授权，仅主机可登录 |
| `license_expired` | 403 | trial/订阅到期只读 |
| `bootstrap_token_invalid` | 403 | 初始化令牌错误 |
| `version_incompatible` | 426 | 客户端版本过低 |
| `unauthorized` | 401 | 会话失效/设备不匹配 |

### 7.4 设备绑定规则

- 登录时必须带 `X-RFERP-Device-ID`；缺省 400。
- 会话记录 `device_id`；后续请求的设备头必须与会话一致，否则 401。
- 主机：服务启动时登记 host；主机 UI 的设备指纹与 host 一致，按 host 处理，不占额度。
- 撤销设备：该设备所有会话立即失效；再次登录返回 `device_revoked`。
- 删除设备记录：释放额度，允许同 ID 重新登记（仅管理员）。

---

## 8. 部署与安装

### 8.1 主机要求

| 项 | 要求 |
|---|---|
| OS | Windows 10/11 64 位；推荐专业版 |
| CPU/内存 | 4 核 / 8GB 起 |
| 磁盘 | SSD；数据目录 ≥20GB 可用 |
| 网络 | 有线优先；固定 IP/DHCP 保留 |
| 电源 | 建议 UPS；关闭睡眠/休眠 |
| 安全 | 杀毒排除数据目录与 `RFERP-server.exe` |
| 账号 | Windows 服务用 `LocalService`；数据目录 ACL 授权 |

### 8.2 网络检查

- 主机和客户端同一网段（如 `192.168.1.x/24`）。
- 路由器关闭 AP 隔离/客户端隔离；不要用访客网络。
- 主机防火墙只放行 `LocalSubnet` 的 TCP 8080。
- 主机 DHCP 保留；客户端手填 IP。
- 验证：

```powershell
ping 192.168.1.10
Test-NetConnection 192.168.1.10 -Port 8080
curl http://192.168.1.10:8080/healthz
```

### 8.3 安装顺序

1. 主机：运行安装器 → 选择数据目录 → 装服务 → 加防火墙 → 记下 IP 和初始化令牌。
2. 主机/管理员设备：打开客户端 → “连接局域网主机” → 输入 IP → 空库建管理员（输令牌）。
3. 导入 License（3/5 客户端）。
4. 其他客户端：安装 → 输入同一 IP → 登录 → 自动登记。
5. 验收：入库/出库/追溯/审计；第 3 个设备被拒；重启；备份。

### 8.4 数据目录

- 推荐 `D:\RFERP数据` 或 `C:\ProgramData\RFERP`。
- 禁止放在网络共享、U 盘、同步盘（OneDrive 等）。
- ACL：Administrators/SYSTEM 完全控制；`NT AUTHORITY\LOCAL SERVICE` 修改权限。
- 备份目录在数据目录下；外部备份由客户手工复制到 USB/NAS。

### 8.5 升级与回滚

- 客户端：现有自动更新。
- 服务端：停服务 → 备份 → 替换 `RFERP-server.exe` → 启动 → `/readyz` → 冒烟。
- 保留上一版 exe 与升级前备份；失败回滚。
- 数据库迁移只向前；升级前必须有备份。

---

## 9. 客户交付 SOP

| # | 步骤 | 内容 | 产出 |
|---|---|---|---|
| 1 | 售前确认 | 设备数、是否同一局域网、主机配置、是否远程、备份盘 | 需求确认单 |
| 2 | 网络勘察 | `ping` + `Test-NetConnection 8080`；AP 隔离/访客网络/Windows 网络类型 | 网络检查表 |
| 3 | 主机准备 | Win10/11 专业版、8GB、SSD、有线、UPS、固定 IP、关睡眠 | 主机就绪 |
| 4 | 装服务端 | **先在杀软信任区加入数据目录 + 安装目录** → 安装器 → 服务 → 防火墙 → 初始化令牌 | 服务在线 |
| 5 | 导入授权 | 3/5 客户端 License | 授权生效 |
| 6 | 装客户端 | 2/4 台；连接向导；登录；设备登记 | 客户端在线 |
| 7 | 验收 | 业务操作；第 3 个被拒；重启；备份 | 验收单 |
| 8 | 培训 | 连接、重启、备份、支持方式 | 培训记录 |
| 9 | 交付资料 | 主机信息卡、IP、管理员、备份位置、支持电话 | 交付包 |
| 10 | 观察回访 | 2 周内每周回访；记录工单 | 试点报告 |

---

## 10. 测试与验收

### 10.1 测试矩阵

| 类别 | 用例 |
|---|---|
| 网络 | 有线+WiFi、全 WiFi、AP 隔离开/关、访客网络、公用/专用、IP 变化 |
| 授权 | 有效/无效/过期/超额/无授权；导入；签名篡改 |
| 设备 | 2 台正常；第 3 台被拒；撤销；删除；重装系统；换电脑；主机换机 |
| 业务 | 并发入库；版本冲突；批次追溯；审计 |
| 主机 | 服务重启；主机重启；断电；睡眠；磁盘满；备份目录不可写 |
| 备份 | 自动/手动/下载/恢复；恢复后登录与数据一致 |
| 安全 | 公网/4G 8080 不通；错误密码限流；未授权初始化 403；路径穿越 |
| 性能 | 2–4 客户端；1 万行；列表 p95 <300ms；写 <500ms |

### 10.2 验收清单

- [ ] 3 台设备登录、入库、追溯、审计。
- [ ] 第 3 个客户端被拒；撤销后可注册。
- [ ] 主机重启后服务自启、客户端重连。
- [ ] 主机 IP 变化后可改地址连上。
- [ ] 客户端不显示 `D:\`；备份可下载。
- [ ] 授权无效/无授权时仅主机可登录。
- [ ] 主机换机不被锁死。
- [ ] 公网/4G 访问 8080 不通。
- [ ] 防火墙仅 `LocalSubnet`。
- [ ] 每日备份生成；恢复后数据一致。
- [ ] 两客户端并发入库 20 次，库存正确。
- [ ] 非开发人员按文档完成安装。

### 10.3 Go/No-Go

- [ ] 上述验收全通过。
- [ ] 无 P0/P1 缺陷。
- [ ] 备份/恢复演练通过。
- [ ] 安装器/文档可交付。
- [ ] 支持流程与回滚方案就绪。

---

## 11. 决策台账（已锁定）

| ID | 决策 | 锁定值 |
|---|---|---|
| D1 | 3/5 台是否含主机 | **不含**；3=1 主机+2 客户端，5=1 主机+4 客户端 |
| D2 | 授权单位 | **客户端设备**；账号不设硬上限 |
| D3 | 数据库 | **SQLite 唯一** |
| D4 | 服务器形态 | **无头服务 + 客户端 UI** |
| D5 | 主机 UI | **客户端连 127.0.0.1**，按 host 处理，不占额度 |
| D6 | License 模式 | **永久授权 + 1 年支持**；支持到期不影响使用 |
| D7 | License 校验 | **离线 Ed25519 签名**；在线激活后置 |
| D8 | 设备指纹 | **SHA-256(MachineGuid + 系统盘序列 + CPU)** |
| D9 | 超额 | **已登记可登录，新设备拒绝** |
| D10 | 到期 | **仅 trial/订阅转只读**；永久授权不停用 |
| D11 | 试用 | **销售签发 3 客户端/30 天**；无内置重置 |
| D12 | 端口 | **8080**；冲突提示 **18080** |
| D13 | 地址 | **DHCP 保留 + 手填 IP**；不做 mDNS |
| D14 | 传输 | **受控局域网 HTTP**；TLS 后置 |
| D15 | 会话 | **内存会话**；服务重启后重登 |
| D16 | 备份 | **每日自动 + 手动**；7 日 + 4 周；外部盘手工复制 |
| D17 | 恢复 | **主机 CLI/停服务**；客户端不开放 |
| D18 | 服务包装 | **SCM 原生**（`internal/svcrun`；2026-11-06 由 WinSW v2 变更） |
| D19 | 主机硬件 | **客户自备或预配置迷你主机**；最低 4 核/8GB/SSD |
| D20 | 数据迁移 | **同机 SQLite 原地；异机复制 DB；MySQL→SQLite 不做** |
| D21 | 多子网/VLAN | **v1 不支持**；按定制 |
| D22 | 远程访问 | **v1 不做**；后置加购 |
| D23 | 更新 | **客户端自动；服务端手动**；升级前备份 |
| D24 | 支持 | **v1 无远程**；日志/截图；支持 VPN 后置 |
| D25 | 日志保留 | **180 天** |
| D26 | 3 vs 5 差异 | **仅 `maxClients`** |
| D27 | 5 台 | **3 台试点通过后再做** |
| D28 | EULA | **完整**：设备数、禁止转售、数据/备份责任、责任上限 |
| D29 | 主机在线承诺 | **不承诺 24×7**；客户负责供电/网络/主机 |
| D30 | 远程加购 | **后置**；单独定价 |

---

## 12. 风险台账（已记录）

| ID | 风险 | 影响 | 处置 | 剩余风险 |
|---|---|---|---|---|
| R1 | AP 隔离/访客网络导致互不可达 | 客户端连不上 | 售前 `Test-NetConnection`；关 AP 隔离；改有线 | 中 |
| R2 | 主机 IP 变化 | 全体断连 | DHCP 保留；客户端可编辑地址 | 低 |
| R3 | 主机睡眠/重启/断电/更新 | 全员不可用 | 关睡眠、服务自启、UPS、更新窗口 | 中 |
| R4 | 主机磁盘坏/无备份 | 数据丢失 | 独立备份盘；每日备份；季度演练 | 中 |
| R5 | SQLite 损坏/放网络共享 | 数据损坏 | 本机 SSD；WAL；禁止网络共享；完整性检查 | 低 |
| R6 | 设备指纹变化（重装/换硬件） | 设备被拒 | 管理员撤销旧设备；主机 CLI 兜底 | 中 |
| R7 | 授权锁死（名额满/管理员被锁） | 无法登录 | 主机豁免；主机 CLI 撤销；保留管理员设备 | 低 |
| R8 | License 被篡改/绕过 | 收入损失 | Ed25519 签名；多点校验；EULA | 中（接受） |
| R9 | 恢复旧备份导致超额 | 新设备被拒 | 已登记可登录、新设备拒绝；界面提示 | 低 |
| R10 | 服务端/客户端共用 config | 服务器误判存储 | 独立 config；服务器只看 `storage` | 低 |
| R11 | 主机同时跑单机模式和 server | 两个写者/锁冲突 | 主机 UI 走客户端连 127.0.0.1；安装器禁止 | 低 |
| R12 | 客户端/服务端版本漂移 | 接口不兼容 | `serverinfo` + `minClientVersion` + 426 | 低 |
| R13 | 远端 401 被本地 API 变 500 | 界面卡死 | 显式错误映射；E2E 覆盖 | 低 |
| R14 | 远程路径泄露 | 信息泄露 | 远程模式脱敏；测试断言无盘符 | 低 |
| R15 | 杀毒/EDR 拦截服务或数据库 | 服务起不来/锁库 | 交付 SOP 前置杀软信任区（数据目录 + 安装目录）；发布包二进制必须 `-trimpath -ldflags "-s -w"`（已具备）；交付附 SHA256 清单；被误报则向厂商申诉 | 中 |
| R16 | 服务账号/数据目录权限不足 | 无法写库/备份 | 安装器设置 ACL；LocalService 授权 | 中 |
| R17 | 8080 被占用 | 服务起不来 | 安装前检查；冲突提示 18080 | 低 |
| R18 | 时间不同步/时区不一致 | 审计时间错乱 | 主机开 NTP；统一时区 | 低 |
| R19 | 局域网 HTTP 被嗅探 | 凭据泄露 | 受控 WiFi/WPA2；不连公共 WiFi；TLS 后置 | 中（接受） |
| R20 | 设备共享/复制客户端 | 绕过设备数 | 设备指纹+会话绑定；审计；EULA | 中（接受） |
| R21 | 客户期望远程访问 | 交付不符 | 售前明确局域网版；远程加购单列 | 中 |
| R22 | SQLite 性能瓶颈 | 大客户卡顿 | 限制 5 台；短事务；索引；超规模转 MySQL 版 | 低 |
| R23 | 服务端升级失败 | 全员不可用 | 升级前备份；保留旧 exe；回滚 | 中 |
| R24 | 备份盘满/备份失败 | 无可用备份 | 保留策略；磁盘告警；每月巡检 | 中 |
| R25 | 管理员忘记密码 | 无法管理 | 主机 CLI 重置管理员 | 低 |
| R26 | 数据迁移出错 | 数据丢失 | 源库不动；dry-run；行数校验；备份 | 中 |
| R27 | 无远程支持，排障慢 | 支持成本高 | 详细日志；客户截图；一页纸排障 | 中（接受） |
| R28 | Windows 家庭版/旧版本 | 服务/防火墙能力不足 | 要求 Win10/11 专业版；安装器检测 | 低 |
| R29 | 多网卡/多 IP 选错 | 客户端连错 | 启动日志列出所有 LAN IP；安装器标注主 IP | 中 |
| R30 | 客户路由器更换/DHCP 重置 | IP 变化 | DHCP 保留；主机信息卡；客户端可编辑 | 中 |
| R31 | 发布工具链被 AV 误报（2026-11-06 实测） | 签名步骤中途文件被删，`releases.json` 生成失败 | 所有 Go 二进制一律 `-trimpath -ldflags "-s -w"` 构建（未 strip 的 `signmanifest` 被 Kaspersky 判 `VHO:Trojan.Win64.Convagent.gen` 并删除，strip 后通过）；构建机加目录排除；向厂商提交误报 | 中 |
| R32 | ~~交付包内含第三方未签名 exe（WinSW.exe）~~ | — | **已关闭**：改为 SCM 原生服务，交付包不再含第三方 exe（2026-11-06） | — |

> 风险状态：**已记录，不阻塞开发**。R8、R19、R20、R27 为明确接受的剩余风险；其余按“处置”列执行。

---

## 13. 排期

| 周 | 阶段 | 里程碑 |
|---|---|---|
| W1 | 0 + 1 | 口径锁定；服务器服务能跑 |
| W2 | 2 | 设备授权生效，第 3 个被拒 |
| W3–W4 | 3 | 客户端向导、错误处理、备份下载 |
| W5 | 4 + 5 | 安装器/文档；内部测试；Go/No-Go |
| W6–W7 | 6 | 3 台试点；问题修复 |
| W8 | 7 | 发布准备；5 台版排期 |

总工作量约 **20–28 人日**；1 人全职 4–5 周，含试点。

---

## 14. 交付物

| 交付物 | 内容 |
|---|---|
| `RFERP-server-<ver>-win64.zip` | server exe（即服务本体）、安装脚本、配置模板 |
| `RFERP-<ver>-setup.exe` | 客户端安装包 |
| `rferp-license.exe` | 内部签发工具（不交付客户） |
| `license.rferplic` | 每客户一份签名授权 |
| 主机安装文档 | 数据目录、服务、防火墙、IP、备份 |
| 客户端连接文档 | 输入 IP、登录、设备撤销 |
| 备份/恢复手册 | 停服务、替换 DB、启动、验证 |
| 排障一页纸 | 连不上、IP 变化、防火墙、服务未启动 |
| EULA 模板 | 设备数、禁止转售、数据/备份责任、责任上限 |
| 测试报告 | 3 台局域网测试、授权/设备、备份恢复 |
| 试点报告 | 2 周运行记录、工单、改进项 |

---

## 15. PR / 文件清单

| PR | 标题 | 主要文件 |
|---|---|---|
| 1 | `fix(config): separate server/client config; storage independent of mode` | `internal/config`、`cmd/server`、`cmd/desktop-wails` |
| 2 | `feat(server): LAN mode, SQLite WAL, service logging, health` | `cmd/server`、`internal/bootstrap`、`internal/repository`、`internal/logging` |
| 3 | `feat(license): signed license with maxClients` | `cmd/rferp-license`、`internal/license`、`internal/service`、`internal/api` |
| 4 | `feat(devices): registration, host exemption, revoke, session binding` | `internal/migrate`、`internal/repository`、`internal/service`、`internal/api` |
| 5 | `feat(api): login guard, bootstrap token, over-quota behavior` | `internal/api`、`internal/auth` |
| 6 | `feat(desktop): ModeClient + LAN connection wizard` | `cmd/desktop-wails`、`web/src/screens`、`web/src/lib/api.ts` |
| 7 | `feat(api): remote error mapping, path redaction, backup download` | `internal/api`、`web/src` |
| 8 | `feat(deploy): Windows host installer, firewall rule, docs` | `deploy/windows`、`docs` |
| 9 | `test: 3-device LAN e2e, license/device limits, backup restore` | `*_test.go`、`web/tests` |
| 10 | `docs: customer handover, runbook, EULA template` | `docs` |

---

## 16. 一页纸

- **产品**：单机 / 3 台（1 主机 + 2 客户端）/ 5 台（1 主机 + 4 客户端）。
- **授权**：`maxClients` 计客户端，主机豁免；离线 Ed25519；无授权仅主机可登录；超额拦新；trial 到期只读。
- **数据库**：SQLite；WAL + busy_timeout；每日备份 7 日 + 4 周。
- **网络**：同一网段、关 AP 隔离、防火墙仅 LocalSubnet、DHCP 保留、手填 IP、HTTP。
- **安装**：SCM 原生服务 + 主机安装脚本 + 客户端连接向导。
- **顺序**：服务器局域网化 → 设备授权 → 客户端模式 → 安装器 → 测试 → 3 台试点 → 5 台。
- **周期**：4–5 周 + 2 周试点；VPN/远程后置。
- **最大风险**：局域网不可达、主机不可靠、设备锁死、备份不可用、配置冲突。
