# 局域网版实施记录（lan-v1）

对应方案：`lan-v1`（2026-10-29 定稿）。本文记录**代码里实际落了什么**、与方案原文的偏差、以及还没做的部分。方案里的 D1–D30 口径未改动，只记录实现层面的落地情况。

---

## 1. 已落地

### 1.1 配置隔离（方案 §3.4 / R10）

- `config.LoadAt(path)` 支持显式配置；候选顺序：`RFERP_CONFIG` → exe 目录 → 当前目录 → 用户配置目录。
- `cmd/server` 支持 `-config`；显式路径读不到直接失败，不退化。
- 服务端选库只看 `storage`（新增 `config.StorageIsSQLite()`），**不看 mode**；`bootstrap.BuildServer` 与桌面端的 `bootstrap.Build` 分开。
- 服务端启动日志打印实际使用的配置文件路径，便于核对两侧没有共读同一份。

### 1.2 服务器形态（§4.1 / NFR）

- `-addr` / 配置 `server.addr`；默认 `:8080`。
- 单实例锁 `RFERP.Server.SingleInstance`（避免两个进程开同一个 SQLite）。
- `/healthz`（进程）、`/readyz`（数据库 Ping）、`GET /{$}` 欢迎 JSON。
- 启动打印所有局域网 IPv4（RFC1918 优先）与主机设备指纹。
- 日志写到 `<dataDir>\日志`，20MB × 20 份 + 180 天年龄清理（`logging.InitWith` / `PruneOlderThan`）。
- 自动备份改为服务端定时器（`StartAutoBackupTicker`），不再依赖"下次打开软件"。
- SQLite 的 WAL / `busy_timeout=5000` / `foreign_keys=ON` / 单连接为既有实现，未改动。

### 1.3 授权（§4.3 / FR-18–24）

- `internal/license`：Ed25519 签名的 JSON envelope（payload + signature + keyId），验签针对**原始字节**。
- `cmd/rferp-license`：`genkey` / `issue`（permanent / trial / subscription，`-maxClients`、`-supportUntil`、`-expiresAt`）。
- 公钥来源：`DefaultPublicKeyBase64`（发布构建用 `-ldflags -X` 注入）、`RFERP_LICENSE_PUBKEY` 环境变量、`<dataDir>\license-public.key`，三者叠加（换钥匙时旧授权仍可验）。
- 判定口径：永久授权不看任何日期；trial/订阅到期转只读；未导入授权时主机仍可登录。
- `GET /api/license`、`POST /api/license/import`（接受文件原文或 `{license: text}`）；导入失败不覆盖已生效授权。

### 1.4 设备（§4.4 / FR-25–31）

- 迁移：**v19 `devices`**、**v20 `license` + `security_events`**（MySQL 与 SQLite 双端）。
- 指纹：`internal/deviceid` = SHA-256(MachineGuid + 系统盘卷序列号 + CPU 标识)；取不到的部件用固定占位，不阻塞。
- 主机启动时登记 `kind=host`，不占额度、永远可登录；**主机不能撤销也不能删除**（撤销会把唯一救场设备锁在门外）；启动时若发现主机行被历史状态撤销，会自动复位为 active。
- 登录时登记/刷新客户端设备；额度按**已登记行数**统计——**撤销不释放名额，删除才释放**（§7.4）。
- `GET /api/devices`、`POST /api/devices/revoke`、`POST /api/devices/delete`；撤销后该设备会话立即失效。
- CLI 兜底：`-devices "list|revoke <前缀>|delete <前缀>"`。

### 1.5 登录门禁与会话绑定（§7）

- 请求头：`X-RFERP-Device-ID`（64 位小写十六进制，严格校验）、`-Device-Name`、`-Client-Version`、`-Client-OS`。
- 顺序：先验密码，再验设备（避免用错密码白白占额度）。
- 错误契约：`device_limit` / `device_revoked` / `license_missing` / `license_expired` / `device_missing` / `bootstrap_token_invalid`，均带 `code` 与中文 `error`。
- 会话记录 `DeviceID`，后续请求设备头不一致或设备已撤销 → 401。
- 到期只读：业务写操作 403 `license_expired`；用户/备份模块与管理操作放行（否则客户无法续期或导出）。
- 首建管理员令牌：空库启动生成 8 位令牌，写 `<dataDir>\初始化令牌.txt` 并打印；`POST /api/bootstrap-admin` 必须带 `X-Bootstrap-Token`。
- `GET /api/setup/status` 供连接向导判断是否已初始化；只回 `needsSetup` 与 `tokenRequired` 两个布尔，不泄露额度与客户名。
- `GET /api/lan/status` 匿名可读但只回部署形态；额度、客户名、主机指纹仅对已登录用户返回。
- 授权与额度缓存 15 秒（权限中间件每个请求都要判只读，无缓存等于每请求一次计数查询）；会话每分钟回库复核一次设备，使 CLI 在另一进程撤销设备能在 1 分钟内生效。

