# 局域网版部署手册

面向 **1 台主机 + 2～4 台客户端** 的小微客户。决策口径见 `docs/plans/lan-version-plan.md`（若存在）与 `lan-v1` 方案；本文只讲怎么装、怎么验、怎么救。

---

## 1. 交付物

| 文件 | 说明 |
|---|---|
| `RFERP-server-<ver>-win64.zip` | `RFERP-server.exe`、`install-host.ps1`、`uninstall-host.ps1`、`client.config.json`、`first-run-blocked.md`、`SHA256SUMS.txt` |
| `RFERP-<ver>-setup.exe` | 客户端安装包（Wails） |
| `rferp-license.exe` | 内部签发工具，**不交付客户** |
| `license.rferplic` | 每客户一份签名授权 |

构建服务端时必须内嵌验签公钥，否则服务端无法验证任何授权：

```bash
go build -trimpath -ldflags "-s -w -X main.version=1.0.0 \
  -X app/internal/license.DefaultPublicKeyBase64=<公钥>" \
  -o dist/RFERP-server.exe ./cmd/server
```

公钥由 `rferp-license genkey` 生成；私钥只留在签发机器上，**不要进版本库**。

---

## 2. 主机安装（约 15 分钟）

### 2.1 前置条件

| 项 | 要求 |
|---|---|
| 系统 | Windows 10/11 64 位（推荐专业版） |
| 硬件 | 4 核 / 8GB / SSD，数据盘 ≥20GB |
| 网络 | 有线优先；路由器做 DHCP 保留 |
| 电源 | 关睡眠/休眠；建议 UPS |
| 安全 | 杀毒软件排除数据目录与 `RFERP-server.exe` |

### 2.2 网络勘察（装之前做）

主机与客户端必须在**同一网段**，且路由器**关闭 AP 隔离 / 客户端隔离**，不要用访客网络。

```powershell
ping 192.168.1.10
Test-NetConnection 192.168.1.10 -Port 8080
```

### 2.3 杀毒软件信任区（装之前做）

本软件未购买商业代码签名证书，杀毒软件会把"没见过的程序"提示出来——这是提示，不是病毒确认。
把下面**两个目录**提前加入信任区，可以避免安装与日常运行时的弹窗，也能避免杀毒软件扫描数据库文件时短暂锁库：

1. **安装目录**（默认 `C:\Program Files\RFERP`）；
2. **数据目录**（安装时选的，默认 `D:\RFERP数据`）。

| 杀毒软件 | 入口 |
|---|---|
| Windows Defender（Windows 安全中心） | 病毒和威胁防护设置 → 排除项 → 添加文件夹 |
| 卡巴斯基 | 设置 → 安全 → 排除项 → 指定可信文件夹 |
| 360 / 火绒 / 腾讯管家 | 设置 → 信任区 / 白名单 → 添加目录 |

被拦截时的具体处置（SmartScreen、解除锁定、PowerShell 执行策略、核对哈希）见交付包里的
`first-run-blocked.md`，客户可以直接照做。

### 2.4 安装

1. 解压 `RFERP-server-<ver>-win64.zip`。
2. 右键 PowerShell → **以管理员身份运行**，进入解压目录：

```powershell
Set-ExecutionPolicy -Scope Process Bypass -Force
.\install-host.ps1 -Port 8080
```

脚本按顺序做：权限检查 → 系统/硬件提示 → 端口检查 → 建数据目录与 ACL → 复制程序 → 写 `server.json` → 注册 Windows 服务（SCM 原生）→ 加防火墙规则（仅 `LocalSubnet`）→ 等 `/readyz` → 打印客户端地址与初始化令牌。

默认路径：

- 程序：`C:\Program Files\RFERP`
- 数据：有 D 盘时 `D:\RFERP数据`，否则 `C:\ProgramData\RFERP`

**数据目录禁止放在网络共享、U 盘、OneDrive 等同步盘**——SQLite 的锁与 WAL 在这些位置上不可靠。

### 2.5 验证主机

```powershell
curl http://127.0.0.1:8080/healthz     # 进程活着
curl http://127.0.0.1:8080/readyz      # 数据库可用
curl http://127.0.0.1:8080/            # 欢迎 JSON（服务名/版本）
```

从**另一台**电脑验证防火墙：

```powershell
Test-NetConnection 192.168.1.10 -Port 8080
```

公网/4G 访问该端口必须**不通**。若通，说明防火墙规则被改过或路由器做了端口映射。

---

## 3. 客户端连接

### 3.1 安装客户端

运行 `RFERP-<ver>-setup.exe`。首启选择「连接局域网主机」，填主机地址：

- 可直接填 `192.168.1.10`（默认补 `http://` 与 `:8080`）
- 也可填完整地址 `http://192.168.1.10:8080`

