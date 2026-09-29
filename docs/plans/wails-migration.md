# 计划：从 Fyne 迁移到 Wails + Web UI（待处理）

> 状态：**规划中，未开工**。架构前置决策已于 2026-09-29 定案（见 §4.0）。
> 部署前提：**桌面端内嵌 WebView2**、**一次性迁移**、**近期不需要多机互联**（只需为未来互联留好接口）。
> 创建于 2026-09-23；同步于 2026-09-24（对照 `main` @ `4d411c8`）；
> **2026-09-29 补充服务器层缺口核查（对照 `main` @ `eef5905`）——见 §4.5。**

---

## 0. 背景

现有桌面端基于 **Go + Fyne v2.4.5**，`internal/ui/` 下一屏一文件。功能拓展性尚可（加页面容易），
但**体验与样式拓展性差**：Fyne 无数据网格/树/日期选择器，表格与图表（`stats.go` 的 `plotSeries`）靠手写；
无设计系统、无热重载、中文文案硬编码、复杂交互上限低。Fyne 生态也**没有后台管理模板**级别的成品。

**有利前提**：ports 重构（PR #17/#18/#19，已合并进 `main`）后，UI 只依赖 `usecase.Applications` 接口，
并已具备完整 HTTP API（`cmd/server`，`internal/api`）与**双运行模式**（本机直连 / 局域网远程客户端）——**表现层可替换，迁移是"换壳"**。

---

## 0.1 当前代码基线（已核对，同步于 2026-09-24）

> 本节记录迁移开始前 `main` 分支的真实状态，供后续会话对照，避免误判"已具备/缺失"。

### 模块与依赖
- 模块 `app`，`go 1.26`；核心依赖：`fyne.io/fyne/v2 v2.4.5`、`go-sql-driver/mysql v1.8.1`、
  `jmoiron/sqlx v1.3.5`、`DATA-DOG/go-sqlmock v1.5.2`、`golang.org/x/sys v0.13.0`
- **尚无 Wails 依赖，无 `web/` 目录** —— 迁移确实未开工

### 入口（`cmd/`）
| 目录 | 作用 |
|---|---|
| `cmd/desktop` | 现有 Fyne 桌面入口（约 210 行；含模式选择 → 登录 → 主界面） |
| `cmd/server` | HTTP API 服务（本地内嵌 / 远程独立部署两用） |
| `cmd/keygen` | 生成密钥（更新签名相关） |
| `cmd/signmanifest` | 清单签名（更新包） |

### 内部包（`internal/`，20 个）
`api`、`auth`、`config`、`dbbackup`、`dbsetup`、`logging`、`migrate`、`model`、`mysqlfind`、
`nativefiledialog`、`paths`、`repository`、`secret`、`service`、`singleinstance`、`ui`、`update`、
`usecase`、`winappid`、`winmsg`

- `usecase/`：`ports.go`（`Applications` 及各 Port 接口）、`usecase.go`、`inputs.go`、
  `permissions.go`（`Allow(role, name)` 操作点矩阵）、`stats.go`
- `api/`：`server.go`（**46 条路由**，裸 `http.ServeMux`，无任何中间件）、`client.go`（实现 `usecase.Applications`，供客户端模式）、
  `catalog.go`、`admin.go`
- `auth/`：`auth.go`（`ModuleDashboard/Stats/Products/Parts/BOM/Batch/Audit/Backup/Users`、
  `moduleAccess` 矩阵）、`session.go`（`CanRead(module)` 菜单可见性）

### UI 屏幕（`internal/ui/`，24 个 .go 文件）
`app`、`dashboard`、`stats`、`product`、`part`、`bom`、`batch`、`audit`、`backup`、`users`、
`login`、`runmode`、`setup`、`update`、`theme`、`logo`、`util`，
另加 `searchselect`（BOM 选择器）、`colfit`（列宽自适应），以及 5 个测试文件

- 页面装配集中在 `app.go` 的 `BuildUI()`：`[]pageDef{ navItem{auth.ModuleX, "名称", icon}, page, refresh }`，
  按 `auth.CanRead(module)` 过滤菜单