### 1.6 远程形态（PR7）

- `api.Client` 统一构造请求（Authorization + 四个设备头），新增 `code` 字段解析与 `ServerURL()/SetServerURL()`。
- 客户端管理员可通过 `/api/license`、`/api/devices`、`/api/backup`、`/api/backup/files`、`/api/backup/download` 操作主机：全部原样转发，错误状态与 `code` 一并透传。
- 首建管理员令牌由客户端透传（`CreateInitialAdminWithToken`）；首启向导在 `tokenRequired` 时显示令牌输入框。
- `GET /api/backup/download?name=…`：只接受备份目录内的文件名（沿用 `backuplayout.Resolve` 的穿越校验），流式返回。
- `GET /api/datadir`：非回环调用或客户端形态一律返回 `{remote:true}`，不泄露目录（R14 / FR-16）。
- 整库恢复/清空/改数据目录在服务器形态仍 403（`hostOperations` 默认为关）。

### 1.7 桌面客户端模式（§4.2 / FR-10–17）

- `cmd/desktop-wails`：`mode=client` 时**不打开本地业务库**，直接用 `api.Client` 起内嵌服务；`-serve` 也可用于自动化验证。
- 客户端模式不启用 hostOperations 与原生目录选择框；自动备份不启动（数据在主机）。
- 登录页右下角显示/切换主机地址（`POST /api/lan/connect`，先探测再落盘，探测失败给出可执行提示）。
- 首启配置页新增「连接局域网主机」页签：填地址 → 测试连接（`probe:true` 只探测）→ 保存后自动重启进程。
- 地址归一化：可只填 IP，自动补 `http://` 与 `:8080`；显式写 `https://` 不会被悄悄改成 http。

### 1.8 版本门禁（FR-15）

- 配置 `minClientVersion`（server.json）；`/api/v1/serverinfo` 下发，客户端启动与切换地址前先比对。
- 低于最低版本：登录与每个已认证请求都回 426 + `version_incompatible`；客户端启动时直接提示升级并退出。
- 版本比较对 `dev`/无法解析的版本串不拦截（否则开发包会被自己挡住）。

### 1.9 部署与文档

- `deploy/windows/install-host.ps1`：权限/硬件/端口检查 → 建目录与 ACL → 复制程序 → 生成 `server.json` → WinSW 注册 → 防火墙 `LocalSubnet` → 等 `/readyz` → 打印地址与令牌。
- `deploy/windows/uninstall-host.ps1`：只卸服务与防火墙，不动数据。
- `deploy/windows/client.config.json`：客户端配置示例。
- `deploy/windows/client-installer.iss` + `build-client-installer.ps1`：Inno Setup 客户端安装包（支持 `/SERVER=` 静默预配置），已实际编译通过。
- `deploy/windows/build-server-package.ps1`：服务端发布包（exe + WinSW + 安装脚本 + 配置模板 + README），已实际产出 `dist/RFERP-server-1.0.0-win64.zip` 并用它完成了一次真机安装/卸载。
- `docs/eula-template.md`：EULA 模板（设备数/禁止转售/数据与备份责任/责任上限）。
- `docs/lan-deployment.md`：部署、授权、设备、备份恢复、升级、排障一页纸。

---

## 2. 与方案原文的偏差

| 方案写法 | 实际 | 原因 |
|---|---|---|
| devices v18 / license v19 | 合并上游后实际落到 **v19 / v20** | 方案的版本号按另一分支基线估算；代码以 `migrate.nextVersion()` 为准，版本号只是迁移顺序 |
| 服务器管理端点 `/api/devices` 等 | 额外有 `/api/lan/status`（匿名，只回部署形态与额度） | 登录页要在**登录前**显示"连的是哪台主机" |
| 设备额度"已登记可登录，新设备拒绝" | 明确为"撤销不释放名额，删除才释放" | 按 §7.4 的删除语义落地；否则撤销可被用来绕过额度 |
| 公钥"内嵌固定常量" | 改为 `-ldflags -X` 注入 + 环境变量 + 数据目录文件 | 私钥/公钥都不该硬编码进会进版本库的源码 |
| 主机 UI 连 127.0.0.1 按 host | 用同一份指纹判断，不看来源 IP | 同机两进程指纹一致；IP 判据在跨 NAT/多网卡时不可靠 |
| server.json 只写 storage | 同时解析 `server.addr` | 安装器用 `-Port` 生成配置；不解析会让服务听 8080、防火墙开 18080 |

