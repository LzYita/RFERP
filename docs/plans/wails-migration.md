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
业务层具备复用基础；`cmd/server` + `internal/api` 已提供 HTTP API（46 条路由，**41 条挂了鉴权中间件**，
做身份校验 + `usecase.Allow` 权限点校验）与**双运行模式**（本机直连 / 局域网远程客户端）的接缝。

但**迁移不等于"只换外壳"**，仍有必须一并完成的接口与生命周期改造：

| 仍需补齐 | 详见 |
|---|---|
| 本地 **SQLite** 接入内嵌服务（`cmd/server` 目前硬编码 MySQL） | §4.5、D3 |
| 服务装配从命令入口下沉为可复用包（`cmd/desktop` 与 `cmd/server` 共用） | §4 阶段 1 |
| 生命周期：随机端口、启动就绪等待、退出清理 | §4 阶段 1 |
| 本机端口防护（启动 token）、超时、限流、登出端点 | §4.5、§4.6 |
| 5 个无端点的用例（恢复 / 清空 / 数据目录 / MySQL 启停） | §4.5 |

---

## 0.1 当前代码基线（已核对，同步于 `main` @ `f3448cd`，2026-10-02）

> 本节记录迁移开始前 `main` 分支的真实状态，供后续会话对照，避免误判"已具备/缺失"。
> 历史基线：`eef5905`（2026-09-29 核查时）、`2f69bd8`、`caef637`、`f3448cd`，仅作历史依据。
> **每次进入新轮次前应重新核对本文引用的行号与结论**——行号会随代码变动失效。

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

- 用户双击即用，**不需要** Node / 浏览器 / Go 工具链
- **前提：机器上有 WebView2 运行时。** Win10 21H2 / Win11 基本自带；
  老旧机器由**安装包检测并引导下载**，不内置运行时（内置会使安装包从约 18 MB 膨胀到 130 MB 量级）
- Inno Setup 打包流程沿用
- exe 体积**粗估** 15–40 MB（视是否附带 WebView2 运行时；未附带时按现有 33 MB 基线）

**分阶段**（重要）：本计划首轮只做**阶段 A 单机桌面**。阶段 B（多机桌面客户端互联）与
阶段 C（浏览器）都在阶段 A 稳定之后，详见 §7 与 §4.5 能力矩阵。

---

## 2. 技术栈

| 层 | 选型 | 备注 |
|---|---|---|
| 外壳 | Wails v2 | v3 尚在推进，暂不用 |
| 前端 | **React 18 + TypeScript + Vite** | 已定，2026-10-02 |
| 样式 | Tailwind CSS | 完全自由的像素级控制 |
| 组件 | **shadcn/ui**（Radix + Tailwind，MIT） | **项目选型**：复制进仓库、无依赖锁定、可任意改 |
| 数据网格 | TanStack Table（headless） | 或 AG Grid Community |
| 表单/校验 | React Hook Form + Zod | |
| 图表 | Apache ECharts | 交互/缩放/提示开箱即用 |
| 数据请求 | TanStack Query | 缓存、重试、加载态 |
| 国际化 | i18next | 解决中文硬编码问题 |
| 图标 | Lucide | |

**架构关键**：本地与远程**统一走 HTTP**。本地把服务内嵌到 `127.0.0.1:随机端口`，
每次启动生成随机 **启动 token**（`Set-Cookie`，`HttpOnly` + `SameSite=Strict`）随根页面下发。
前端只有一套代码路径，**永远不直接访问数据库**。

> 这条约束是阶段 A/B/C 三阶段共用的前提：只有前端走 HTTP，阶段 B（多机桌面客户端连一台服务器）
> 才是"在另一台机器上启动同一个服务"，而不是重写数据访问层。

```
Wails 外壳(Go) --内嵌--> 服务(127.0.0.1:随机端口) --> service/usecase --> repository.Store --> MySQL / SQLite
桌面客户端(阶段 B) ------> 同一服务(server 机器的 VPN 虚拟网卡地址)
浏览器     (阶段 C) ------> 同一服务(server 机器的 VPN 虚拟网卡地址)
```