- 导航当前为：工作台、统计分析、产品管理、零件管理、BOM管理、批次追溯、操作记录、备份导出、用户管理
- 非页面流程：`ShowRunModePicker` / `ShowRunModeSwitch`（模式选择/切换）、
  `ShowLogin` / `TryAutoLogin` / `showCreateAdmin`（登录与首启建管理员）、`ShowSetup`（数据库初始化）

### HTTP API 面（`internal/api/server.go`）
`GET /healthz`、`POST /api/login`、`POST /api/bootstrap-admin`、`GET /api/users/count`、`GET /api/me`，
以及带 `s.auth(handler, "权限点")` 的业务路由：`/api/products`、`/api/parts`、`/api/parts/stock-in`、
`/api/parts/adjust-stock`、`/api/bom`、`/api/batches`、`/api/batches/status`、`/api/batches/revoke`、
`/api/batches/skip`、`/api/traces/*`、`/api/audit`、`/api/audit/recent`、`/api/stats`、
`/api/export/audit`、`/api/export/all`、`/api/backup`、`/api/users/*`

### 运行模式（`internal/config`）
- `ModeLocal`（本机直连业务内核）/ `ModeClient`（连接局域网主机 API，不直连业务库）
- `SetRunMode(mode, serverURL)` 持久化；`api.NewClient(cfg.ServerURL)` 构造客户端 `Applications`

### 其它已具备
- `deploy/docker/`：`Dockerfile`、`docker-compose.yml`、`.env.example`、`README.md`
- `.github/workflows/ci.yml`；`docs/rulesets/`（`main-flow.json`、`main-safety.json`）
- 打包：`build.bat`、`build-installer.bat`、`release.bat`、`setup.iss`、`lang/ChineseSimplified.isl`
- 更新：`internal/update`、`internal/singleinstance`、`rferp-update-public.key`、`changelog.ps1`
- 文档：`README.md` / `README.zh-CN.md` / `CONTRIBUTING.md`

### 迁移需覆盖的完整屏幕清单（含新增项）
> 相比原计划第 4 阶段，现项目已多出 **统计分析、用户管理、运行模式、登录、初始化、更新** 等界面。

工作台、统计分析、产品、零件、BOM、批次、追溯、操作记录、备份导出、用户管理、
登录/首启建管理员、运行模式选择/切换、数据库初始化、（可选）更新提示

---

## 1. 结论：仍然是 exe

采用 **Wails v2**，产出**单个 Windows exe**：前端资源内嵌，用系统自带 **Edge WebView2** 渲染。

- 用户双击即用，**不需要** Node / 浏览器 / Go
- Inno Setup 打包流程沿用
- exe 体积约 15–40 MB（视是否附带 WebView2 运行时）
- 额外收益：**同一套前端**可同时发布**浏览器版**（局域网多人），代码完全相同

---

## 2. 技术栈

| 层 | 选型 | 备注 |
|---|---|---|
| 外壳 | Wails v2 | v3 尚在推进，暂不用 |
| 前端 | React 18 + TypeScript + Vite | 或 Vue（待决策） |
| 样式 | Tailwind CSS | 完全自由的像素级控制 |
| 组件 | **shadcn/ui**（Radix + Tailwind，MIT） | 复制进仓库，可任意改 |
| 数据网格 | TanStack Table（headless） | 或 AG Grid Community |
| 表单/校验 | React Hook Form + Zod | |
| 图表 | Apache ECharts | 交互/缩放/提示开箱即用 |
| 数据请求 | TanStack Query | 缓存、重试、加载态 |
| 国际化 | i18next | 解决中文硬编码问题 |
| 图标 | Lucide | |

**架构关键**：本地与远程**统一走 HTTP**。本地把 `cmd/server` 内嵌到 `127.0.0.1:随机端口`，
每次启动生成随机 token 注入前端。前端只有一套代码路径，且可直接被浏览器访问。

```
Wails 外壳(Go) --内嵌--> cmd/server(127.0.0.1:随机端口) --> service/usecase --> MySQL
浏览器 -----------------> 同一 cmd/server(VPN 虚拟网卡地址)
```