---

## 3. 验证

- `go test ./internal/... ./cmd/server/... ./cmd/desktop-wails/...` 全绿（新增授权、额度、登录门禁、会话绑定、只读、路径穿越、/readyz 用例）。
- `web`：`tsc -b`、`vitest`（231 项）、`oxlint` 无 error。
- 端到端冒烟（本机）：
  - 服务端 `storage=sqlite` 但配置写着 `mode=client` → 仍按 SQLite 启动（配置隔离生效）；
  - 无授权：主机登录 200、客户端 403 `license_missing`；
  - 导入 2 客户端授权后：第 1、2 台 200，第 3 台 403 `device_limit`；
  - 撤销设备后其登录 403 `device_revoked`；设备列表含 host/client；
  - 远程 `/api/datadir` 只回 `{remote:true}`；`/api/restore` 403；
  - 客户端模式 `-serve`：`/api/lan/status` 报 `client + serverUrl`，改地址错则 400 且给排障提示，正确则落盘并记录日志；
  - 客户端管理员：读/导入授权、列/撤销设备、创建备份（流）、列备份、下载备份全部经客户端转发成功；
  - 配置里写 `server.addr: 127.0.0.1:18091` 且不传 `-addr`，服务端按配置监听；
  - 首建管理员：无令牌 403、带令牌 200，客户端形态令牌被转发。

测试稳定性：整仓并行跑曾偶发 `TempDir RemoveAll cleanup: The directory is not empty`（`repository` / `backuplayout` / `service` / `migrate`）。根因是 Windows 上 SQLite 连接刚关闭时 `-wal/-shm` 仍被短暂占用，而 `testing.TempDir` 的清理只做一次 `RemoveAll`。已在六个 DB 相关测试包改用带重试的 `testTempDir(t)`（最多 2 秒），并把 `internal/service` 的默认数据目录兜底到临时目录（原先会把 `pre_restore_*.db` 写进用户主目录，污染后续用例）。之后整仓并行连跑 11 次、六包 `-count=3` 连压 2 轮均全绿，`~/RFERP` 不再产生残留。

已知环境问题：`app/cmd/signmanifest` 在本机测试时被安全软件拦截（写测试 exe 报 `Access is denied`），与本次改动无关；交叉验证 `go build -o <其他路径>` 可正常产出。

---

## 3.1 单机验收脚本

`deploy/windows/smoke-lan.py` 用脚本模拟多台设备，把"需要两三台电脑"的判据固化下来
（服务端只看客户端自己算的指纹，所以一台机器足够）。三个阶段：

| 阶段 | 覆盖 | 判据数 |
|---|---|---|
| `bare` | 初始化令牌（缺/错/对/重复）、设备头缺失与非法、无授权时主机放行/客户端拒绝、目录脱敏、破坏性操作 403、下载路径穿越 | 13 |
| `licensed` | 额度边界、超额老设备仍可登录、撤销（会话立即失效/不可再登录/不释放名额）、删除释放、主机名不被空值覆盖、只读用户读写、并发入库、备份生成/列表/下载、备份与写入并发 | 25 |
| `restart` | 重启后旧会话失效、设备与授权仍在、可重新登录 | 4 |
| `expired` | 试用到期：主机仍可登录、`read_only=true`、业务写入 403 `license_expired`、读取/用户管理/备份仍可用 | 6 |
| `tampered` | 篡改授权被拒 403 `license_invalid`、失败导入不覆盖原授权 | 2 |
| `version` | `serverinfo` 下发最低版本、旧版本登录/已认证请求 426、不带版本头 426、达标版本 200 | 6 |
| `jitter` | 弱网：正常登录、400ms 延迟下成功且变慢、断链快速失败且不挂死、链路恢复后同一会话继续可用 | 4 |

`jitter` 需要 `deploy/windows/jitter-proxy.py`：一个可注入延迟/断链的 TCP 代理
（模式文件支持 `normal` / `delay:400` / `drop`，按**每块数据**重读，避免 keep-alive
连接绕过注入）。测试对象是**客户端模式的内嵌服务**（`RFERP.exe -serve`），
因此覆盖的是"客户端 → 代理 → 主机"整条链路，而不是直接打主机。