> **接入方式（2026-10-02 复核，结论不变、理由已改写）**：
> 远端通过 VPN 组网（Tailscale / ZeroTier / WireGuard）访问服务端所在机器的虚拟网卡地址。
>
> **暂不考虑公网部署。** 理由是成本与风险，不是技术不可能：
> 2–3 人规模下，把 ERP 暴露到公网所需的证书轮换、暴力破解防护与 WAF 投入，高于其收益。
>
> 需要澄清的两点：
> - **VPN 减少网络暴露，但不替代安全措施**——认证、会话管理、服务端授权、监听地址限制、
>   文件操作校验仍须逐项具备。已具备的部分见 §4.5，未具备的见 §4.6 与 §4.5 能力矩阵。
> - **"无域名"不等于无法使用 TLS**（可签 IP 证书，或自签 + 客户端信任）。
>   VPN 隧道本身已对传输加密（WireGuard/Tailscale），故本阶段不做 TLS 是**取舍**而非**不能**。
>   传输安全须按"本机回环"与"跨机部署"分别评估，不能用一句话覆盖两种场景。
>
> **启动 token 只属于阶段 A**：它是本机 `127.0.0.1` 端口的防护（阻止同机其他进程访问），
> 对跨机可达的 server 没有意义。阶段 B/C 只有会话 token + VPN。

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
├─ docs/rulesets/           # 已有：**GitHub 仓库治理规则集的版本化存档**（main-flow / main-safety）。GitHub 不读取此目录，仅供评审与重建。迁移不应改变这些规则
├─ lang/ChineseSimplified.isl  # 已有：Inno Setup 中文语言
├─ setup.iss / release.bat / build-installer.bat  # 已有：打包链
└─ .github/workflows/ci.yml # 已有：增加前端 job
```

---

## 4. 落地清单

### 阶段 -1 · 架构前置决策（2026-10-02 定案）

**定案结论：**

| 项 | 决定 | 理由 / 状态 |
|---|---|---|
| 首轮交付范围 | **阶段 A 单机桌面**，能力不缩水；阶段 B/C 后续 | 阶段 B/C 会把"换壳"变成"换壳 + 把 server 做成产品"，工作量与风险不是一个量级 |
| 前端框架 | **React + TypeScript + shadcn/ui + Tailwind** | shadcn/ui 构建在 Radix 上，只有 React 版；源码进仓库、无依赖锁定。已最终确定 |
| 前端托管 | **内嵌 HTTP 服务 + 前端从 `127.0.0.1:随机端口` 加载** | 同源免 CORS；且这是阶段 B 成立的前提。**具体实现待阶段 1 关口验证后才写死** |
| 服务装配 | `cmd/desktop` 与 `cmd/server` **共用一个服务装配包**，接受 `repository.Store` | 否则阶段 A/B 是两份装配逻辑，D-016 的"就地升格"不成立 |
| 本地 SQLite | **保留** | `repository.Store` 已由 `*Repository` 与 `*SQLiteStore` 编译期满足（`store.go:104,106`），只需装配层下沉 |
| 鉴权 | **完整登录体系** | HTTP 层已有 41/46 路由挂 `s.auth`（身份 + `usecase.Allow`），需补齐而非重建 |
| 远端能力矩阵 | 恢复 / 清空 / 改数据目录 / MySQL 启停 **在阶段 B/C 禁用** | 四道确认门建立在"操作者就是这台机器的主人"上；跨机时该前提不成立 |
| 远端接入方式 | **VPN 组网**，不暴露公网 | 见 §2 改写后的表述 |
| 过渡策略 | **一次性切换**，但**每轮迁移设实机关口** | 见下方"关口" |
| 旧版 UI 定位 | **仅作回退出口**：保留可构建入口与旧安装包，不冻结自动更新 | 冻结会让 Fyne 用户永久拿不到 bug 修复；回退出口是"旧安装包"而非"旧代码继续维护" |
| 依赖收敛 | 过渡期保留 Fyne 可构建入口，Web UI 稳定一个版本后移除 | 在此之前**不能**声称"依赖树告警归零" |

**因此"零 TLS / 零 CORS"不再是阻塞项**——改为 VPN 组网后，证书与同源问题一并消失。
但请注意 §2 中对该结论适用范围的限定：VPN 不替代认证与授权。

开发期注意：采用 embed 托管后，`wails dev` 的一键热重载不可用，改为
`Vite dev server :5173 --proxy--> 服务 :54321`，Wails 窗口在开发期指向 `:5173`、生产期指向 `:54321`。

---

### 阶段 1 · 纵向关口（Spike）—— **不逐屏迁移前必须先过**

> 这一轮的目的**不是**交付功能，而是验证架构假设。全部通过才进入逐屏迁移。

| 验证项 | 通过条件 |
|---|---|
| 服务装配下沉 | `cmd/desktop` 与 `cmd/server` 共用同一装配包，两者均可用 |
| SQLite / MySQL 双通道 | 同一份前端代码在两种后端下行为一致 |
| 内嵌服务生命周期 | 随机端口分配、启动就绪等待、端口冲突处理、异常退出清理全部可靠 |
| 启动 token | 端口仅绑 `127.0.0.1`；无 token 的同机请求被拒 |
| 登录闭环 | 登录 → 已登录外壳（导航 + 登出）→ 登出后请求返回 401 |
| 一个真实列表屏 | 选一个列表（建议零件列表），含权限拒绝路径 |
| 安装交付 | Inno Setup 装出 exe，WebView2 检测生效 |

**实机验收矩阵**：Windows 10 + Windows 11 × MySQL + SQLite。

**关口不通过时的处置**：回到本轮修架构，不进入阶段 2。24 屏写完才发现架构问题，
返工量是全部前端代码——这是设立关口的原因。

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

## 4.5 服务器层缺口清单（2026-10-02 复核，对照 `main` @ `f3448cd`）

原计划覆盖了前端与外壳，但**服务器侧（`cmd/server` / `internal/api`）此前按"可信内网"假设写**。
定案为「VPN 组网 + 同源托管」后，**TLS 与 CORS 不再是阻塞项**；但文件类能力、用例覆盖与鉴权缺口仍须补齐。
全部基于实际代码核查，非推测。

### 🔴 硬前提

| # | 缺口 | 证据 | 处置 |
|---|---|---|---|
| 1 | ~~**零 TLS**~~ | `cmd/server/main.go:53` `http.ListenAndServe`；注释自认「公网部署前必须加 TLS（C4）」| ✅ **VPN 组网下不适用**（不监听公网）。若将来要公网直连 → 前置 Caddy 反代（勿自管证书）|
| 2 | ~~**CORS / 同源**~~ | `api.New(...).Handler()` 返回裸 `http.ServeMux` | ✅ **同源托管后不需要 CORS**。仍需补 `http.Server` 超时与基础中间件 |

> **2026-10-02 更正**：本文早期版本称「46 条路由**零中间件**」，**该结论有误**。
> 实际 `main` 上 46 条路由中有 **41 条挂了 `s.auth(...)`**，做两件事：
> ① `userFromRequest` 校验 `Authorization: Bearer <token>` 与会话有效期；
> ② `usecase.Allow(role, opName)` 做角色权限点校验。
> 未挂 `s.auth` 的 5 条为：`GET /healthz`、`GET /api/v1/serverinfo`、
> `POST /api/login`、`POST /api/bootstrap-admin`（已用 `UserCount()>0 → 409` 守卫）、`GET /api/users/count`。
> **鉴权是"补齐"而非"重建"**，工作量比早期估计低。

### 🟡 功能缺口

| # | 缺口 | 证据 | 处置 |
|---|---|---|---|
| 3 | **零文件上传** | 全仓库 `multipart` / `FormFile` 零命中 | 阶段 5 前补端点 |
| 4 | **5 个用例无端点** | `RestoreDatabase` / `ClearDatabase` / `DataDir` / `SetDataDir` / MySQL 启停；权限点已定义但无路由消费 | 阶段 5 前补端点；其中恢复/清空在阶段 B/C 需按下方矩阵禁用 |
| 5 | **备份下载返回服务器路径** | `client.go:424-433` 丢弃 `saveDir`，返回服务端绝对路径 | 改为文件流；现形态属信息泄露 |
| 6 | **无登出端点** | `internal/api` 中 `logout` 零命中；`ui/app.go` 的"退出登录"只调本地 `auth.Logout()`，**`Client.tok` 不清** | 阶段 1 补（见 §4.6 会话设计）|
| 7 | **无超时 / 无限流 / 无安全响应头** | 无 `ReadTimeout`/`WriteTimeout`/`IdleTimeout`；登录失败不计数不锁定 | 阶段 1/5 补 |
| 8 | **`nativefiledialog` 无对应物** | `SHBrowseForFolderW` 仅 `ui/backup.go`、`ui/setup.go` 两处调用 | 阶段 3 定替代方案 |

### 本地 / 远程能力矩阵（2026-10-02 新增）

破坏性操作**不应**在跨机场景开放。现有四道确认门——校验过的备份文件、需手输的时间戳令牌、
说明后果的确认对话框、执行中禁用按钮——每一道都建立在「操作者就是这台机器的主人」这一前提上；
跨机后该前提不成立，四道门只剩仪式。

| 能力 | 阶段 A<br>单机桌面 | 阶段 B<br>桌面客户端 → server | 阶段 C<br>浏览器 → server |
|---|:---:|:---:|:---:|
| 仪表盘 / 统计 / 看板 / 操作记录 | ✅ | ✅ | ✅ |
| 产品 / 零件 / BOM / 批次 | ✅ | ✅ | ✅ |
| 用户管理 | ✅ | ⚠️ 建议仅 server 端 | ⚠️ 同左 |
| 备份导出（下载到本机） | ✅ | ✅ | ✅ |
| **整库恢复** | ✅ | ❌ 仅 server 所在机器 | ❌ |
| **清空业务数据** | ✅ | ❌ 仅 server 所在机器 | ❌ |
| 修改数据目录 | ✅ | ❌ server 本机操作 | ❌ |
| 初始化 / 启动 MySQL | ✅ | ❌ server 本机操作 | ❌ |
| 本地目录选择 | ✅ | N/A（无"本地"概念） | N/A |

> 本地目录选择与"修改远程服务器目录"是**不同能力**，不可合并成一条"补端点"的事项。
> 远程文件操作需另行定义：文件归属、权限、上传大小上限与格式校验、下载与恢复的安全门槛。

### 服务端与桌面端的行为不一致（迁移时需注意）

- `cmd/server` 的日志直接铺在数据目录根下（`main.go:31`），桌面端是 `<dataDir>/日志/`（`main.go:116`）
- `cmd/server` **只支持 MySQL**（`main.go:34` 硬编码 `sqlx.Connect("mysql", ...)`，连 `IsSQLite()` 都不检查）
  → **2026-10-02 更正**：早期版本由此推出「不需要动存储层」，**该推论对阶段 A 不成立**。
  阶段 A 的内嵌服务必须同时支持 SQLite（单机用户用），否则 SQLite 用户迁移后功能回退。
  所需改动是把**装配逻辑从命令入口下沉为可复用包**并接受 `repository.Store`——
  存储层抽象**已经存在**（`store.go:104,106` 两个实现均有编译期断言），改的是装配位置，不是存储层本身。
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

### 与多实例无关、阶段 1 就应补齐的项

| 项 | 现状 | 决定 |
|---|---|---|
| **登出端点** | 缺失（`delete(s.sessions, tok)` 仅存在于惰性过期清理路径） | 补 `DELETE /api/session`：删服务端会话 + 前端清凭据 + 中止该会话在途请求 |
| **过期会话清理协程** | 当前 map 无界增长（内存泄漏） | 补 |
| **会话有效期** | 硬编码 12 小时绝对过期，**无空闲超时** | 保留 12h 绝对上限；**增加 30 分钟空闲超时（滑动续期）**。仓库场景有人离开工位不锁屏，纯绝对上限会让界面还开着但所有操作 401 |
| **会话粒度** | 未定义 | **单用户单会话**：新登录使旧的失效。2–3 人规模下最简，且避免"我登出了怎么还能用"的困惑 |
| **重启后重登** | 进程重启 → 集体登出 | **接受**。持久化设备凭据留到阶段 B/C 再评估 |

> **凭据传递方式维持现状**：会话 token 走 `Authorization: Bearer`（**非 Cookie**），因此没有 CSRF 面；
> 前端存 `sessionStorage`（刷新保留、关窗丢失），**不要用 `localStorage`**（关窗仍在，等于变相长期凭据）。
> 阶段 A 新增的**启动 token 走 `HttpOnly` + `SameSite=Strict` Cookie**——同源所以浏览器自动携带，
> 前端 JS 完全不碰；HttpOnly 意味着即使页面被注入脚本也拿不到启动凭据。
> 两者是**串联的两层**：启动 token 回答"是不是我自己的 UI"，会话 token 回答"是谁、能做什么"。
> **启动 token 属于阶段 A 的本机端口防护，阶段 B/C 不使用**（server 本就跨机可达，它没有意义）。

### 为阶段 B 留口的低成本准备

阶段 B（多机桌面客户端连一台 server）的服务端改动，**不止绑定地址**——还涉及网络、
认证、文件传输与兼容验证。下面第一项是必要条件，其余按当时的实际范围评估：

- **会话存取收在窄接口后**（如 `SessionStore` 接口，内存实现先落地）——届时换数据库/Redis 实现只动一个文件
- **监听地址显式化** —— `config.Srv.Addr`（`SRV_ADDR`）目前是**死配置**（`cmd/server` 只认 `-addr` flag）。接上它
- **服务装配包可独立启动** —— 阶段 A 的装配包要能被 `cmd/server` 直接复用，否则阶段 B 会变成两份装配逻辑，
  D-016 的「就地升格就是改个启动方式」不成立
- **本地目录能力不可跨机复用** —— 见 §4.5 能力矩阵

另有一项属于**版本兼容**，建议在首次跨机部署前完成：

- **API 统一版本前缀** —— 目前 46 条路由中只有 `GET /api/v1/serverinfo` 带 `v1`，其余都是 `/api/...`。
  跨机客户端一旦出现版本滞后（旧桌面版连新服务器），没有版本前缀就无法协商与拒绝。
  **注意：版本前缀与版本协商不是同一件事**——前缀只是路由组织，是否兼容仍需 `serverinfo` 的 `API version` 字段判定（D-016 决策 3）

---

## 4.7 阶段 0/1 期间应一并完成的"阶段 B 准备"

| 优先 | 项 | 现在做的理由 |
|---|---|---|
| **T1（阶段 A 内做）** | 前端由内嵌服务托管 | 本地与远端同一份代码路径，一次投入两处受益 |
| T1 | 前端 `baseURL` **可配置**，不写死 `127.0.0.1` | 远端只需改配置，不改前端代码 |
| T1 | 会话存取收窄接口（`SessionStore`），内存实现先落地 | 将来换实现只动一个文件 |
| T1 | 登出端点 + 空闲超时 + 单用户单会话 | 见 §4.6；这是"完整登录体系"的最小可交付形态 |
| T1 | 部署文档写明：单实例 / 重启即全员登出 / 跨机需 VPN | 避免误部署与"以为是 bug" |
| **T2（首次跨机部署前）** | `cfg.Srv.Addr` 接上、允许绑定 VPN 网卡 | 到时零代码改动 |
| T2 | API 统一版本前缀 + 版本协商 | 见 §4.6，注意两者不是一回事 |
| T2 | 按 §4.5 能力矩阵门控破坏性路由 | 跨机时不开放恢复/清空 |

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
  启动内嵌服务于 127.0.0.1:随机端口 → 生成启动 token
  → 服务层按 config 选 repository.New(db) 或 repository.NewSQLite(db)
  → 复用 internal/service、internal/usecase、internal/api、internal/singleinstance、
      internal/update、internal/winappid、internal/winmsg
  ```
  > **注**：实际 API 以 Wails v2 官方文档为准。启动 token 应通过
  > `Set-Cookie`（`HttpOnly` + `SameSite=Strict`）随根页面下发，**不注入 JS 变量**——
  > 同源时浏览器自动携带，且 `HttpOnly` 使页面被注入脚本也读不到。

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
| **1（关口）** | 见 §4 阶段 1 的验证表与实机验收矩阵。**未通过不进入阶段 2** |
| 2 | 登录 + 零件增删改查/入库。**隔离对照**：从同一种子数据生成两个独立测试库，旧/新 UI 各连一个，结果逐项比对；**破坏性操作不共用同一可写库，不使用正式数据** |
| 3 | `wails build` 产出 exe，双击即用，无外部依赖（WebView2 前提已声明，安装包含检测） |
| 4 | 每迁移一屏，功能与权限行为逐项对照通过（**权限矩阵按 `auth` 权限点核对**，非按 UI 可见性核对） |
| 5 | CI 前端 job 绿；`release.bat` 能产出安装包 |
| 6 | 全屏对齐，Fyne 版可下线 |