> **接入方式（2026-09-29 定案）**：无域名，故**不暴露公网、不做 TLS**——
> 远端通过 VPN 组网（Tailscale / ZeroTier / WireGuard）访问服务端所在机器的虚拟网卡地址。
> 攻击面为零，证书与 CORS 问题一并消失。

> 现状契合：`internal/api/client.go` 已实现 `usecase.Applications`，客户端模式本就通过 HTTP 走
> `cmd/server`。Web 前端可复用**同一套 `/api/*`**，无需另造接口。

---

## 3. 目标目录结构（对照现状）

```
RFERP/
├─ cmd/
│  ├─ desktop/              # 保留：现有 Fyne 版（过渡期回退用）
│  ├─ server/               # 已有：HTTP API（本地内嵌 / 远程独立部署）
│  ├─ keygen/               # 已有：更新签名密钥生成
│  ├─ signmanifest/         # 已有：更新清单签名
│  └─ desktop-wails/        # 【新增】Wails 入口（main.go + wails.json）
├─ internal/                # 不动：api / auth / config / dbbackup / dbsetup / logging /
│                           #       migrate / model / mysqlfind / nativefiledialog / paths /
│                           #       repository / secret / service / singleinstance / ui /
│                           #       update / usecase / winappid / winmsg
├─ web/                     # 【新增】前端工程（主体）
│  ├─ package.json / vite.config.ts / tsconfig.json / tailwind.config.js
│  └─ src/
│     ├─ main.tsx / App.tsx
│     ├─ api/client.ts             # baseURL + Bearer + 401/错误统一处理
│     ├─ api/types.ts              # Go model.* → TS 类型
│     ├─ components/ui/            # shadcn/ui
│     ├─ components/AppShell.tsx   # 侧栏 + 顶栏 + 内容区
│     ├─ components/DataTable.tsx  # TanStack Table 通用表格
│     ├─ components/FormDialog.tsx # RHF + Zod 通用表单弹窗
│     ├─ features/                 # 一屏一目录，对应现有 ui/*.go
│     │  ├─ dashboard/ stats/ products/ parts/ bom/ batches/
│     │  └─ trace/ audit/ backup/ users/ runmode/ login/ setup/
│     ├─ i18n/                     # zh-CN / en
│     └─ lib/                      # 权限矩阵映射、工具
├─ deploy/docker/           # 已有：可加前端静态托管 nginx 示例
├─ docs/rulesets/           # 已有：业务规则集（迁移后应保持行为一致）
├─ lang/ChineseSimplified.isl  # 已有：Inno Setup 中文语言
├─ setup.iss / release.bat / build-installer.bat  # 已有：打包链
└─ .github/workflows/ci.yml # 已有：增加前端 job
```

---

## 4. 落地清单

### 阶段 -1 · 架构前置决策（**已于 2026-09-29 定案**）

**定案结论：**

| 项 | 决定 | 理由 |
|---|---|---|
| 前端框架 | **React + TypeScript + shadcn/ui** | shadcn/ui 构建在 Radix 上，**只有 React 版**；源码进仓库、无依赖锁定 |
| 前端托管 | **由内嵌的 `cmd/server` embed 托管 `web/dist`** | **同源** → 不需要 CORS；启动 token 可直接保护根路径；浏览器版复用同一二进制 |
| 会话存储 | **维持进程内存** + 补登出端点与过期清理 | 单实例已定；"多实例"是尚未发生的需求，不提前写代码 |
| 远端接入方式 | **VPN 组网（Tailscale / ZeroTier / WireGuard）**，不暴露公网 | **无域名** → 证书路径全部不成立；2–3 人规模无需把 ERP 暴露到公网 |

**因此"零 TLS / 零 CORS"不再是阻塞项**——改为 VPN 组网后，攻击面为零，证书与同源问题一并消失。

开发期注意：采用 embed 托管后，`wails dev` 的一键热重载不可用，改为
`Vite dev server :5173 --proxy--> cmd/server :54321`，Wails 窗口在开发期指向 `:5173`、生产期指向 `:54321`。