用法（服务端已启动，空库启动的令牌在 `<dataDir>\初始化令牌.txt`）：

```bash
python deploy/windows/smoke-lan.py --phase bare \
  --base http://127.0.0.1:18080 --password <管理员密码> --token <初始化令牌> \
  --host-device <服务端日志里的主机指纹>
# 导入授权后
python deploy/windows/smoke-lan.py --phase licensed --base ... --password ... --host-device ...
# 重启服务端后
python deploy/windows/smoke-lan.py --phase restart  --base ... --password ... --host-device ...
```

脚本可重复执行（每次先清掉上一轮遗留的客户端设备）。

### 本轮由它查出并修掉的问题

| 问题 | 现象 | 修复 |
|---|---|---|
| 初始化后仍报"令牌不正确" | 已建过管理员时，不带令牌的请求返回 403 `bootstrap_token_invalid`，用户去翻令牌文件 | 先判 `UserCount()>0` 返回 409，再验令牌 |
| 同一秒内第二次备份失败 | `backup_<时间戳>.db` 撞名，返回 500 "The file exists" | 用 `.2`/`.3` 后缀避让；后缀形式命中恢复文件名规则，恢复口令仍可算 |
| 备份失败不落日志 | 只回"请检查数据目录是否可写"，真实原因（库忙/磁盘/权限）看不到 | 记录并回显真实错误 |
| 写入高峰备份偶发失败 | VACUUM INTO 遇 BUSY 直接失败 | 退避重试 5 次（约 3 秒） |
| 设备名被空值覆盖 | 命令行登录不带计算机名，把"主机"抹成指纹前缀 | 空名保留已登记值 |
| 非法 UTF-8 设备名入库 | 旧客户端用 GBK 发中文名，存成一串 U+FFFD | 非法 UTF-8 按"没带名字"处理 |
| **不报版本号可绕过 426 门禁** | 空版本头被 `VersionLess` 判为"不比最低版本低" | 门禁开启时把"没报版本"按过低处理 |
| **安装脚本在 PS 5.1 下语法报错** | `.ps1` 存成无 BOM 的 UTF-8，中文按 GBK 解析，脚本根本跑不起来 | 三个 `.ps1` 改为 **UTF-8 with BOM**；已用 Parser 逐个验证 |
| **提前拒绝导致连接重置** | 处理器在读 body 前就返回（令牌错、403），net/http 直接关连接，客户端还在发 body → Windows 回 RST，界面显示"网络错误"。本机实测约 5% | Handler 外层加 `drainRequestBody`：返回后排空 body（上限 1MB）；修复后 60 次请求 0 次重置 |
| 备份失败日志缺失 | 只回"检查数据目录是否可写" | 记录并回显真实错误 |

### 3.2 服务化真机验证（2026-11-02）

用发布包解压后的 `install-host.ps1` 在本机完成一次完整安装与卸载：

- 安装：权限/硬件/端口 → 数据目录与 ACL → 复制程序 → 生成 `server.json` → 注册 WinSW 服务 → 防火墙（`LocalSubnet`）→ `/readyz` 200；
- 服务：`Running` / `Automatic`；
- 局域网访问：`http://<本机LAN IP>:18581/readyz` 返回 200（证明防火墙规则与 0.0.0.0 监听都生效）；
- 卸载：服务与防火墙规则均移除，数据目录按设计保留；安装目录已清理。

未覆盖：整机重启后的自启（需重启客户机）、失败自动重启的实际触发。

### 3.3 验收顺序

`bare` 必须在最前面跑——它自己完成"空库首建管理员"的验证；先建管理员再跑 `bare`
会得到 409，看起来像门禁失效，其实是顺序错了。其余阶段顺序不敏感（`licensed`
每次会先清掉上一轮遗留的客户端设备）。

## 4. UI 复核（截图级）

用内置浏览器跑了一遍客户端形态（`RFERP.exe -serve` 连真实主机）：

- 登录页：右下角显示主机地址与「更改」；左下角部署说明按形态显示「已连接局域网主机 · 数据保存在主机」，不再误称"本机运行"。
- 设置 → 授权与设备：客户/类型/额度、支持到期（按日期显示）、设备表（主机不可操作）正常。
- 备份导出：远程提示条、数据目录显示「—」、整库恢复与清空业务数据**整块隐藏**、自动备份注明"由主机负责"且间隔不可改。

## 5. 未完成 / 后续