### 验收清单必须覆盖的维度

逐屏验收时，以下维度**每一项都要过**，不能只验主流程：

- 存储：SQLite / MySQL
- 运行模式：单机 / 阶段 B 客户端
- 认证：登录 / 登出 / 会话过期 / 权限拒绝
- 数据库不可用（对齐 #48 的重试路径）
- 恢复结果三分类：已回滚 / 结果不明 / 已应用但收尾失败（对齐 #42 与 D-017）
- 单实例 / 退出 / 更新 / 安装
- 键盘交互与窄窗口
- 长日志与 90 天趋势的可读性（#27）

> 托管 CI **不等于**真实 GUI / 真实数据库验收。`Windows / Go verify` 只能覆盖编译、
> 单元测试与空白检查；上表的多数项必须实机跑。实机结果须落成文字（见 §4 阶段 1 的关口记录要求）。

---

## 7. 决策状态

### 已定（2026-10-02，见 §4 阶段 -1 完整表）

| 项 | 决定 |
|---|---|
| 交付形态 | **桌面端内嵌 WebView2**（Wails v2 单 exe）|
| 首轮范围 | **阶段 A 单机桌面**，能力不缩水 |
| 前端框架 | **React + TypeScript + shadcn/ui + Tailwind** |
| 前端托管 | **内嵌 HTTP 服务 + 前端从 `127.0.0.1:随机端口` 加载**（实现待阶段 1 关口验证后写死）|
| 服务装配 | `cmd/desktop` 与 `cmd/server` 共用装配包，接受 `repository.Store` |
| 本地 SQLite | **保留** |
| 鉴权 | **完整登录体系**（HTTP 层已有 41/46 路由鉴权，需补登出/空闲超时/单用户单会话）|
| 远端能力 | 恢复/清空/改数据目录/MySQL 启停在阶段 B/C **禁用**（见 §4.5 矩阵）|
| 远端接入 | **VPN 组网，不暴露公网**（暂不考虑公网部署）|
| 过渡策略 | **一次性切换**，但每轮迁移设**实机关口** |
| 旧版 UI 定位 | **仅作回退出口**：保留可构建入口与旧安装包，**不冻结自动更新** |