### 阶段 0 · 准备（半天）
- [ ] 安装 Node LTS + npm/pnpm
- [ ] `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
- [ ] `wails doctor` 确认 WebView2 可用

### 阶段 1 · 前端骨架（1–2 天）
- [ ] `web/` 用 Vite 初始化（React + TS）
- [ ] 接入 Tailwind + shadcn/ui（`npx shadcn@latest init`）
- [ ] 定主题：色板、字号、圆角、暗色模式（可沿用 Fyne 版 `theme.go` 的色板）
- [ ] `AppShell`：导航项与 `internal/ui/app.go` 的 `pageDef` **一一对应**
- [ ] `api/client.ts` + `api/types.ts`
- [ ] i18n 骨架（zh-CN / en）

### 阶段 2 · 打通一条链路（1–2 天）
- [ ] 登录页 → `POST /api/login` 取 token
- [ ] 零件管理：列表 → 新增/编辑 → 入库/盘点
- [ ] 按角色隐藏菜单/按钮：菜单可见性复用 `auth.CanRead(module)`；
      操作点复用 `usecase.Allow(role, name)`（与 API 的权限点字符串一致）
- [ ] 与 Fyne 版对照，行为一致
- [ ] **补登出端点**（`internal/api` 目前 `logout` 零命中，"退出登录"只清前端 token，服务端会话条目永久留存）
- [ ] **加过期会话清理协程**（当前只在"被再次使用"时惰性删除，map 无界增长）

### 阶段 3 · Wails 外壳（1 天）
- [ ] `cmd/desktop-wails/`：内嵌 `cmd/server` 于 `127.0.0.1:随机端口`
- [ ] 每次启动生成随机 token 注入前端
- [ ] `wails build` 产出单 exe；`setup.iss` 加 `#define` 切换产物
- [ ] 托盘 / 单实例 / 自动更新：复用 `internal/singleinstance`、`internal/update`、
      `internal/winappid`、`internal/winmsg`
- [ ] `internal/nativefiledialog` 的两个调用点（改数据目录、初始化向导）需替代方案：
      文本输入或目录枚举端点，WebView 没有原生文件夹选择框
- [ ] `http.Server` 补 `ReadTimeout` / `WriteTimeout` / `IdleTimeout`（当前是裸 `ListenAndServe`）

### 阶段 4 · 逐屏迁移（每屏 0.5–1.5 天；备份导出屏单独留 2–3 天）
> 完整清单见 §0.1「迁移需覆盖的完整屏幕清单」。

- [ ] **先补齐服务器侧的文件类用例**（迁到「备份导出」屏**之前**）：
      - 4 个用例**无 HTTP 端点**：`RestoreDatabase` / `ClearDatabase` / `DataDir` / `SetDataDir`
      - **零文件上传能力**：46 条路由无一个 multipart，备份导入无从上传
      - **备份下载**：`POST /api/backup` 现在返回**服务器绝对路径**给前端，
        浏览器需要的是文件流；返回路径属于**信息泄露**（暴露服务器文件系统布局）
- [ ] 登录/首启建管理员 → 运行模式选择 → 数据库初始化
- [ ] 工作台 → 统计分析 → 产品 → 零件 → BOM → 批次 → 追溯 → 操作记录 → 备份导出 → 用户管理
- [ ] （可选）更新提示

### 阶段 5 · CI / 打包 / 发布（1 天）
- [ ] CI 增加前端 job（可与 Go job 并行；**Action 仍固定到 commit SHA**）
- [ ] `wails build` 纳入发布脚本；`release.bat` 产出改为「Wails exe + 安装包」
- [ ] `cmd/server` 用 `embed` 托管前端 dist
- [ ] 部署文档：单实例 / 重启即全员登出 / 远连需 VPN / 不暴露公网
- [ ] README 更新（构建者需 Node；**端用户仍无需任何运行时**）

### 阶段 6 · 切换与收尾

已确认采用**一次性迁移**（不并行发布）。相应调整：

- [ ] **保留 Fyne 版的安装包**（如 v1.2.3）挂在 Release 页作为**用户可自行下载的降级通道** ——
      一次性切换意味着一旦新版本有严重缺陷，Fyne 版不再随路发布，用户否则只能干等
- [ ] 保留 `cmd/desktop` 与其构建脚本（代码不删，便于紧急修复时回退）
- [ ] 每屏验收必须**对照同一数据库**与 Fyne 版逐项比对（无并行验证的情况下，这是唯一防线）