| 项 | 为什么还没做 | 需要什么 |
|---|---|---|
| 3 台 / 5 台试点 | 需要真实客户环境（多台电脑、真实路由器、真实业务数据） | 客户现场 + 按 §9 SOP 执行 |
| 真实 MySQL 环境回归 | 本机没有 MySQL 实例；v19/v20 的 DDL 只在 SQLite 上真跑过 | 一台装 MySQL 8 的机器，或允许本机安装 |
| 代码签名 | 没有代码签名证书 | 公司证书与签名流程 |
| 5 台版 | 只改 `maxClients`（D26），代码无需改动 | 按 D27 待 3 台试点通过后签发 |
| 窄屏 / 深色主题逐屏复核 | 视觉复核只覆盖了桌面宽度下的登录页、授权页、备份页 | 再跑一轮窄屏与主题切换截图 |
| Windows 10 / 家庭版实机 | 本机是 Win11；服务、防火墙在家庭版的能力差异未实测 | 一台 Win10 机器 |

## 6. 变更记录

### 6.1 去掉 WinSW，改用 SCM 原生服务（2026-11-06）

**动机**：1.0.0 的交付包里除 `RFERP-server.exe` 外还有一个第三方二进制
`WinSW.exe`（约 18MB，构建时从 `ghfast.top` 下载，只校验 `MZ` 头与大小）。
它是包里唯一的未签名第三方 exe，也是唯一的供应链导入点；而它提供的四个能力
在 Windows 上都有系统级替代，其中"日志轮转"还与 `internal/logging` 重复。

**改动**：

| 项 | 之前 | 现在 |
|---|---|---|
| 服务包装 | `WinSW.exe` + `RFERP-server-service.xml` | `cmd/server` 直接实现 `svc.Handler` |
| 启动注册 | `WinSW.exe install` | `Win32_Service.Create`（`install-host.ps1`） |
| 失败重启 | `<onfailure action="restart" delay="10 sec"/>` | `sc.exe failure … restart/10000` |
| 服务账号 | `<serviceaccount>LocalService` | `StartName=NT AUTHORITY\LocalService` |
| 日志轮转 | `WinSW <log mode="roll-by-size">` | 已有 `internal/logging`（20MB × 20 + 180 天） |
| 服务进程输出 | WinSW 收进 `程序目录\logs\` | 事件日志（来源 `RFERPServer`）+ `<数据目录>\日志\` |
| 包体 | 约 22MB | **5.3MB** |

**新增包**：`internal/svcrun`
- `Run(name, main)`：由 SCM 启动就走服务分支，否则走控制台分支（Ctrl+C/SIGTERM）；
- 服务分支先报 `StartPending`（`WaitHint` 300 秒）再跑主体，**监听成功后才报 `Running`**。
  这一点是必须的：bootstrap 里包含迁移与升级前 `mysqldump`，慢的时候超过 SCM 的
  30 秒启动窗口，若在 `svc.Run` 之前做，SCM 会以 1053 中止启动而日志里毫无异常。
- 启动失败/运行异常/优雅关闭超时都返回服务特定退出码，并写事件日志——
  原生服务没有控制台，stdout 会被丢弃，这是 WinSW 时代"logs 目录"的替代物。

**`cmd/server` 重构**：`main` 只保留 flag/配置/日志/健康检查/CLI 分流/单实例，
装配与监听下沉为 `serve(ctx, markReady, …)`；`log.Fatal` 全部改为返回 error，
否则服务模式下进程会静默消失而 SCM 只看到"服务意外终止"。

**兼容**：`uninstall-host.ps1` 会顺带清理 1.0.0 遗留的
`RFERP-server-service.exe` / `.xml`，避免旧包装器占着服务名。

**验证**（本机，无管理员权限）：
- 控制台模式实跑：迁移 v1–v20、主机登记、令牌生成、监听 `:18099`、`/readyz` 200；
- `-healthcheck` 退出码 0；第二个实例被单实例锁挡下（退出码 1）；
- CLI `-devices list` 在服务运行时可执行（不抢单实例锁）；
- 强杀后重启：`applied=[]`（无重复迁移），`/readyz` 再次 200；
- `go build ./...` / `go vet ./...` Windows + `GOOS=linux` 全通过；
- `internal/svcrun`、`internal/api`、`internal/service`、`internal/logging` 测试全绿；
- `build-server-package.ps1` 出包，zip 只有 5 个文件（无第三方 exe）。

**未验证**（需要管理员权限的机器）：
- SCM 实际握手（`sc start` → StartPending → Running）、`sc stop` 优雅关闭；
- 事件日志是否真的能写入（依赖安装脚本注册的事件源）；
- 失败重启策略是否按 10 秒 ×3 生效；
- 开机自启（需重启机器）。