### 仍待决策

| 项 | 何时定 |
|---|---|
| 内嵌服务的**具体实现**（端口分配策略、就绪等待机制） | 阶段 1 关口实测后 |
| 会话是否落库（阶段 B 多客户端场景） | 阶段 B 启动前 |
| `nativefiledialog` 的替代方案 | 阶段 3 |
| 阶段 B 是否需要"就地升格"以外的部署路径（B 路径需导出导入工具） | 阶段 B 启动前 |
| API 版本前缀的具体形态 | 首次跨机部署前 |

> 早期版本此处写「无阻塞项」。该说法**不准确**——上表五项以及 §4 阶段 1 关口的七个验证项，
> 都是真实的未决事项。区别在于：**它们都有明确的验证时点与责任人**，而不是"没有"。

### 依赖收敛的条件性表述（2026-10-02 修订）

删除 Fyne 版后，以下四个模块**预计**随之消失——但**在 Fyne 入口真正删除之前不能断言归零**：

- `golang.org/x/net`、`golang.org/x/image`、`golang.org/x/text`、`github.com/yuin/goldmark`
  四个模块（33 个已知漏洞）均由 Fyne 引入
- 唯一例外：`filippo.io/edwards25519`（2 个漏洞）随 `go-sql-driver/mysql` 引入，需保留 → 届时顺手升级