---

## 4.5 服务器层缺口清单（2026-09-29 核查，对照 `main` @ `eef5905`）

原计划覆盖了前端与外壳，但**服务器侧（`cmd/server` / `internal/api`）此前按"可信内网"假设写**。
定案为「VPN 组网 + 同源托管」后，**TLS 与 CORS 不再是阻塞项**；但文件类能力与用例覆盖的缺口仍须补齐。全部基于实际代码核查，非推测。

### 🔴 硬前提

> **原判断已因"VPN 组网 + embed 同源托管"而失效**：不暴露公网就没有 TLS 与 CORS 的需求。
> 保留记录以备将来若要"浏览器直接公网访问"时重新评估。

| # | 缺口 | 证据 | 处置 |
|---|---|---|---|
| 1 | ~~**零 TLS**~~ | `cmd/server/main.go:53` `http.ListenAndServe`；注释自认「公网部署前必须加 TLS（C4）」| ✅ **VPN 组网下不适用**（不监听公网）。若将来要公网直连 → 前置 Caddy 反代（勿自管证书）|
| 2 | **CORS / 同源** | `api.New(...).Handler()` 返回裸 `http.ServeMux`，**46 条路由零中间件** | ✅ **embed 同源托管后不需要 CORS**。仍需补 `http.Server` 超时与基础中间件 |

### 🟡 功能缺口

| # | 缺口 | 证据 | 处置 |
|---|---|---|---|
| 3 | **零文件上传** | 全仓库 `multipart` / `FormFile` 零命中 | 阶段 4 前补端点 |
| 4 | **4 个用例无端点** | `RestoreDatabase` / `ClearDatabase` / `DataDir` / `SetDataDir`；权限点已定义但无路由消费 | 阶段 4 前补端点 |
| 5 | **备份下载返回服务器路径** | `client.go:424-433` 丢弃 `saveDir`，返回服务端绝对路径 | 改为文件流；现形态属信息泄露 |
| 6 | **无登出端点** | `internal/api` 中 `logout` 零命中；`ui/app.go:106-111` 的"退出登录"只调本地 `auth.Logout()`，**`Client.tok` 不清** | 阶段 2 补 |
| 7 | **无超时 / 无限流 / 无安全响应头** | 无 `ReadTimeout`/`WriteTimeout`/`IdleTimeout`；登录失败不计数不锁定 | 阶段 3/5 补 |
| 8 | **`nativefiledialog` 无对应物** | `SHBrowseForFolderW` 仅 `ui/backup.go:223`、`ui/setup.go:54` 两处调用 | 阶段 3 定替代方案 |

### 服务端与桌面端的行为不一致（迁移时需注意）

- `cmd/server` 的日志直接铺在数据目录根下（`main.go:31`），桌面端是 `<dataDir>/日志/`（`main.go:116`）
- `cmd/server` **只支持 MySQL**（`main.go:34` 硬编码 `sqlx.Connect("mysql", ...)`，连 `IsSQLite()` 都不检查）→ 多用户场景下与现有设计一致，**不需要动存储层**
- `cmd/server` 完全不读 `updateUrl` / `autoUpdate` / `mysqlService` / `storage` / `mode` / `serverUrl` → 服务端**无任何自动更新代码路径**
- `config.Srv.Addr`（`SRV_ADDR` 环境变量）在 server 端是**死配置**，`cmd/server` 只认 `-addr` flag
- `auth.CanRead/CanWrite` 读的是**进程级全局变量**（`auth/session.go:12`）→ WebUI 里前端权限门需由服务端下发，**且不能把前端门当安全边界**（服务端 `s.auth` 每次请求重算 + `usecase.Allow` + 未知角色降级 viewer，这层是正确的）

## 4.6 会话存储与多实例

### 现状

```
登录 → 生成 48 位随机 token → 存入进程内存 map[string]*session → 返回前端
每请求 → Bearer token → 查 map → 放行
```

`internal/api/server.go:33-34`（map + mutex）、`:233-243`（签发，12 小时绝对过期）、
`:245-261`（校验，仅在"被再次使用"时惰性删除过期项）。