地址会写入 `config.json` 的 `serverUrl`。登录页可改地址，改完会先探测再落盘。

静默安装可用 `/SERVER=192.168.1.10:8080` 预配置；已有 `config.json` 会保留，需在程序里更改主机地址。客户端模式每次启动需要输入密码，暂不提供单机版的「记住登录」功能。

### 3.2 首次初始化（只在第一台做）

空库第一次连上时，客户端会提示「创建管理员」：

1. 输入主机安装脚本打印的**初始化令牌**（也在 `<数据目录>\初始化令牌.txt`）。
2. 设置管理员账号与密码（服务器模式要求 ≥10 位）。
3. 创建成功后令牌即失效，可删除该文件。

### 3.3 导入授权

管理员登录 → 设置 → 授权：

- 「导入授权」选择 `license.rferplic`
- 页面显示：客户名、类型（permanent/trial）、客户端额度、支持到期日

也可以在主机上用命令行导入（服务运行中也可，SQLite 允许并发读）：

```powershell
& "C:\Program Files\RFERP\RFERP-server.exe" -config "D:\RFERP数据\server.json" -license "D:\license.rferplic"
```

### 3.4 之后每台客户端

装客户端 → 填同一地址 → 登录。设备会自动登记并占用一个客户端名额。

---

## 4. 授权与设备口径

| 场景 | 行为 |
|---|---|
| 未导入授权 | **仅主机可登录**；客户端被拒（`license_missing`） |
| 额度用满 | 已登记设备照常登录；**新设备被拒**（`device_limit`） |
| 撤销设备 | 该设备会话立即失效，再登录返回 `device_revoked`；**不释放名额** |
| 删除设备 | 释放名额，允许同指纹重新登记 |
| trial/订阅到期 | 业务写入只读；查询、备份、导入授权仍可用 |
| 永久授权 | 支持期到期不影响使用 |

主机（服务端所在机器）不占额度且永远可登录——它是授权锁死时的唯一救场设备。

### 4.1 管理员被锁在外面时

在主机上执行：

```powershell
cd "C:\Program Files\RFERP"
.\RFERP-server.exe -config "D:\RFERP数据\server.json" -devices "list"
.\RFERP-server.exe -config "D:\RFERP数据\server.json" -devices "revoke <指纹前12位>"
```

同理可 `delete` 释放名额。指纹前 12 位即可，前缀有歧义时会报错。

---

## 5. 备份与恢复

### 5.1 备份