> **过渡期保留 Fyne 可构建入口**（见 §4 阶段 -1「旧版 UI 定位」），因此这期间依赖树与告警**不会**归零。
> 是否归零须在**移除 Fyne 构建入口之后**，按最终代码保留方式、实际依赖图与漏洞扫描确认——
> 不能在保留可构建入口的同时声称告警归零。
>
> 是否现在单独做依赖升级：建议**不做**。Fyne 移除时 MVS 会直接选到新版本，
> 届时的一次覆盖升级更划算。**但这属于风险权衡，不是"必然无用功"**——若某个漏洞在此期间
> 被判定为高危且影响 Fyne 入口，应立即单独升级，不等迁移。

---

## 8. 风险与回退

- **工作量**：中偏大，每屏重写（代码量**粗估**为 Fyne 版 2–4 倍），观感与交互质变
- **新工具链**：引入 Node/npm，CI 与打包脚本需改
- **过渡期两套 UI**：必须隔离（`internal/ui` 与 `web/` 互不依赖）
- **WebView2**：Win10 21H2 / Win11 基本自带。**决定：安装包检测 + 缺失时给明确下载链接，不内置运行时**
  （内置会使安装包从约 18 MB 膨胀到 130 MB 量级）
- **权限一致性**：菜单与操作点须同时对齐 `auth.CanRead` 与 `usecase.Allow`，避免越权/漏权。
  **前端权限门不是安全边界**，服务端每次请求重算才是