### 约束

| 情况 | 后果 |
|---|---|
| 进程重启 | map 清空 → 所有在线用户被集体登出 |
| 跑 2 个 `cmd/server` 实例 | 各自 map 互不相通 → A 签发的 token 到 B 返回 401 |

→ **当前实现不支持多实例。** 单实例部署不受影响。

### 三种方案

| 方案 | 多实例 | 主动登出 | 成本 |
|---|---|---|---|
| 现状（内存 map）| ❌ | ❌ | 零 |
| **存数据库** | ✅ | ✅ | **零新增依赖**（已有 MySQL/SQLite 抽象）；每请求多一次查库，当前规模可忽略 |
| Redis | ✅ | ✅ | 引入新组件（部署 / 运维 / HA），当前用户规模属杀鸡用牛刀 |
| HMAC 自包含 token（不落存储）| ✅ | ❌ 只能等过期 | 零存储，但登出体验变差 |

若选「存数据库」：会话表属于**系统表**，须与 `users` / `schema_migrations` / `db_identity`
一并**排除在整库恢复之外**（同 `internal/service/tables.go` 的 `preservedTables` 语义）。

### 与多实例无关、现在就应补的两项

1. **登出端点** —— 缺失，纯增量
2. **过期会话清理协程** —— 当前 map 无界增长（内存泄漏）

### 为未来互联留口的低成本准备

将来若要"多台机器连一台服务器"，需要改动的只有**绑定地址**，业务与会话都无需变
（单台服务器仍是单实例）。建议在迁移期就把两处收窄，将来只改一处：

- **会话存取收在窄接口后**（如 `SessionStore` 接口，内存实现先落地）——
  届时换数据库/Redis 实现只动一个文件，不用改 `Server` 结构体
- **监听地址显式化** —— `config.Srv.Addr`（`SRV_ADDR`）目前是**死配置**
  （`cmd/server` 只认 `-addr` flag）。把它接上，并允许"绑定局域网网卡"成为纯配置行为

另有一项属于**版本兼容**，建议在首次远端部署前完成：

- **API 统一版本前缀** —— 目前 46 条路由中只有 `GET /api/v1/serverinfo` 带 `v1`
  （`internal/api/server.go:70`），其余都是 `/api/...`。远端客户端一旦出现版本滞后
  （旧桌面版 / 旧浏览器页连新服务器），没有版本前缀就无法协商与拒绝

---

## 4.7 阶段 0/1 期间应一并完成的"未来互联准备"

| 优先 | 项 | 现在做的理由 |
|---|---|---|
| **T1（迁移期做）** | 前端由 `cmd/server` embed 托管 | 本地与远端同一份代码路径，一次投入两处受益 |
| T1 | 前端 `baseURL` **可配置**，不写死 `127.0.0.1` | 远端只需改配置，不改前端代码 |
| T1 | 会话存取收窄接口（`SessionStore`），内存实现先落地 | 将来换实现只动一个文件 |
| T1 | 部署文档写明：单实例 / 重启即全员登出 / 远连需 VPN | 避免误部署与"以为是 bug" |
| **T2（首次远端部署前）** | `cfg.Srv.Addr` 接上、允许绑定局域网网卡 | 到时零代码改动 |
| T2 | API 统一版本前缀 | 远端版本协商的前提 |
| T2 | 启动 token 与绑定地址解耦（本地必需、远端可配置） | 两种暴露面共用一套鉴权 |

---

## 5. 关键文件要点

**`web/src/api/client.ts`**
```ts
const base = () => (window as any).__RFERP_API__ ?? "http://127.0.0.1:8080";
let token = "";
export function setToken(t: string) { token = t; }

export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(base() + path, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error ?? `HTTP ${res.status}`);
  return data as T;
}
```

**`cmd/desktop-wails/main.go`（要点）**
```
启动内嵌 cmd/server 于 127.0.0.1:随机端口 → 生成随机 token
→ wails.Options{ JS: "window.__RFERP_API__='http://127.0.0.1:PORT'" }
复用 internal/service、internal/usecase、internal/api、internal/singleinstance、
    internal/update、internal/winappid、internal/winmsg
```