| 类型 | 触发 | 位置 | 保留 |
|---|---|---|---|
| 手动 | 设置页「一键备份」 | `备份\manual\` | 永不自动删除 |
| 自动 | 服务定时器（默认关闭，可在设置页开启每日/每周） | `备份\auto\` | 滚动保留 |
| 操作前副本 | 恢复/清空前 | `备份\safety\` | 滚动保留 |

客户端备份页可以直接**创建、列出、下载**主机上的备份：请求经客户端转发到主机，备份始终落在主机的备份目录，本机不留副本；服务端只回文件名与大小，不暴露路径。

**外部备份仍需人工**：把 `备份` 目录定期复制到 U 盘/NAS。主机磁盘坏了，同盘备份一起没。

### 5.2 恢复（仅主机）

1. 停服务：`Get-Service RFERPServer | Stop-Service`
2. 客户端（或主机客户端）发起恢复，或在主机上直接替换数据库文件：
   ```
   备份\manual\<快照>.db  →  D:\RFERP数据\rferp.db
   ```
3. 起服务：`Get-Service RFERPServer | Start-Service`
4. 验证：`/readyz` 200，登录后抽查入库/追溯/审计。

SQLite 整库恢复会**连授权与设备记录一起回到快照时刻**：恢复旧备份后可能出现"某台设备重新可用"或"某台新设备被拒"，属预期行为（已登记可登录、新设备拒绝）。

---

## 6. 升级

- **客户端**：自动更新（现有机制）。
- **服务端**：手动。停服务 → 备份 → 替换 `RFERP-server.exe` → 起服务 → `/readyz` → 冒烟。
- 数据库迁移只向前；升级前必须有备份（迁移流程会自动生成升级前快照）。
- 保留上一版 exe，失败可回滚。

---

## 7. 排障一页纸

| 现象 | 先查什么 |
|---|---|
| 客户端连不上 | ① 两机同网段？② `Test-NetConnection 主机IP -Port 8080`；③ 路由器 AP 隔离/访客网络；④ 主机防火墙规则是否只有 `LocalSubnet` |
| 主机 IP 变了 | 客户端登录页改地址；建议路由器做 DHCP 保留 |
| 服务起不来 | `Get-Service RFERPServer`；看 `<数据目录>\日志
ferp.log`；再看「事件查看器 → Windows 日志 → 应用程序」里来源 `RFERPServer` 的记录（原生服务没有控制台，启动失败原因写在这里）；端口被占看安装日志 |
| 第 3 台设备被拒 | 额度已满。撤销闲置设备（不释放名额）→ 删除设备（释放名额） |
| 设备被拒说"已撤销" | 管理员在设备页恢复：删除该记录后重新登录即可重新登记 |
| 全部提示"缺少设备标识" | 客户端版本过旧（未带 `X-RFERP-Device-ID`），升级客户端 |
| 登录后一直 401 | 会话绑定设备：同一账号在另一台设备登录会踢掉前一台（单用户单会话） |
| 授权无效 | 确认授权文件属于本产品签发（公钥是否匹配）；换钥匙时旧授权仍有效 |
| 日志太大 | 服务端日志 20MB×20 份 + 180 天年龄清理，正常不会撑爆磁盘 |

服务端状态一律以 `/readyz` 为准；`/healthz` 只说明进程在。

服务由 SCM 直接启动 `RFERP-server.exe`（**没有** WinSW 之类的包装器）。因此日志只有两处：
`<数据目录>\日志\`（应用日志）与事件查看器（启动/停止/异常）。失败重启策略由 SCM 提供，
`sc.exe qfailure RFERPServer` 可查。

---

## 8. 构建与打包

### 8.1 服务端

一条命令出包（注入版本与公钥、打 zip）：

```powershell
pwsh deploy/windows/build-server-package.ps1 -Version 1.0.0 -PublicKeyFile C:\keys\public.key
# 产出 dist\RFERP-server-1.0.0-win64.zip
```

包内含：`RFERP-server.exe`、`install-host.ps1`、`uninstall-host.ps1`、
`client.config.json`、`license-public.key`、`README.txt`。

手工构建（仅排障用）：

```bash
go build -trimpath -ldflags "-s -w -X main.version=1.0.0 -X app/internal/license.DefaultPublicKeyBase64=<公钥>" -o dist/RFERP-server.exe ./cmd/server
```

### 8.2 客户端安装包

```powershell
# 先确保前端产物是最新的
cd web; npm run build:embed; cd ..
pwsh deploy/windows/build-client-installer.ps1 -Version 1.0.0
# 产出 dist\RFERP-1.0.0-setup.exe
```

> 手工构建 exe 时必须带 `-tags "desktop,production"`，否则启动会弹
> "Wails applications will not build without the correct build tags"。
> 上面的脚本已经包含该标签。

安装包支持静默预配置（售后批量部署）：

```powershell
RFERP-1.0.0-setup.exe /VERYSILENT /SERVER=192.168.1.10:8080
```

参数会把 `mode=client` 写进 `%APPDATA%\RFERP\config.json`，客户端首次启动直接是登录页。

### 8.3 版本门禁

服务端 `server.json` 可加：

```json
"minClientVersion": "1.0.0"
```

低于它的客户端在登录与所有已认证请求上回 426，客户端启动时也会直接提示升级。
留空表示不设门禁。

### 8.4 脚本编码（改脚本前必读）

`deploy/windows/*.ps1` 必须保存为 **UTF-8 with BOM**。Windows PowerShell 5.1 对
无 BOM 的 UTF-8 文件按 ANSI（中文机器上是 GBK）解析，中文注释会把语法撑坏，
表现为 "语句块或类型定义中缺少右 }"、"RedirectionNotSupported" 之类的解析错误，
脚本一行都执行不了。改完可以用：

```powershell
$e=$null; [void][System.Management.Automation.Language.Parser]::ParseFile('<路径>',[ref]$null,[ref]$e); $e.Count
```

自检（返回 0 才算通过）。

### 8.5 自动化验收

`deploy/windows/smoke-lan.py` 用设备指纹模拟多台客户端，覆盖额度、撤销、
只读、到期、篡改授权、426 门禁、并发与备份；`jitter-proxy.py` 可注入延迟与断链。
详见 `docs/plans/lan-v1-implementation.md` §3.1。

### 8.6 签名

安装包与 exe 的代码签名需要代码签名证书，本仓库不包含证书与签名步骤。
未签名的 exe 更容易被杀软拦（R15），交付前请按公司证书流程签名。

---

## 9. 安全边界（务必向客户说明）

- 传输是**受控局域网内的明文 HTTP**，依赖 WPA2 以上的 WiFi 与"不连公共网络"。
- 防火墙只放行 `LocalSubnet`；**不做**公网、VPN、TLS（后置）。
- 授权校验是离线签名，不联网；设备共享/复制客户端属于 EULA 约束范围。
- 主机不承诺 24×7：供电、网络、硬件由客户负责。