- **回退**：Fyne 版代码**与可构建入口**在过渡期保留；Fyne 版安装包挂在 Release 页作为用户可自行下载的降级通道

### 回退的实际口径（2026-10-02 补充）

**旧安装包不等于安全降级。** 需要说明清楚：

| 场景 | 结果 |
|---|---|
| 升级后**无新增数据**，回退到旧版 | 大概率可行 |
| 升级后**有新数据**，回退到旧版 | **可能失败或数据错乱** |
| 数据库迁移已前滚，旧版读新 schema | 取决于有无降级迁移——**目前没有** |

因此：

1. **不冻结旧版自动更新**。旧版用户应能自动升级到新 UI 版本——这样他们才能继续拿到 bug 修复。
   **回退出口是"旧安装包"，不是"旧代码继续维护"**。冻结会让旧版用户永久停留在旧版本，
   遇到数据损坏类问题时无法自助修复。
2. **升级前强制备份**，与现有恢复四道门对齐（校验过的备份文件、手输令牌、确认框、执行中禁用按钮）
3. 回退支持范围应如实告知：**仅保证无新增数据的情况**
4. 过渡期结束时移除 Fyne 构建入口，此后 Fyne 版仅作为历史版本存档

---

## 9. 明确不选

- **Gio**：自由度略高但仍即时模式、控件生态小、无模板
- **Tauri**：需引入 Rust，等于换栈
- **Qt 绑定**：许可证与维护状况不理想