**`.github/workflows/ci.yml` 新增 job**
```yaml
  web:
    name: Web / Build & lint
    runs-on: ubuntu-latest
    defaults: { run: { working-directory: web } }
    steps:
      - uses: actions/checkout@<sha>
      - uses: actions/setup-node@<sha>
        with:
          node-version: '20'
          cache: npm
          cache-dependency-path: web/package-lock.json
      - run: npm ci
      - run: npm run lint
      - run: npm run build
```

---

## 6. 验收标准

| 阶段 | 验收 |
|---|---|
| 1 | `npm run dev` 打开空壳，侧栏与现有菜单一致 |
| 2 | 登录 + 零件增删改查/入库，与 Fyne 版结果一致（对照同一数据库） |
| 3 | `wails build` 产出 exe，双击即用，无外部依赖 |
| 4 | 每迁移一屏，功能与权限行为逐项对照通过（含 `docs/rulesets/` 规则） |
| 5 | CI 前端 job 绿；`release.bat` 能产出安装包 |
| 6 | 全屏对齐，Fyne 版可下线 |

---

## 7. 待决策

### 已定（2026-09-29）

| 项 | 决定 | 理由 |
|---|---|---|
| 交付形态 | **桌面端内嵌 WebView2**（Wails v2 单 exe）| 用户无需 Node / 浏览器 / Go |
| 前端框架 | **React + TypeScript + shadcn/ui + Tailwind** | shadcn/ui 构建于 Radix，仅有 React 版；源码进仓库 |
| 前端托管 | **由内嵌 `cmd/server` embed 托管 `web/dist`（同源）** | 免 CORS；启动 token 可保护根路径；浏览器版复用同一二进制 |
| 会话存储 | **维持进程内存** + 补登出端点与过期清理 | 单实例已定；多实例是尚未发生的需求 |
| 远端接入 | **VPN 组网，不暴露公网** | **无域名** → 证书路径不成立；2–3 人规模无必要把 ERP 暴露公网 |
| 部署规模 | 单实例 `cmd/server` | 多机互联近期不做，仅留接口（见 §4.7）|
| 过渡策略 | **一次性迁移**（不并行发布）| 需保留 Fyne 版安装包作为用户可自行下载的降级通道 |

### 仍待决策

无阻塞项。迁移过程中如遇新岔路再定。

### 已知会随迁移消失的维护负担

删除 Fyne 版后，以下会**自动消失**，无需专门处理：

- `golang.org/x/net`、`golang.org/x/image`、`golang.org/x/text`、`github.com/yuin/goldmark`
  四个模块（33 个已知漏洞）**均由 Fyne 引入**，移除后依赖树自动收缩、告警归零
- 唯一例外：`filippo.io/edwards25519`（2 个漏洞）随 `go-sql-driver/mysql` 引入，需保留 →
  届时顺手从 `v1.1.0` 升到 `v1.2.0` 即可

（结论：不建议现在单独做依赖升级——Fyne 移除时 MVS 会直接选到新版本，那次覆盖升级会变成无用功。）

---

## 8. 风险与回退

- **工作量**：中偏大，每屏重写（代码量约为 Fyne 版 2–4 倍），但观感与交互质变
- **新工具链**：引入 Node/npm，CI 与打包脚本需改
- **过渡期两套 UI**：必须隔离（`internal/ui` 与 `web/` 互不依赖）
- **WebView2**：Win10/11 基本自带；老旧机器可在安装包内附带运行时
- **权限一致性**：菜单与操作点须同时对齐 `auth.CanRead` 与 `usecase.Allow`，避免越权/漏权
- **回退**：Fyne 版代码始终保留；一次性迁移下，另需把 Fyne 版安装包挂在 Release 页作为用户可自行下载的降级通道
- **服务器侧是"可信内网原型"而非产品**：文件上传下载、4 个未暴露的用例、登出、超时等缺口仍须补齐（详见 §4.5）；TLS/CORS 因改用 VPN + 同源托管而不再是阻塞项

---

## 9. 明确不选

- **Gio**：自由度略高但仍即时模式、控件生态小、无模板
- **Tauri**：需引入 Rust，等于换栈
- **Qt 绑定**：许可证与维护状况不理想