---

## 10. 审阅意见处理

> 记录 2026-09-30 `Wu-Wh0` 在 #44 提出的审阅意见（针对 head `675ed64`）的处理结果。
> 逐条对应，便于复核。

### 一、表述修正

| # | 意见 | 处理 |
|---|---|---|
| 1 | 收敛"全部定案／无阻塞项" | ✅ §7 改为「决策状态」，列出五项**真实待决**及其验证时点 |
| 2 | §0 不宜称"完整 HTTP API、迁移只是换壳" | ✅ §0 改写，并列出 5 项必须一并完成的改造 |
| 3 | 删除「VPN 攻击面为零」「无域名证书路径不成立」 | ✅ §2 改写为成本/风险判断，并注明"无域名 ≠ 无法 TLS" |
| 4 | 明确 SQLite 与运行模式是否保持 | ✅ 定为**保留**；§4.5 加更正说明（早期"不需要动存储层"对阶段 A 不成立）|
| 5 | 旧安装包不等于安全降级 | ✅ §8 新增「回退的实际口径」四场景表 |
| 6 | 依赖/漏洞消失应写成条件性收益 | ✅ §7 改为「条件性表述」，明确保留可构建入口期间不会归零 |

### 二、架构边界

| # | 意见 | 处理 |
|---|---|---|
| 1 | 明确 Wails 如何加载前端 | ✅ §2 定为「内嵌 HTTP 服务 + 随机端口」；**具体实现标为待阶段 1 关口验证**，不再宣称已定案 |
| 2 | 启动 token 与会话 token 分开 | ✅ §2/§4.6 分开说明：前者管传输层信任（仅阶段 A），后者管身份与授权 |
| 3 | `wails.Options{JS:...}` 应标为伪代码 | ✅ §5 改为要点描述，并说明启动 token 走 `HttpOnly` Cookie 而非注入 JS 变量 |
| 4 | §4.5 增加本地/远程能力矩阵 | ✅ 新增矩阵表，恢复/清空/改目录/MySQL 启停在阶段 B/C 禁用 |
| 5 | 按最新主线校准契约 | ✅ §0.1 基线更新至 `f3448cd`；§6 验收清单补入 #42 三分类与 #48 数据库不可用 |
| 6 | §4.6/§4.7 收敛"只改绑定地址" | ✅ 改为「不止绑定地址」，列出认证、文件传输、兼容验证；补 API 版本前缀 ≠ 版本协商 |

### 三、执行与验收

| # | 意见 | 处理 |
|---|---|---|
| 1 | 全面逐屏迁移前加纵向试验关口 | ✅ §4 新增「阶段 1 · 纵向关口」，含 7 项验证条件与实机验收矩阵 |
| 2 | §6 改为隔离对照 | ✅ 改为"从同一种子数据生成两个独立测试库；破坏性操作不共用可写库，不用正式数据" |
| 3 | 验收清单补齐各维度 | ✅ §6 新增 8 条必覆盖维度清单，并注明托管 CI ≠ 实机验收 |
| 4 | 「双击即用」需注明 WebView2 前提 | ✅ §1 声明前提；§8 定为安装包检测 + 引导下载，不内置运行时 |

### 四、事实与一致性

| # | 意见 | 处理 |
|---|---|---|
| 1 | §0.1/§4.5 按最新 main 核对 | ✅ 基线更新至 `f3448cd`；并**新发现两处需更正**（见下）|
| 2 | 统一框架状态；组件库写作项目选择 | ✅ §2 删除「或 Vue（待决策）」，shadcn 标为项目选型 |
| 3 | §3 的 `docs/rulesets/` 是治理配置 | ✅ 改为「GitHub 仓库治理规则集的版本化存档，GitHub 不读取此目录」|
| 4 | 估算标为粗估 | ✅ §1/§8 标注「粗估」 |

### 复核时新发现的两处事实错误

按意见 1 重新核对代码时，发现本文早期版本有两处结论与当前 `main` 不符，已在正文更正：

1. **「46 条路由零中间件」** → 实际 **41 条挂了 `s.auth(...)`**（身份 + `usecase.Allow` 权限点）。
   鉴权是"补齐"而非"重建"，工作量低于早期估计。见 §4.5。
2. **「`cmd/server` 只支持 MySQL → 不需要动存储层」** → 该推论对**阶段 A 不成立**：
   阶段 A 的内嵌服务必须同时支持 SQLite，否则 SQLite 用户迁移后功能回退。
   所需改动是**装配层下沉**（`Store` 抽象已存在，`store.go:104,106` 有编译期断言），不是重写存储层。

### 尚未处理 / 需后续确认

| 事项 | 说明 |
|---|---|
| 屏幕清单是否含工具与测试文件 | §0.1 的「UI 屏幕（24 个 .go 文件）」把非屏幕文件计入了，需在阶段 1 前重新清点 |
| 工期估算 | 各阶段天数仍为 2026-09-29 的粗估，**待阶段 1 关口实测后校准** |
