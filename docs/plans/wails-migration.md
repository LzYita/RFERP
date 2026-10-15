# 计划：从 Fyne 迁移到 Wails + Web UI（进行中 · Round 0–7 已交付）

> 状态：**进行中**。Round 0–7 已交付并合并（进度快照见 §4 开头），UI 侧已无占位屏；
> 下一步是 **Round 8 交付与冻结**。Round 7 的实机关口（1.3.0 → 2.0.0 升级演练）**已于
> 2026-10-14 实机执行**（隔离库 + 临时数据目录，正式库零写入）；演练发现 5 项，
> 已按先后顺序并入 Round 8 的「先决项」。
> 架构前置决策已于 2026-09-29 定案（见 §4.0）。
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
| 服务装配从命令入口下沉为可复用包（`cmd/desktop` 与 `cmd/server` 共用） | §4 Round 1 |
| 生命周期：随机端口、启动就绪等待、退出清理 | §4 Round 1 |
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

## 3. 目录结构（实际交付，2026-10-14 对齐）

> 下表是**实际长成的样子**，不是最初的设计稿。三处偏离是有意的，且都已定型：
>
> - 前端**没有**引入 shadcn/ui、TanStack Table 与 RHF/Zod，改用一组自写的轻量原语
>   （`components/ui.tsx`、`components/form.tsx`、各屏自带的 `<table>`），
>   因此设计稿里的 `components/ui/`、`features/` 两个目录没有出现；
>   取而代之的是 `components/` + `screens/` + `lib/` 的扁平结构。
> - HTTP 客户端与各业务域的调用集中在 `lib/`（`api.ts` + 每域一个模块），
>   没有单独开 `api/` 目录——一处 `request()` 已经承担了 baseURL、Bearer、401 广播。
> - `i18n/` 按计划留到 Round 10，现在提前建目录只会是个空壳。

```
RFERP/
├─ cmd/
│  ├─ desktop/              # Fyne 版：过渡期回退出口；Round 9 才移除其构建入口
│  ├─ desktop-wails/        # Wails 入口（main.go / wails.json / setupwizard.go / dirpicker.go …）
│  │  └─ frontend/          # 只是**转发 shim**：Wails v2 硬要求项目目录下存在 frontend/，
│  │                        #   且不支持把命令指向别处，故它 cd ../../../web 再跑真前端
│  ├─ server/               # HTTP API（本地内嵌 / 远程独立部署）
│  ├─ keygen/  signmanifest/
├─ internal/                # 27 个包；迁移期间新增：backuplayout / dbfile / help /
│                           #   noconsole / webassets / windowsobj
│  ├─ api/                  # 全部 HTTP 端点（Round 7 新增 help.go：设置屏的离线帮助）
│  ├─ webassets/            # //go:embed all:dist —— 前端产物内嵌入口
│  ├─ help/                 # guide.md / faq.md / changelog.json（go:embed）与解析函数
│  └─ ui/                   # Fyne 各屏，保留到 Round 9
├─ web/                     # 前端工程（主体）
│  ├─ index.html / vite.config.ts / tsconfig*.json / .oxlintrc.json / package.json
│  ├─ public/favicon.svg
│  ├─ scripts/              # browser-gate.mjs（浏览器关口）、embed.mjs（dist → Go 内嵌）
│  ├─ tests/                # 12 个 vitest 文件（逻辑 + 组件 + 文档渲染器）
│  └─ src/
│     ├─ main.tsx / App.tsx / index.css
│     ├─ components/        # AppShell / ui（原语）/ form（Modal、ComboInput…）/ Markdown
│     ├─ lib/               # api、nav、permissions + 各业务域（backup / batch / catalog /
│     │                     #   users / …）+ markdown、help、num
│     ├─ screens/           # 18 个屏/表单，一屏一文件；registry.tsx 是屏幕注册表
│     └─ （i18n/ 留到 Round 10）
├─ deploy/docker/           # Dockerfile / docker-compose.yml / README / .env.example
├─ docs/                    # decisions/、plans/、rulesets/（main-flow、main-safety 存档）、screenshot.png
├─ lang/ChineseSimplified.isl
├─ setup.iss / release.bat / build-installer.bat / build.bat  # 打包链（目前仍产出 Fyne 版）
└─ .github/                 # workflows/ci.yml、workflows/codeql.yml、codeql/extensions/、模板
```

> `docs/decisions/` 目前只有 **D-016、D-017** 两份成文记录；代码与文档里引用的
> D-001–D-015 尚无对应文件，历史决策散落在本文档 §4.0 的表格里。补写成文是待办，
> 但**不要**为了让编号连续而补造内容。

---

## 4. 落地清单：十轮迁移

> **编号约定**：全程使用 `Round 0` – `Round 10`，不再有"阶段 N"。
> 早期版本曾用"阶段 -1 … 阶段 6"，与后加的 Spike 关口编号撞车且交叉引用错乱，已废弃。
> 每一轮 = **一个 PR** + **一道实机关口**；关口未过不进入下一轮。

### 进度快照（2026-10-14 对齐）

| 轮 | 主题 | 状态 | PR |
|---|---|---|---|
| 0 | 架构前置决策 | ✅ 已定案（2026-10-02） | 本文档 |
| 1 | 纵向关口（Spike） | ✅ 已交付 | #52 |
| 2 | 只读屏（工作台 / 统计 / 操作记录） | ✅ 已交付 | #54 |
| 3 | 主数据 CRUD（产品 / 零件 / BOM） | ✅ 已交付 | #55 |
| 4 | 事务（批次追溯 / 库存流水） | ✅ 已交付 | #56 |
| 6 | 用户管理 + 权限矩阵关口 | ✅ 已交付 | #57 |
| 5 | 破坏性操作（备份 / 恢复 / 清空） | ✅ 已交付 | #58 |
| 7 | 设置与更新 | 🟡 已交付，待升级演练关口 | #69 |
| 8 | 交付与冻结 | 🟡 未开工（7 项中 4 项已满足） | — |
| 9 | 移除 Fyne 构建入口 | ⬜ 未开始 | — |
| 10 | 国际化（i18n） | ⬜ 未开始 | — |

> **定序**：Round 6 先于 Round 5 合并（2026-10-12 定）——两轮都要动 `POST /api/backup`
> 这类端点，权限表同时变动，叠在一起不好归因。
>
> **未勾选 ≠ 待办**：下文带"暂缓 / 放弃"字样的条目是有意做出的决定（已按决定勾选并注明），
> 不是被漏掉的活。真正未完成的是 Round 8–10。
>
> **质量关口（2026-10-14 实测）**：9 个导航模块 + 设置屏全部迁移完毕，**已无占位屏**；
> 前端 `vitest` **190/190** 通过（12 文件）、`browser-gate.mjs` **80** 条 check；
> 规模 18 屏/表单 + 4 组件 + 11 lib，最大 `BackupScreen.tsx` 826 行。

### Round 0 · 架构前置决策（2026-10-02 已定案）

**定案结论：**

| 项 | 决定 | 理由 / 状态 |
|---|---|---|
| 首轮交付范围 | **阶段 A 单机桌面**，能力不缩水；阶段 B/C 后续 | 阶段 B/C 会把"换壳"变成"换壳 + 把 server 做成产品"，工作量与风险不是一个量级 |
| 前端框架 | **React + TypeScript + shadcn/ui + Tailwind** | shadcn/ui 构建在 Radix 上，只有 React 版；源码进仓库、无依赖锁定。已最终确定 |
| 前端托管 | **内嵌 HTTP 服务 + 前端从 `127.0.0.1:随机端口` 加载** | 同源免 CORS；且这是阶段 B 成立的前提。**具体实现待 Round 1 关口验证后才写死** |
| 服务装配 | `cmd/desktop` 与 `cmd/server` **共用一个服务装配包**，接受 `repository.Store` | 否则阶段 A/B 是两份装配逻辑，D-016 的"就地升格"不成立 |
| 本地 SQLite | **保留** | `repository.Store` 已由 `*Repository` 与 `*SQLiteStore` 编译期满足（`store.go:104,106`），只需装配层下沉 |
| 鉴权 | **完整登录体系** | HTTP 层已有 41/46 路由挂 `s.auth`（身份 + `usecase.Allow`），需补齐而非重建 |
| 远端能力矩阵 | 恢复 / 清空 / 改数据目录 / MySQL 启停 **在阶段 B/C 禁用** | 四道确认门建立在"操作者就是这台机器的主人"上；跨机时该前提不成立 |
| 远端接入方式 | **VPN 组网**，不暴露公网 | 见 §2 改写后的表述 |
| 过渡策略 | **一次性切换**，但**每轮设实机关口** | 见下 |
| 旧版 UI 定位 | **仅作回退出口**：保留可构建入口与旧安装包，不冻结自动更新 | 冻结会让 Fyne 用户永久拿不到 bug 修复；回退出口是"旧安装包"而非"旧代码继续维护" |
| 依赖收敛 | 过渡期保留 Fyne 可构建入口，Web UI 稳定一个版本后移除 | 在此之前**不能**声称"依赖树告警归零" |

**因此"零 TLS / 零 CORS"不再是阻塞项**——改为 VPN 组网后，证书与同源问题一并消失。
但请注意 §2 中对该结论适用范围的限定：VPN 不替代认证与授权。

本轮的产出就是本文档：决策落纸 + 逐条回应审阅意见（见 §10）。

### Round 1 · 纵向关口（Spike）—— **不逐屏迁移前必须先过**

> 这一轮的目的**不是**交付功能，而是验证架构假设。全部通过才进入逐屏迁移。

**准备**

- [x] 安装 Node LTS + npm/pnpm
- [x] `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
- [x] `wails doctor` 确认 WebView2 可用

**前端骨架**

- [x] `web/` 用 Vite 初始化（React + TS）；接入 Tailwind + shadcn/ui
- [x] **主题按下方 §Round 1.1 基线实现**（已定稿，不再二选一）
- [x] `AppShell`：导航项与 `internal/ui/app.go` 的 `pageDef` **一一对应**
- [x] `api/client.ts` + `api/types.ts`
      （实际落在 `web/src/lib/api.ts`；命名与计划略有出入，功能等价）
- [x] i18n **骨架**：延后到 Round 10（本轮不做）
      理由：现在做要改遍每屏每个字符串，且 Round 3–8 会持续新增屏；
      当前全中文硬编码是正确状态，不是缺陷。半成品（部分屏有翻译）比没有更糟。

#### Round 1.1 · 视觉与表格基线（2026-10-10 已定稿）

以下为评审后定稿，Round 1 直接实现，不再重新选型。参考原型见 §附录 A。

| 项 | 定稿 | 取舍理由 |
|---|---|---|
| 主色 | **墨绿 `#0F766E`** | 跳出后台惯用的蓝紫；且**不与红/黄状态色撞色**（对比：赭红 `#B91C1C` 会与「已撤销」红混淆） |
| 侧栏 | **深色 `#1E293B`，宽 166px** | 表格列多时深色侧栏对内容干扰最小；宽度固定，布局可预测 |
| 密度 | **Ant `size='middle'`**（`td` 上下 5px / 左右 11px，正文 13px） | 一屏约 19 行。ERP 表格为长时间高频场景，过密易看错行、过疏需频繁滚动 |
| 圆角 | **8px**（shadcn 默认） | 与组件库默认值一致，避免后续对齐摩擦 |
| 表格阈值 | **`min-width:720px`** | 由实测反推，见下 |

**表格宽度策略（实测，非估算）**

最宽的表是批次追溯 9 列，**自然宽 680px**（墨绿/Ant middle/13px）。阈值取 720px 留 40px 余量给真实长数据。
`width:100%` + `min-width:720px` 同时满足两种情况：

- 容器有余 → 表格填满容器
- 容器不足 → 表格撑到 720px 并按比例均摊，**不会缩在自然宽里留白**

可用宽 = 视口 − 166（侧栏）− 26（内容区 padding）= 视口 − 192，因此 **视口 ≥ 912px 不出现横向滚动条**；
940–1200px 目标区间全程无滚动（实测：容器 748px + 批次追溯 680px → 无滚动）。
仍保留横向滚动能力：当某表列数确实超出阈值（如追溯页加跳件/缺料明细），中间列从**钉右的操作列下方滑过**，而不是把列压扁。

**决定「一屏能读完」的是砍列，不是阈值**（已实施）：

- **单位并入库存**：`1,284 个`，少一列
- **更新时间去年份**：`10-01 08:12`，省约 60px；年份由筛选/排序语境承担
- 批次追溯**保留负责人**（680px 放得下，仓管需认人）

> ⚠️ 阈值基于 3 行样本测得。Round 1 须用**真实数据**复核一次；若真实物料名称显著更长，
> 调整的是 `--table-min-width` 这一个 token，不是逐表改宽度。

**页结构（中后台通行骨架）**：页头 → 筛选区 → 批量操作栏（选中时）→ 表格 → 分页。
组件能力对齐 Ant Table：`tableAlertRender` 批量栏、`fixed:'right'` 操作列、`children` 树形数据（BOM）、分页 + 每页条数。

**架构验证项**

| 验证项 | 通过条件 |
|---|---|
| 服务装配下沉 | `cmd/desktop` 与 `cmd/server` 共用同一装配包，两者均可用 |
| SQLite / MySQL 双通道 | 同一份前端代码在两种后端下行为一致 |
| 内嵌服务生命周期 | 随机端口分配、启动就绪等待、端口冲突处理、异常退出清理全部可靠 |
| 启动 token | 端口仅绑 `127.0.0.1`；无 token 的同机请求被拒 |
| 登录闭环 | 登录 → 已登录外壳（导航 + 登出）→ 登出后请求返回 401 |
| 一个真实列表屏 | 选一个列表（建议零件列表），含权限拒绝路径 |
| 安装交付 | `setup.iss` 装出 exe，WebView2 检测生效 |

**Wails 外壳**

- [x] `cmd/desktop-wails/`：内嵌服务装配包于 `127.0.0.1:随机端口`
- [x] 启动 token 经 `Set-Cookie`（`HttpOnly` + `SameSite=Strict`）随根页面下发，**不注入 JS 变量**
- [x] `wails build` 产出单 exe；`setup.iss` 加 `#define` 切换产物
- [x] 单实例 / 自动更新：复用 `internal/singleinstance`、`internal/update`、
      `internal/winappid`、`internal/winmsg`
      （自动更新：查 / 下载 / 应用 三端点分开，`apply` 必须带 `confirm` 才替换 exe）
- [x] **托盘：已决定放弃（2026-10-11 定）** —— Wails v2 不支持，且 RFERP 当前不需要

  **这是产品决策，不是技术妥协**：先回答「关闭窗口后程序该不该继续跑」——
  RFERP 是单机桌面工具，关窗口就该关，所以托盘没有存在理由。

  | 事实 | 来源 |
  |---|---|
  | Wails v2 有 `menu.TrayMenu` 类型，但 `options.App` **没有 `Tray` 字段** | 查 v2.16.0 源码 |
  | `menumanager.AddTrayMenu` **零调用方** —— 类型是 v3 回移的，v2 无应用侧入口 | 同上 |
  | `Shell_NotifyIcon` 只在 `win32/consts.go` 定义，**无任何调用方** | 同上 |
  | **Fyne 版本来就没有托盘**（`fyne.io/systray` 只是 Fyne 的间接依赖） | 查本仓库 |
  | Wails **不暴露窗口过程钩子**（`windows.Options` 无 `MessageFilter`） | 查 v2.16.0 源码 |

  **若日后恢复，先满足这个触发条件**：RFERP 改成**服务端形态**（多机连同一
  数据库、服务需常驻）。届时托盘才是刚需，且应与开机自启、崩溃重启一起设计。

  **两个已知的坑，恢复时必须一并解决**：

  1. **单实例锁会一直被占** —— 窗口关了程序还活着，双击会提示「已在运行」，
     而用户找不到窗口。这正是「点关闭后双击无反应」的成因，文案必须改成
     明确指向托盘
  2. **标题栏 ✕ 改不掉** —— 它是 Windows 画的，Wails 不给钩子。要「点 ✕ 缩到
     托盘」就得接管窗口过程，风险高。**只加图标不改关闭行为是更糟的半成品**：
     两个入口做同一件事，用户只会困惑
- [x] `http.Server` 补 `ReadTimeout` / `WriteTimeout` / `IdleTimeout`（当前是裸 `ListenAndServe`）

**会话补齐**（详见 §4.6）

- [x] **补登出端点** `DELETE /api/session`（`internal/api` 目前 `logout` 零命中，
      "退出登录"只清前端 token，服务端会话条目永久留存）
- [x] **加过期会话清理协程**（当前只在"被再次使用"时惰性删除，map 无界增长）
- [x] 会话失效 24h 绝对上限 + **按 4h 滑动空闲续期**，**单用户单会话**
- [x] **改角色时强制下线**（2026-10-10 定）：管理员改某人角色后，按 user ID 扫描
      `Server.sessions`（`internal/api/server.go:253`，`map[string]*session` + `s.mu`）并
      `delete` 对应 token，其下次请求立即 401，重登时读到新角色。
      无需新造基础设施；会话仅在内存，重启即全灭，单用户单会话天然成立。

**权限与 UI 联动**（2026-10-10 定）

- [x] **只读角色不得看到操作日志**：`internal/auth/auth.go:82` 的 `ModuleAudit`
      由 `{..., RoleViewer: AccessRead}` 改为 `RoleViewer: AccessNone`。
      影响 `GET /api/audit`、`GET /api/audit/recent`（`server.go:82,113`）。
- [x] **工作台「最近操作」卡片对只读角色不渲染**：现有 Fyne 工作台在
      `internal/ui/dashboard.go:183` 拉 30 条日志；Web 走 HTTP 后只读会拿到 403，
      必须**不渲染该卡片**，而不是渲染后报错。
- [x] **单个卡片 403 不得拖垮整页**：`dashboard.go:184-186` 是「拉日志失败就整体 return」，
      Web 端不要照抄——各卡片独立失败、各自降级。
- [x] **「无」= 整个入口不显示**，不做置灰。渲染条件是 `CanRead(module)`。
      例：仓管对 `backup` 是 Read 而非 Write，可导出 CSV 但**不得**看到恢复/清空入口。
- [x] 权限表 `internal/auth/auth.go:75-85` 共 9 模块 × 4 角色，前端导航由其驱动，不另维护一份。

**实机验收矩阵**：Windows 10 + Windows 11 × MySQL + SQLite。

**关口不通过时的处置**：回到本轮修架构，不进入 Round 2。24 屏写完才发现架构问题，
返工量是全部前端代码——这是设立关口的原因。

**开发期注意**：采用内嵌托管后，`wails dev` 的一键热重载不可用，改为
`Vite dev server :5173 --proxy--> 服务 :54321`，Wails 窗口在开发期指向 `:5173`、生产期指向 `:54321`。

### Round 2 · 只读屏（导航闭合）

> **实施细案（2026-10-11 编写，PR #52 合并后）**
> Round 1 已交付并合并 `main` @ `acc6987`。以下是本轮的可执行拆解。

#### 2.0 范围与前置结论

**API 层无需新增端点。** 已核对 `server.go` 的 47 条路由，Round 2 所需的读接口
全部就位：工作台由 `GET /api/users/count` + `GET /api/audit/recent` 组合，
统计走 `GET /api/stats`，操作记录走 `GET /api/audit` 与 `GET /api/audit/recent`。
**因此本轮是纯前端工作**，不碰 `internal/api`，风险面显著小于 Round 1。

**唯一的 Go 侧改动**是目录枚举端点（见 2.4），用于替代 WebView 缺失的原生文件夹选择框。

| 屏幕 | 数据来源 | 状态 |
|---|---|---|
| 工作台 `dashboard` | `/api/users/count` + `/api/audit/recent?limit=30` | 新建 |
| 统计分析 `stats` | `/api/stats?days=` | 新建 |
| 操作记录 `audit` | `/api/audit` + 分页 | 新建 |
| 首启建管理员 | `/api/users/count` + `/api/bootstrap-admin` | 新建 |
| 零件管理 `parts` | 已在 Round 1 交付 | 复用 |

#### 2.1 先做屏幕切换骨架

现状 `App.tsx` 用 `screen.key` 手工三元判断（`activeKey === 'parts' ? ... : <Placeholder/>`）。
本轮要扩到 5 个屏幕，继续堆三元会失控。

- [x] 抽 `screens/registry.tsx`：`Record<ModuleKey, () => ReactNode>`，
      `App.tsx` 改为查表渲染，删除三元
      **注意**：条目必须返回 JSX 元素（`() => <X/>`），不能直接存组件再 `factory()`
      调用——那会让 hook 挂到 App 的 fiber 上，切屏时抛 React #310 整页白屏，
      且 TypeScript 不报错
- [x] 导航点击后 `AppShell` 的选中态与内容区同步（Round 1 已具备，确认即可）
- [x] **未实现的模块保留 `PlaceholderScreen`**，本轮不删除

#### 2.2 三张只读屏

对齐 Fyne 版语义，不加不减：

- [x] **工作台**：`dashboard.go` 的三块——经营预警、最近操作记录、统计卡。
      时间格式 `01-02 15:04`，表名/动作走 `tableLabel` / `actionLabel` 的中文映射
      （映射集中在 `lib/auditLabels.ts`，**逐字移植** `audit.go`，Dashboard 与 Audit 共用）
- [x] **统计分析**：`GET /api/stats?days=`，复刻 `stats.go` 的筛选与图表口径。
      注意 `usecase.StockStats` 及其子结构**无 json tag**，JSON 键是 PascalCase
      （`Days`/`TopIn`/`TotalIn`…）；写成 snake_case 会静默全空
- [x] **操作记录**：`GET /api/audit/recent?limit=500` + 前端分页 + 对象筛选。
      `/api/audit` 是**按记录**查的（需 `record_id`），不是全局列表，勿误用

> 三张屏全部**只读**，不含任何写操作——这是本轮与 Round 3 的分界，不要提前做编辑。

#### 2.3 权限可见性（沿用 Round 1 结论）

- [x] 导航可见性：继续用 `canRead(modules, key)`，`'none'` 整个入口不显示（**不置灰**）
- [x] 操作点复用 `usecase.Allow(role, name)` 的权限点字符串，与 API 侧一致
- [x] **前端隐藏不是安全边界**，每个请求仍由服务端重算——不要在 `permissions.ts` 里堆业务判断
      （2026-10-11 实测：`viewer1` 导航由 10 项降到 7 项，直接调 `/api/users`、
      `/api/audit/recent`、`/api/backup` 均返回 403，两层一致）

#### 2.4 首启向导：唯一需要 Go 的地方

`internal/nativefiledialog` 在 WebView 里**没有对应能力**，两个调用点必须替代：

| 调用点 | 原行为 | Round 2 方案 |
|---|---|---|
| `internal/ui/setup.go:54` | 选数据目录 | 文本输入 + `GET /api/v1/serverinfo` 校验；必要时加目录枚举端点 |
| `internal/ui/backup.go:277` | 选备份目录 | **本轮不做**（备份屏属 Round 5） |

##### 2.4.1 选数据目录：宿主注入的原生框（方案丙，2026-10-13 定）

上面第二行留到 Round 5 才做，因为它和 §2.4 的「文本输入」不是一回事：
**备份屏需要给出原生文件夹框**，否则桌面用户明明装的是桌面软件，却被要求手打
`D:\仓库数据`。

做法是**宿主注入、服务端裁决**，与 `WithHostOperations` 同构：

```
浏览器 ──POST /api/datadir/pick──▶ internal/api  ──▶ DirectoryPicker 接口
                                                        │ 由 cmd/desktop-wails 注入
                                                        ▼
                                              wails runtime.OpenDirectoryDialog
```

关键约束：**`web/` 不能直接调 Wails 绑定**。它全程不引用 `window.go` / `wailsjs`，
同一份页面要能跑在 vite dev、纯浏览器（自动化关口）与内嵌服务里——这是它刻意保持的
宿主无关性。为了一个选择框引入第一个宿主耦合点，收益与代价不成比例。所以原生能力
从宿主注入、前端只发普通 HTTP 请求，架构边界没被推动。

- [x] `internal/api`：`DirectoryPicker` 接口 + `WithDirectoryPicker` Option + `POST /api/datadir/pick`
- [x] 无 picker 的形态（`cmd/server`、浏览器）回 **501**，不是 500：界面据此降级
- [x] `cmd/desktop-wails/nativeDirPicker`：内嵌服务先于 `wails.Run` 建好而 ctx 后到，
      故用可后填的槽位 + `sync.RWMutex`（`OnStartup` 写入，picker 调用时读）
- [x] `cmd/server` **不**注入 picker；`-serve` 无头模式同理
- [x] 前端：501/500 → 亮出手填输入框；用户取消 → 静默保持原状（取消不是错误）
- [x] 关口断言按钮**显隐**门控，**刻意不点击**（点了会弹真模态框挡住跑批，
      且会改掉关口自己的数据目录）

用户决策：改数据目录**要提示**「旧目录已有的备份不会自动迁移」，但**不做成确认门**——
改目录可逆、不影响已有数据，多一道确认只会让人多按一次。

- [x] 首启流程：`GET /api/users/count` → 为 0 则进建管理员向导，否则直接登录
- [x] 建管理员：复用 `POST /api/bootstrap-admin`，密码强度校验与错误提示对齐 Fyne 版
- [x] **运行模式选择（`runmode.go`）：已决定暂缓（2026-10-11 定）** —— 服务器模式未完善时，
      选「连接服务器」只会得到一个不能用的应用。旧版同样关掉了它
      （`cmd/desktop/main.go` 的 `serverModeEnabled = false`）。**与服务器形态一起做**，
      届时需要的还包括可用的服务器程序与多机部署说明，不只是这个界面。

#### 2.5 关口（与本轮绑定，不可跳过）

- [x] **导航闭合**：9 个模块无一显示「建设中」，全部可用或明确只读
      （实测 admin 10 项导航，工作台/统计分析/操作记录/零件管理四屏均渲染真实数据）
- [x] **权限拒绝路径**：用 `viewer1` 确认被禁模块**不出现**在导航里，
      且**直接调接口返回 403**（两层都要验，前端隐藏不算数）
- [x] **首启闭环**：空库 → 建管理员 → 登录 → 三张只读屏数据正确
      （待做：`nativefiledialog` 替代方案属 2.4，尚未实现）
- [x] **升级不回归**（范围已收窄为 3 步，见 Round 1 待办专项）
      实测：新 UI 版本直接打开 Fyne 版留下的 `appliancedb`（MySQL），装配日志
      `mysql ready applied=[]` —— **无需任何迁移**；用户数 2、业务数据 210 零件
      正常读取。三步中"旧库能被新版打开"与"数据完整"已验证。
- [x] 全量 `go test ./...` + `npm run build` + `wails build` 通过

#### 2.6 本轮明确不做

| 项 | 原因 | 排期 |
|---|---|---|
| 列设置 / 自定义列 | 用户偏好存储是独立工作量 | 另排 |
| 备份/恢复屏 | 依赖目录枚举，且属破坏性操作 | Round 5 |
| 任何编辑表单 | 属主数据 CRUD | Round 3 |
| WebView2 目录选择框 | Wails 无原生 API，用文本输入替代 | 已定 |

### Round 3 · 主数据 CRUD（Round 2 完成后展开）

- [x] 产品 / 零件 / BOM 的列表与编辑
- [x] BOM 搜索选择器（级联与查询框双向同步）

**关口**：并发编辑、BOM 级联与 Fyne 版结果一致 —— **已达成（2026-10-12）**

编辑走 `PUT` 并回传 `version`（后端 `WHERE id=? AND version=?` 乐观锁），
并发编辑由版本号拒绝，与 Fyne 版同源。

两处**刻意与 Fyne 不同**，都是修 Fyne 的 bug：
  - 编辑保留 `status` —— Fyne 的「启用」复选框建完即丢（`_ = status`）且
    保存时写死 `st := 1`，编辑一条停用记录会把它悄悄启用
  - 低库存判定改为 `warn>0 && stock<warn` —— Round 2 写的 `stock<=warn`
    没有 `warn>0` 守卫，会把所有未设预警线的零件全标成「低库存」

两处**刻意不照抄 Fyne**：
  - 产品编码不加重复校验 —— 迁移 v4 主动删掉了 `products.code` 的唯一索引
    （系列号允许重复，migrate.go:260-267），加了就是凭空发明规矩
  - BOM 加行时不查库存 —— Fyne 也不查；缺料属于**建批次时**的检查
    （batch.go:335-371，那里能列出零件名并中止建批次）

副产品：建了 `components/form.tsx`（Round 3 之前除 `Btn` 外没有任何表单基元，
26px 输入框已散落三份副本）、`lib/catalog.ts`（DTO 有 `DisallowUnknownFields`，
请求体必须逐字段写死），并给 web/ 接上 vitest（13 条用例，CI 已加 `npm run test`）。

> **批次完成时的库存夹取已改为拒绝（决策 B，2026-10-13 用户定）**
>
> `UpdateBatchStatus` 转到「已完成」时按 BOM 扣库存，原实现是
> `actualDeduct = min(请求, 现有)`：库存不够也扣到 0、批次照样标记完成、接口返回
> 成功。**账面库存归零、批次齐套，账实不符而调用方毫无察觉**；唯一的痕迹是审计里
> `requested_deduct ≠ deduct`，没人会去翻审计。界面那层缺料预检只是提示，
> 直接调 API 或预检后库存被改都能绕过。
>
> 这违反了本计划一贯原则（前端隐藏不是安全边界，服务端必须重算），故按用户决策改为
> **缺料即拒绝完成**，与建批次时的缺料拦截同一语义（Fyne `batch.go:364` 也是中止
> 而非少扣）。一次列全缺料项，因为只报第一个会让人反复提交、反复失败。
> `internal/service/service_batch_test.go` 有对应回归用例（反向验证：退回夹取即失败）。

### Round 4 · 事务

- [x] 批次追溯、库存流水（`#56`）
- [x] 已撤销批次不计入出库统计（对齐 #41）：`service.go` 的 `revokedBatchSet` 把
      `STOCK_DEDUCT` 与撤销回退 `STOCK_ADJUST` 一并跳过，`service_stats_test.go` 覆盖

**关口**：**隔离对照**——从同一种子数据生成两个独立测试库，旧/新 UI 各连一个，逐项比对结果。
破坏性操作不共用同一可写库，不使用正式数据。

### Round 5 · 破坏性操作
状态: **服务器侧已完成；界面侧已完成**（2026-10-13）

**先补齐服务器侧的文件类用例**（迁到「备份导出」屏**之前**）：

- [x] **用例无 HTTP 端点**：`RestoreDatabase` / `ClearDatabase` / `DataDir` / `SetDataDir`
      —— 实为 **4 个，不是 5 个**。本节原写「5 个」，第 5 个「MySQL 启停」**根本不是用例**：
      `EnsureMySQL` 只是 `bootstrap.Options` 的函数字段，无接口方法、无权限点、无路由。
      要做它是「新增用例 + 新增权限点 + 下放 bootstrap 选项」，属另一件事，本轮不做。
      已接：`GET /api/backup/files`、`GET|POST /api/datadir`、`POST /api/restore`、`POST /api/clear`
- [ ] **零文件上传能力**：46 条路由无一个 multipart，备份导入无从上传。
      本轮用「从备份列表里选」覆盖了主要场景；**从任意路径上传备份文件仍未做**。
- [x] **备份下载**：`POST /api/backup` 原本返回**服务器绝对路径**（泄露 Windows 用户名与目录布局，
      浏览器也拿不到内容）。现改为**文件流** + `Content-Disposition`，照抄 `handleExportAudit` 的形态

**实现**

- [x] 备份屏替换占位屏，六个区块对齐 Fyne（数据目录 / 一键备份 / 整库恢复 / 清空 / 审计导出 / 全量导出）
- [x] **四道门**：G1 从备份列表选、无法导出时间戳的文件**直接拒**；G2 手输令牌；
      G3 列明后果的确认框；G4 执行中禁用
- [x] 令牌**随所选文件变化**、不可预填、不可粘贴（与 Fyne 版的**有意差异**：那边选中即预填
      `ui/backup.go:439`，预填+可粘贴会把 G2 降级成勾选框）
- [x] **恢复结果三分类**：三分类的判定在 service 侧**本来就有**（普通 error=已回滚、
      `OperationError{Unknown/Applied}`），缺的是 API 层把它可程序化区分——
      现响应带 `state` 字段，界面据此分色与决定是否渲染重试按钮
- [x] 「结果不确定」三点：① **黄**色；② 主文案直接写「**不要重试**」；
      ③ **不渲染重试按钮**（`rolled_back` 才渲染）
- [x] 按 §4.5 能力矩阵门控：新增 `api.WithHostOperations()`，**默认关闭**（漏传=不放行），
      与 `WithUpdate`/`WithRemember` 同构。`cmd/server`（跨机）不传，
      `cmd/desktop-wails`（窗口 + `-serve`，都只绑回环）传
- [x] **路径穿越防线**：恢复只接受**文件名**，服务端在备份目录内解析。
      两道独立检查（`filepath.Base` + `filepath.Rel` 包含性），去掉任一道另一道仍挡住
- [x] **「更改数据目录」按钮**：Fyne 版有（`ui/backup.go:82`，`CanWrite` 时显示），
      Web 版一度只显示路径、漏了入口。原生文件夹框经**宿主注入**取得，
      前端保持宿主无关；无宿主窗口时降级为手填输入框。详见 §2.4.1
- [x] **备份文件名兼容真实数据**（2026-10-13，实机反馈）：`D:\仓库数据` 里同一时刻
      `20260924_170830` 有三个世代 —— `.sql`(217.4KB) / `.upgraded.sql`(222.8KB) /
      `.final.sql`(223.7KB)。本程序只写 `%s_%s.sql`（`dbbackup/backup.go:48`），
      后两个是迁移期间手工改名 / 外部工具留下的。早先规则只认结尾 `.sql`，
      于是**旧备份一个都恢复不了**，界面只回「请选择本应用生成的文件」——那是在教用户
      做软件该做的事。
      规则改为允许**一个**点分段（`(?:\.[A-Za-z0-9_-]+)?`），结尾仍须 `.sql`/`.db`，
      故 `xxx.sql.bak` 照拒。三个世代导出**同一个**口令：口令确认的是"读过后果"，
      不是"选了哪个版本"。
- [x] **这条规则此前只活在前端**：服务端 `resolveBackupFile` 只防目录穿越、不看文件名格式，
      于是界面拒绝的文件直接调 API 又能恢复——界面那道门是摆设。现已把规则提到服务端
      （`restoreFilePattern` + `BackupTimestamp`）作唯一来源，前端镜像之，
      并加 `backup_contract_test.go`：直接读 TS 源码里的正则、交给 **Node 求值**，
      与 Go 侧对同一批真实文件名逐一比对（不在 Go 里复刻一遍规则——复刻等于又写一份）

### 备份可恢复性预检（2026-10-13，实机反馈驱动）

查 `.final` / `.upgraded` 的来源时发现的、比命名严重得多的问题。

**恢复的实现是「先 DELETE 全部业务表，再回灌 INSERT」**（`service/restore.go:107-113`），
所以一份备份能否安全回滚，取决于它**schema 里有没有当前的每张业务表**：

- 表存在但当时为空 → 无 INSERT，但 DELETE 后回填空 = 正确回滚，**不算缺**
- 表**不存在** → 照样被 DELETE，且无处回填 → 若现在有行，这些行永久丢失

实测 `D:\仓库数据\备份` 9 份备份：**7 份缺 `batch_consumptions`**，
只有 `.upgraded.sql` 与 `.final.sql` 覆盖完整 —— 它们是用户管理迁移**之后**的版本。
旧版 Fyne 的确认文案（`ui/backup.go:254`）只笼统写「账号、迁移记录与数据库身份会保留」，
对表级覆盖只字不提。

另一颗地雷：`pre_restore_20260924_172919.sql` 是 **UTF-16LE**（PowerShell 另存为的产物）。
按 UTF-8 读它 **0 行 INSERT 匹配**。好在 `VerifyDumpFile` 在 `deleteBusinessTables`
**之前**按字节找不到 `Dump completed` 会拦下，失败在删除之前——但用户已经输完口令、
点完确认了，而且文案说「文件不完整」，而文件其实没缺任何东西。

- [x] `internal/dbbackup.InspectDump`：编码探测 + 覆盖度，读取**有上限**（1MB）
- [x] 读不完整个文件时返回 `SchemaKnown=false` 而不是猜测——
      把「没读到」谎报成「备份里没有」，比不显示更糟
- [x] `usecase.BackupInspector` **可选**端口（`service.InspectBackupFile`）：
      没实现时列表照常工作，预检是增强而非前提
- [x] `GET /api/backup/files` 随列表返回 `schemaKnown` / `missingTables` /
      `restorable` / `unusableReason`
- [x] 恢复端点**在删除任何数据之前**拒掉编码不兼容的文件
- [x] 下拉框每项带标记：`✓ 可完整回滚` / `⚠ 缺 N 张表` / `（覆盖度未确认）` / `⚠ 不可用`
- [x] G3 确认框逐条点名回填不了的表；**警告但允许**（用户明确决策：
      回滚到某个历史时刻是正当需求，替用户决定不合适，但要让他在按下确认前知道）
- [x] **语法可重放性预检**（2026-10-13 实机踩坑后补）：上面那份 UTF-16 副本转成
      UTF-8 之后**仍然恢复不了** —— MySQL 回 `Error 1064`。原因是它当初由
      `mysqldump | Out-File` 在 PowerShell(GBK 控制台) 里生成：UTF-8 字节被按 GBK
      解码，中文变乱码，还**吃掉了字符串的结束引号**（`'常熟仓库'` → `瀹佹尝浜旈噾?`），
      于是引号数变奇数、SQL 语法不合法。
      **我上一轮只验了编码就说"转换后可用了"，那是错的** —— 转换器只能换编码，
      修不了已损坏的内容。
      现按**整条语句**（而非逐行）数引号，且刻意镜像 `replayDumpInserts` 的分句逻辑：
      同样的跳过规则、同样的 `;` 收尾、同样的跨行缓冲。转义引号（`\'` 与 `''`）
      不计入奇偶性，窗口边界处未收尾的语句不判定（那只是没读到，不是损坏）。
- [x] **恢复口令简化**：`restore 20260924_172919` → `restore 172919`（23 → 14 字符）。
      依据是这道门的作用是**让人停下来读一遍后果**，不是校验文件身份；输入摩擦越大，
      越容易被无意识地划过去。有意接受的局限：不同日期的同一秒会撞口令
      （而"同一秒"本来就不是唯一标识——`.sql`/`.final.sql`/`.upgraded.sql` 就共用一个）

**顺带查清的旧版逻辑**（都不是程序产生的）：三个怪文件都出自 2026-09-24 那次
人工迁移。证据三条 —— ① 全仓库含全部历史搜不到 `.final`/`.upgraded`；
② 写任意前缀的能力 `BackupTo` 是 **09-29** 才引入的，而那些文件是 **09-24**，
那时程序连 `pre_restore_*` 这个名字都产生不了；③ 程序写 dump 是
`cmd.Stdout = outFile` 原始字节 + 写完自校验失败即删，Go 代码历史上从未用过 `Out-File`。

**操作前自动副本功能仍在**：清空 → `pre_clear_*`（`service.go:1692`），
恢复 → `pre_restore_*`（`restore.go:65/98`），迁移 → `pre_migrate_*`。
保留份数统一在 `paths.SafetyCopyKeep = 5`，**按前缀各自计数**。

### 备份分类目录 + 自动定期备份（2026-10-13）

用户需求：迁移前备份也滚动保留 5 份；新增「每 n 天自动备份」（保留 20 份、
与手动分开）；不同类型的备份放进不同子目录；列表按类别级联展示。

```
备份\
  manual\   backup_<ts>.sql        手动一键备份 —— **永不自动删除**
  auto\     auto_<ts>.sql          自动定期备份 —— 滚动保留 20
  safety\   pre_restore_<ts>.sql   操作前/升级前副本 —— 各类各 5
            pre_clear_<ts>.sql
            pre_migrate_<ts>.sql
```

**为什么必须分类**：早期所有备份平铺在根下且同为 `backup_` 前缀，于是
「手动永不删」与「副本要滚动清理」两条**相反**的策略没法各自成立——前者靠
前缀筛选保不住（升级前备份也是 `backup_`），后者一旦写错前缀就会删掉用户
主动留的备份。分类之后每条策略各自落在自己的目录里。

---

#### 更新影响与旧版本兼容（这一节是本改动的重点）

旧版本把所有备份平铺在 `备份\` 根下。用户从任意旧版本升级上来后，
**那些文件必须照样能选、能恢复**。为此有两条硬性设计约束：

**约束一：`List()` 与 `Resolve()` 都会照扫 `备份\` 根目录。**
根目录里剩下的文件一律作为「旧布局」类别返回。于是下面每一种情况
**都不会让备份在界面上消失**：

| 情况 | 结果 |
|---|---|
| 迁移没跑（用户跳过 / 代码没执行到） | 文件仍在「旧布局」组里，可选可恢复 |
| 迁移跑到一半被强杀 | 已移走的在分类目录、没移走的在根目录，**两边都可见** |
| 目录只读 / 权限不足 | 该文件留在原地，其余照常迁移 |
| 分类目录创建失败 | 该类全部留在原地 |
| 文件名无法归类（如 `mybackup.sql`） | **不挪**（替用户决定含义是危险的），仍可见 |

**迁移是尽力而为的整理，不是正确性的前提。** `MigrateLegacy` 失败只记日志、
不影响启动 —— 把一个整理步骤失败升级成「软件起不来」，是拿可用性换整洁。

**约束二：迁移只移动能确定归类的文件，且写降级恢复日志。**
- 只移动前缀可判定的（`backup_` / `auto_` / `pre_restore_` / `pre_clear_` / `pre_migrate_`），
  不认识的一律留在原地
- **同名冲突不覆盖任何一方**：分类目录里那份是程序写的，根目录里那份更可能是
  用户手工放的旧文件，覆盖任何一方都可能是错的 —— 留给用户自己看
- 每次移动写进 `备份\layout-migration.log`，记 `时间 原文件名 -> 目标目录/`

**降级回旧版本的唯一风险**：旧版本只读 `备份\` 根目录，升级后看到的是空的。
为此日志里写明「照本文件把文件搬回根目录即可」，且日志本身就在根目录下、
降级后仍然可见。

**配置文件**：`autoBackupDays` 用 `*int` + `omitempty`。老版本 `config.json`
里根本没有这个键 → 缺省 0 = 关闭，**不会因为升级就莫名其妙开始自动备份**；
关闭时字段不落盘，不搅动用户的文件。

**前缀常量收归 `paths` 包**：`service.safetyCopyKeep` 曾经是 service 私有的
常量，但它现在要同时管三类副本、且必须与写文件的目录逻辑放在一起。两处各写
一份 `5` 就是等着一场漂移（改了写目录的那个，清理还按旧的来）。已删除本地常量。

#### 自动定期备份的语义

- 间隔：`0（关闭）/ 1 / 2 / 3 / 5 / 7 / 30`，**固定枚举**。
  非法值由服务端拒绝并回 400，**前端与后端都不静默改成邻近的合法档** ——
  用户以为每 4 天有一份、实际每 5 天，而真正需要备份的那一刻正好落在空档里
- 判断依据是 **auto/ 目录里最新一份的时间**，不是「上次检查的时间」：
  软件关三天再打开会立刻补一份（而不是把间隔一直顺延）
- **一份都没有时立刻跑一次**：用户刚开启就该看到结果，否则他无法确认
  它到底工作不工作
- **没有后台服务**，所以「每 n 天」的真实含义是「每 n 天里第一次打开软件时」。
  这一点在界面上写明了，否则用户会以为关着软件的那天也会备份
- 滚动清理只在**确实产生了新备份**时执行，且**只删 `auto/` 里的**。
  `manual/` 与 `safety/` 一个都不许动（有测试专门守这条）

- [x] `paths`：分类目录、前缀映射、`AutoBackupKeep=20` / `SafetyCopyKeep=5`
- [x] `backuplayout`：`List`（含旧根）/ `Resolve`（跨目录，仍防穿越）/ `MigrateLegacy`
- [x] 写入端分类；`pre_migrate` 前缀 + 保留 5
- [x] `GET|POST /api/backup/autoschedule`（读=只读档，写=本机档）
- [x] 前端 `<optgroup>` 分组 + 自动备份设置区
- [x] 测试：布局 14 + 自动备份 7 + 分组 6

### 导出端点的三个回归（2026-10-13 实机反馈）

都是「两端各自以为对方会怎么做」这一类，Go 与前端的单测**各自都过**，
因为没有任何一处从界面真发过一次请求。

**① 审计导出 `invalid body`**
界面发 `{start, end}`，值是纯日期 `"2026-10-13"`；服务端把字段声明成 `time.Time`，
而 Go 的 `time.Time` 只认 RFC 3339 → 400。
现由 `parseExportDate` 同时接受 `2006-01-02` 与 RFC 3339（后者是 Go 侧客户端
远程模式会发来的形状，收掉它会打断 client.go）。结束日顺延一天，
否则选「10-13 到 10-13」的人拿到空文件却不知道为什么。

**② 全量导出的 `all_data.csv` 是乱码**
`handleExportAll` 回 `{files, dir}` JSON，前端却按「下载单个文件」处理，
把这段 JSON 存成了 `all_data.csv`。打开看到的是一段含服务器路径的 JSON ——
既拿不到数据，又把目录布局泄了出去。
现改为：前端拿到**文件夹路径 + 文件名列表**后弹窗告知，并提供「打开文件夹」。
全量导出本来就是**多份** CSV 落在数据目录里，打包成一个下载反而多一道"解压再找"。

**③ 导出目录丢了旧版的分组**
旧版是 `导出\all_data\<时间戳>\` 与 `导出\audit_log\<时间戳>\`（`ui/backup.go:313/317`），
Web 版直接扔在 `导出\` 根下，于是每次多出一个 `20261013_160107-1666890059`
这种随机名目录 —— 用户在自己的数据目录里看到它，既不知道是什么也不敢删。
现已恢复分组，子目录名也恢复成可读时间戳。

> 注意：子目录**不能**直接 `MkdirAll` 到已存在的时间戳目录 ——
> `failExport` 会 `RemoveAll` 它，而那可能是上一次导出留下的，会把人家的数据删掉。
> 现在只在目录不存在时创建，同一秒内重复导出追加 `-2`、`-3`。

**④ 打开文件夹是本机操作**

`explorer.exe` 启动的是**这台机器上**的程序，远程客户端点了只会在自己电脑上
打开一个不存在的路径 → 走 `hostOperations` 闸门。
且只允许打开导出目录本身或其子目录：客户端给任意路径等于让它在这台机器上
打开任意文件夹。

**⑤ 审计导出也不再落进「下载」（用户明确要求，与全量导出对齐）**
原先 `handleExportAudit` 直接把 CSV 作为文件流返回，界面 `saveBlob` 一下 ——
文件既进了浏览器的「下载」，又在数据目录里留了一份，**同一份数据两个副本**，
而「下载」里那个认不出是什么。
现在导出只回报 `{dir, files, count}`，界面弹窗告知位置并提供「打开文件夹」，
与全量导出共用**同一个弹窗组件**（两件事形态一样，分成两个弹窗早晚会漂成两套措辞）。

> **远程形态例外（2026-10-13 用户决策）**：代码路径全部就绪
> （`POST /api/export/audit/download` 端点、`client.go` 指向、远程弹窗里的按钮与
> handler 都写好了），但**远程联机本身尚未完成**（§Round 2「运行模式选择」暂缓），
> 所以按钮暂时**隐藏**：`BackupScreen.tsx` 的 `REMOTE_EXPORT_DOWNLOAD_AVAILABLE = false`。
> 理由是那之前远程用户点导出只会拿到一个**打不开的服务器路径**，给一个同样打不开的
> 下载按钮只会被当成坏了。远程形态的提示文案也据实写成「文件保存在服务器上，
> 本机打不开这个文件夹」。
> 关口对该开关有断言（远程形态下按钮必须不存在），避免它被顺手删掉或误打开。
> 远程形态真正可用时把开关翻成 true 即可，代码不用动。

> 文件名用**用户填的**结束日期，不是内部顺延一天后的边界：
> `audit_log_20261001_20261013.csv` 用户才知道自己导的是哪一段；
> 写成 `…_20260930` 会让他以为自己选错了日期。（这条是被测试抓出来的：
> 我第一版用 `end.AddDate(0,0,-1)` 去"还原"，对多天区间直接算错。）

- [x] 关口新增：按**界面**发审计导出（不再直接调 API —— 应用限制同一账号只保留
      一个会话，关口自己再 `login()` 会把页面会话顶掉，整屏变成「登录已失效」）
- [x] 关口新增：全量导出后断言对话框里的 `all_data` 路径、文件列表、打开按钮，
      且**限定在 `[role="dialog"]` 内**断言（屏上本来就有「全部数据导出」区块标题，
      不限定范围会子串匹配上，等于没验）
- [x] 关口新增：远程形态下「下载 CSV」**必须**不存在（`REMOTE_EXPORT_DOWNLOAD_AVAILABLE = false`），
      避免那个开关被顺手删掉或误打开

### 侧栏与主区的断层（2026-10-13 实机反馈）

截图里侧栏的深色背景只到视口中部就断了，下面露出一条白缝，而主区还在往下滚。

**根因**：`<main>` 是 `flex-1 min-h-0` 但**没有 `overflow`**，于是它不是滚动容器。
各屏多数自带 `min-h-0 flex-1 overflow-auto` 的包裹（表格页要粘性表头），
**备份屏是唯一登录后没写这个包裹的屏** —— 内容撑破外壳，侧栏到视口底就断了。

**修法**：把滚动收到 `<main>` 自己身上（加 `overflow-y-auto`），而不是给备份屏补包裹。
理由是包裹式修法要求**每一屏**都记得写，漏一屏就复现一次；收在 main 上，
任何屏漏写最多退化成「多一层能滚的容器」，不会把布局顶穿。

**关口断言用实测而不是类名**：

- 文档本身不滚（`documentElement.scrollHeight - innerHeight <= 1`）
- 滚动确实发生在 `<main>` 里（`main.scrollHeight > main.clientHeight`）
- 侧栏 `getBoundingClientRect()` 贯穿 `0 ~ innerHeight`

> 负向对照：撤掉 `overflow-y-auto` 后，关口报「文档多出 143px 可滚——外壳被内容顶穿」。
> 注意**侧栏那条断言单独看是通过的** —— 未滚动时侧栏仍占满视口，断层要滚下去才出现。
> 真正抓到问题的是文档溢出那条，别只留侧栏的断言。

**CodeQL 现状（2026-10-13 复核）**：仓库实际未解决告警 **5 条**（3 × `go/path-injection`
+ 2 × `go/sql-injection`），都指向既有代码（`setupwizard.go` 等），**不落在本轮新增代码里**。
PR 页面那个红叉写「38 new alerts」是「本 PR 改动行范围内」的粗口径统计，与仓库实际
未解决集合对不上——以 code-scanning API 为准。决策不变：这些告警根因是用户可配置的
`paths.DataDir()`，属建模缺口，但**不压制**，另开任务用 `paths.ResolveUnder(root,name)`
单一出口根治（见 §8 缺口 8 的收口方案）。
- [x] 副本路径只回**文件名**（`preRestore`/`preClear`），不回绝对路径

**关口**：三分类各有实机用例，含"结果不确定"路径。

> **三分类的真正缺口不在"做"，在"传得出去"**（2026-10-13 记）
>
> service 层早就能区分三种结局，但 `internal/api` 此前只用 `writeErr` 把它拍扁成
> 一句中文 + 500，前端拿到的只有 `error` 字符串。而 §Round 5 要求的三条呈现规则
> （黄色 / 写明不要重试 / 不渲染重试按钮）**全都依赖这个状态能被程序化读到**。
> 所以先补 API 层的 `state` 映射，界面才有分支的依据。
>
> 缺省取 `rolled_back` 而不是 `unknown`：service 侧普通 error 的语义就是"已确认回滚"
> （`bulkLoadOutcome` 只在 COMMIT/ROLLBACK 本身失败时才包装成 `OperationError`）。
> 把缺省判成 unknown 会让绝大多数**可重试**的失败都被标成"别重试"。

> **待办：CodeQL path-injection 告警要治本，不是压制**（2026-10-13 记，用户定）
>
> Round 5 的 PR 触发了 30 条 `go/path-injection` 告警（GitHub 报"38 new"，
> 含重复计数）。**其中只有 3 条在 Round 5 新增的 `internal/api/backup.go`**，
> 其余 27 条在 `logging`、`dbbackup`、`repository/sqlite_backup`、`restore`、
> `dbfile`、`client`、`sqlite` —— 本轮从未碰过的文件。它们被判"新"只是因为
> 这一版让 CodeQL 做了更完整的分析，把 main 上一直存在却没被扫到的同类点翻了出来。
>
> 告警的 taint 源头都是 `paths.DataDir()`，而它的值来自**用户自己在首启向导里
> 选的目录**（或 `config.json` / `MES_DATA_DIR`）。本应用的威胁模型明确写着
> 「操作者就是这台机器的主人」，所以"数据目录可由用户指定"是**设计本身**，
> CodeQL 的 taint 规则没有建模这个前提。
>
> **但不能因此压制**（用户 2026-10-13 定：治本）。压制的代价是：一条真实存在的
> 提示被永久静音，而下一任维护者不会知道它被静音过。治本要做的是让"这些路径
> 是配置根目录下的子路径"成为**代码里可验证的事实**，而不是靠注释说服人：
>
> - 在 `paths` 层提供唯一出口（如 `paths.ResolveUnder(root, name)`），内部做
>   `Abs` + `Rel` 包含性校验，**所有**子路径只能经它构造；
> - 逐步收敛 `logging` / `dbbackup` / `sqlite_backup` / `restore` 等处散落的
>   `filepath.Join(dir, name)`；
> - 收敛完成后若告警仍在，说明真有遗漏，再单独处理那几条。
>
> 这件事横跨多个包、触及备份与日志的落盘路径，**不该夹在 Round 5 的 PR 里**。
> 单开一轮做。

### Round 6 · 用户管理与权限

> **先做这一轮，再做 Round 5**（2026-10-12 用户定）。Round 6 短且独立，能在动备份之前
> 把「谁能改什么」这条轴理顺；Round 5 要新增 `POST /api/backup` 等端点，权限表会同时变动，
> 两轮叠在一起不好归因。

- [x] 用户 CRUD；角色变更需**重新登录后生效**（沿用现有语义）：改角色会
      `revokeRememberForUser` 作废该用户的全部凭证（`remember.go`），`UsersScreen` 明写这一点（`#57`）
- [x] 前端权限门**不是安全边界**，服务端每次请求重算才是：`server.go` 的 `s.auth` 逐请求
      校验身份 + `usecase.Allow`（`#57`）

**关口**：4 个角色 × 全部屏幕的权限矩阵逐项核对（按 `auth` 权限点核，不按 UI 可见性核）。

> **权限矩阵核对结果（2026-10-13 完成）**
>
> 矩阵本身（`internal/auth/moduleAccess`）逐项核对无误。核对过程中**查出三处
> 「声明与实际不符」**，均已处理：
>
> | 问题 | 性质 | 处置 |
> |---|---|---|
> | `AllModules()` 漏了 `ModuleSystem` | **真 bug**：`/api/me/permissions` 少返回一个模块，前端永远看不到它 | 已修：模块清单改为从权限表派生（见下） |
> | e2e 断言 `len(admin.modules) == 9` | **把 bug 锁住了**：这个数字反成了"漏掉也是对的"的证明 | 已改为断言"权限表里的每个模块都在" |
> | `System.CheckUpdate`/`Identity.UserCount`/`Identity.ChangePassword` 三个权限点未接线 | 有意为之（登录页更新提示、首启判断、自助改密），但权限表没写明，容易被"顺手修回去" | 当时已在这三行加注说明；**2026-10-15 更新改为登录即可后，三个 `System.*` 权限点已整条删除**（连同 `ModuleSystem`），另两个保留 |
>
> 根因是**同一信息有两处声明**：`moduleAccess`（map）与 `AllModules()`（另写的
> 字面量列表）。注释当时还写着"从一处派生才不会漂移"，而实现正是漂移的来源。
> 现在模块清单就是权限表的键，加模块不可能只加到一半。
>
> 最终矩阵（🟢写 / 🔵读 / ⚪无）：
>
> | 模块 | admin | warehouse 仓管 | production 生产 | viewer 只读 |
> |---|:---:|:---:|:---:|:---:|
> | dashboard 工作台 | 🔵 | 🔵 | 🔵 | 🔵 |
> | stats 统计分析 | 🔵 | 🔵 | 🔵 | 🔵 |
> | products 产品管理 | 🟢 | 🔵 | 🔵 | 🔵 |
> | parts 零件管理 | 🟢 | 🟢 | 🔵 | 🔵 |
> | bom BOM管理 | 🟢 | 🔵 | 🔵 | 🔵 |
> | batch 批次追溯 | 🟢 | 🔵 | 🟢 | 🔵 |
> | audit 操作记录 | 🔵 | 🔵 | 🔵 | ⚪ |
> | backup 备份导出 | 🟢 | 🔵 | ⚪ | ⚪ |
> | users 用户管理 | 🟢 | ⚪ | ⚪ | ⚪ |
>
> **2026-10-15 更新**：`system 应用维护` 这一行连同 `ModuleSystem` 模块与
> `System.*` 三个权限点已删除——产品决定软件更新对齐 Fyne 旧版，任何登录角色都能更新，
> 不再需要"仅管理员"的模块。上表其余各行不变；`internal/auth/matrix_test.go` 的
> `TestNoSystemModule` 守着它不再回来，`nav_contract_test.go` 的非导航模块白名单随之变空。
>
> 前端侧核对：9 个导航项全部用正确的模块键做 `canRead` 门控；各屏写操作全部用
> `canWrite(modules, '<本模块>')`；**前端无任何硬编码角色判断**；`backup` 屏仍是
> 占位屏（Round 5 交付）。
>
> **新增三道契约测试**（都是"两份声明必须一致"型，靠反向验证确认能抓到对应事故）：
>
> | 测试 | 挡住什么 |
> |---|---|
> | `internal/auth/matrix_test.go` | 模块漏列 / 某模块漏写某个角色的档位 / 已决策档位被无声改动。反向验证：去掉一个模块 → 失败 |
> | `internal/auth/nav_contract_test.go` | **后端加了模块、前端 `nav.ts` 没加导航**——TS 编译不会报错，表现为"功能加了但没有入口"。反向验证：加一个假模块 `reports` → 失败 |
> | `internal/api/routes_permissions_test.go` | **路由漏挂权限点**（`s.auth(h, "")` 等于放行所有登录用户，就是当初 update/download 那个事故）；权限点名拼错（表现为"所有人 403"）；定义了权限点却没接线。反向验证：把 `/api/parts` 改成空权限点 → 失败 |
>
> `routes_permissions_test.go` 里有两份白名单（不需要鉴权的路由、没挂权限点的路由），
> 每条都必须写明理由；新增一条会立刻失败，逼作者回答"这条为什么不需要权限点"。

> **不要在任何文档或报告里写演示账号**（2026-10-12 记）
>
> 我曾在对话报告里写「演示账号：admin/admin12345、viewer1/viewer12345」，那是照抄
> `cmd/desktop-wails/e2e_test.go` 的测试夹具——**这两个账号只存在于该测试自建的临时库里**，
> 在任何真实环境都不存在（实测正式库返回 `invalid credentials`）。
>
> 账号口令属于环境相关的事实，不写进文档与仓库，需要时现场建。本仓库里的 `admin12345`
> 仅出现在 `e2e_test.go` 且由该测试自己创建，保留。

> **零件屏「分类」列与「分类」筛选是迁移漏项，已补回**（2026-10-12 记）
>
> Fyne 原版零件屏有「分类」列（`internal/ui/part.go:80` 的 headers）、分类筛选下拉
> （`filterSel`）、以及按数据重建候选的 `syncTypeFilter`（`:210`）与 `applyFilter`（`:244`）。
> Wails 版**整条丢失**，而 `idx_parts_type` 索引还在——说明这个维度本来就是用的。
> §4 Round 1.1 明确列了「砍列」清单（单位并入库存、更新时间去年份），**分类不在其中**，
> 所以这不是设计取舍，是漏项。已在 `PartsScreen.tsx` 补回列与筛选。
>
> 两处**有意偏离** Fyne，均已在代码注释里写明：
> - 分类候选排序改拼音序。Fyne 用 `sort.Strings`（中文按码点，下拉里看着像乱序），
>   那是 Go 默认排序的副产物，没有可保真的语义。
> - 关键字搜索范围取编码/名称/规格/分类的并集。Fyne 占位写「编码/名称/规格」而实际
>   只匹配「编码 名称 分类」（`ui/part.go:252`），规格根本没进全文；照抄任一边都会
>   留下「说了能搜却搜不到」的坑，故取并集并把占位文案写实。
>
> **仍缺**：Fyne 有的「供应商」列同样没迁移，尚未补——补它要再评估列宽预算，单独决策。

> **交互类缺陷必须有浏览器关口**（2026-10-12 记）
>
> 分类下拉原来的 `<datalist>` 在 WebView2 里弹不出来，而 DOM 检查完全正常：
> `input.list` 能解析、六个 option 都在。**任何静态检查与 jsdom 测试都会放它过关。**
>
> 因此新增两道：
> - `npm run test:browser`（`web/scripts/browser-gate.mjs`）：自起后端 + vite + Chromium，
>   真点击真断言，覆盖 datalist 这类只有真浏览器才暴露的行为。CI 里没有 WebView2，
>   所以这道关口只在本地跑，**改动涉及表单/弹窗/下拉时应先跑它**。
> - `web/tests/comboInput.test.tsx`（jsdom）：把交互不变量钉进 CI。
>
> 该轮浏览器关口实际抓出三个真缺陷，都是纯函数测试看不见的：
> ① 展开只绑 `onFocus`，选完后「再点一次」无反应；
> ② Esc 同时被下拉与 Modal 响应，按一下把填了一半的表单整个丢掉；
> ③ `FormRow` 是包裹式 `<label>`，会把面板内的点击转发给控件——导致选完面板立刻弹回、
>    小三角永远关不上。③ 的根因是结构问题，补 ①②③ 时用的是事件拦截而非改结构，
>    **若将来还有控件在 `FormRow` 内出现类似「点了没反应/自己弹回来」，先怀疑这条**。

### Round 7 · 设置与更新

状态: **已交付（2026-10-14）**。关口（1.3.0 → 2.0.0 升级演练）**已实机执行**，结论见本
节末尾；演练发现 5 项，已按序并入 Round 8「先决项」。

- [x] 设置入口（侧栏左下角）→ 右侧主内容区显示，**不弹窗**
      （入口 Round 5 时已接在 `AppShell.tsx`，本轮补上屏幕本体；`registry.tsx` 里
      「设置」不是模块，故不进 `NAV`／不参与权限矩阵）
- [x] 三个页签：**使用指南 / 常见问题 / 更新说明**
- [x] 复用 `internal/help`（`guide.md`、`faq.md`、`changelog.json` 走 `go:embed`）；
      经 `GET /api/help` 一次取全，更新说明由 `help.ParseNotes` 在**服务端**切好小节
      （前端不再实现第二份切分逻辑）
- [x] 渲染约束：**禁止代码块、引用块、Markdown 表格、行内代码含中文**
      （`internal/help/permissions_test.go` 的静态检查继续守住这些约束；
      Web 侧用自写的最小分块器 `web/src/lib/markdown.ts` 而不是引 markdown 库，
      越界写法按字面量渲染）
- [x] 版本显示、内置最新版本、查询最新更新；自动更新走同一条通道
      （版本取服务端自报值，不是前端常量——否则发出去的 exe 升级后界面还在报旧号）

> ⚠️ 接更新端点时的两条约定（2026-10-13 定，见下方 T2）：
> ① `POST /api/update/apply` **忽略请求体里的 `path`**，只应用服务端在下载步骤
> 记住并校验过的那个包。设置屏**不要**传 path、也不要指望它生效；下载成功后直接
> 带 `{confirm:true}` 调 apply 即可。
> ② apply 会**再核对一次哈希**，所以下载与 apply 之间不要移动或修改那个临时文件，
> 否则会被拒并提示重新下载（这是有意的）。

> 本轮设置屏只做到「查询」，**没有**在界面里放下载 / 应用按钮：安装走启动时的
> 原生提示（`api.StartUpdateCheck`），与 Fyne 版一致。
>
> **更正（2026-10-14 实机演练后）**：上句「安装仍走启动时的原生提示」在 **Wails 版并不成立**——
> `internal/api/update.go` 的 `startUpdateCheck` 全仓库无调用点，`cmd/desktop-wails/main.go`
> 只调 `update.CleanupOld()`，前端也只调 `/api/update/check`。实测：把清单指向更高版本
> （2.1.0）重启 Wails 版，日志无任何 update 行、exe 不变；而同一份配置下 Fyne 版会记
> `update available` 并弹确认框。→ 切成新 UI 后**只能查、不能装**，已列为 Round 8 第一先决项。
>
> **已修（2026-10-15）**：`startUpdateCheck` 导出为 `api.StartUpdateCheck` 并由
> `cmd/desktop-wails/main.go` 在窗口形态启动前调用，门与 Fyne 版一致；手动「查询最新更新」
> 查到新版后调 `POST /api/update/prompt`，**服务端弹同一个原生框**（共用 `runUpdatePrompt`），
> 点「是」即安装。同一天按产品决定**取消角色门**（`System.DownloadUpdate` /
> `System.ApplyUpdate` 与 `ModuleSystem` 一并删除），更新对所有登录角色开放。
> 人工确认环节仍待实测。

**关口（2026-10-14 实机通过）**：**1.3.0（Fyne 旧 UI）→ 2.0.0（Wails 新 UI）升级演练**，
含升级前备份、迁移、验签门、哈希门、失败中止与回滚材料。全程用隔离库 + 临时数据目录，
正式库 / 正式数据目录零写入。

| 验证项 | 结果 |
|---|---|
| 端到端换 UI | ✅ 13.8 MB 包 → 验签 → 验哈希 → 替换 exe → 重启；起来的是 Wails 版（日志 `RFERP 2.0.0 embedded server on http://127.0.0.1:52688` + `[WebView2] Environment created successfully`），exe 哈希 = 目标构建 |
| 升级前备份 + 迁移 | ✅ `备份\safety\pre_migrate_<ts>.sql` → 迁移到 v13 `db_identity`；数据完好（users=2 / parts=210 / products=70 / audit_log=398） |
| 备份失败即中止 | ✅ mysqldump 指向不存在的路径 → 中止迁移，库纹丝不动 |
| 回滚材料可用 | ✅ 备份在干净库还原成功 |
| 验签门 | ✅ 生产公钥拒绝陌生钥匙：`更新清单签名校验失败（可能被篡改）` |
| 哈希门 | ✅ 篡改包被拒：`更新包校验失败：文件哈希与清单不一致` |
| 确认门 | ✅ 安装前有原生确认框（`dialog.ShowConfirm`），不点不装 |
| 未执行 | ⏳ 用**生产私钥**签的「出厂 1.3.0 自助升级」；真实 2.0.0 发布版（演练用当前源码 + 版本号 2.0.0 的替身） |

### Round 8 · 交付与冻结

状态: **未开工**。先做下面的「先决项」（按序，全部来自 2026-10-14 的 Round 7 实机演练
与既有审计），再做本轮交付；交付项里在早先几轮就已满足的标 `[x]`。

#### 先决项（按此顺序；冻结前每一条都要落地或明确定案）

- [x] **① 给 Wails 版接上更新入口**（第一优先）。**2026-10-15 已落地**：`api.StartUpdateCheck`
      （`internal/api/update.go`）从 `cmd/desktop-wails/main.go` 启动前调用，门与 Fyne 版一致
      （`cfg.AutoUpdate` 为 false 时 `updateURL` 已被清空 → 不查不弹）。行为逐条对齐
      `internal/ui/update.go`：延时 3 秒静默查 → 有新版弹原生确认框（含版本与说明）→
      下载 → 替换 exe 并重启；查不到/没配置/不点确认一律安静退出。
      同时按产品决定**取消角色门**：`POST /api/update/download` 与 `/apply` 改为登录即可
      （`s.auth(h, "")`），`System.CheckUpdate/DownloadUpdate/ApplyUpdate` 三个权限点与
      `ModuleSystem` 模块一并删除——旧版的启动检查本就对任何登录用户弹框。安全边界改为与
      角色无关的三道闸门：包必须由**生产私钥** ed25519 签名、哈希与清单一致（下载与 apply
      前各核一次）、apply 只认本次下载刚校验过的文件且必须 `confirm:true`。
      与 Fyne 版的唯一差别：没有进度条（原生 MessageBox 画不出来），进度写日志 +
      一条"正在下载更新，请勿关闭程序…"提示。
      手动入口同批接上：设置屏「查询最新更新」查到新版本后调 `POST /api/update/prompt`，
      **服务端弹出与启动时完全同一个原生框**（两条入口共用 `runUpdatePrompt`），
      用户点「是」即下载安装。端点立即返回 202（对话框要等人点、下载可能几分钟，
      不能把 HTTP 请求挂在上面）；同一时刻只允许一条更新流程（`promptGate`），重复
      触发返回 409。
      **2026-10-15 实机演练（出厂 1.3.0 + 生产私钥签发的清单）已跑通弹框 → 验签 →
      下载 → 哈希核对 → 就地替换 → 重启全链路**；当时的更新包是用 `go build` 造的
      （缺 Wails build tags），新 exe 起不来——更新机制本身没问题，构建必须用
      `wails build`。
- [x] **⑦ 升级后新进程第一次开库会失败**（2026-10-15 实机演练查出并已修）。软件更新是
      「先启动新进程、老进程再退出」（`internal/update.ApplyAndRestart`），新进程只等单实例锁，
      而**单实例锁比文件句柄先释放** → 新进程在老进程还在拆 WAL/SHM 时开库，驱动报
      `disk I/O error (1546)`（主码 10），用户看到「RFERP 无法启动」，手动再点一次就好了。
      DSN 里的 `busy_timeout(5000)` 救不了：它只管 `BUSY`/`LOCKED`，不管硬 I/O 错误。
      修法：`repository.OpenSQLite` 外包一层**有界重试**（约 6 秒，只重试 `BUSY`/`LOCKED`/`IO`，
      每次写日志）；`CANTOPEN` 这类配置问题不重试，免得把明确报错拖成几秒后才出现。
      **只能在新版本这边修** —— 1.3.0 已发布，它的 `ApplyAndRestart` 改不了。
      同轮还把**弹框时机改回登录之后**（1.3.0 就是登录后检查；之前挂在启动后 3 秒＝登录前，
      会在用户刚坐下、正在录入数据时弹框，点「是」就立刻重启）。
      **同轮实测确认 ④**：`.old` 活不过下一次启动（升级失败那轮因为老进程映像占着文件而删不掉，
      下一���启动就被删）。
- [x] **⑥ `POST /api/update/apply` 回完响应后没有人退出旧进程**（2026-10-15 实施①时
      查出，**2026-10-15 已修**）。`server.go` 的 `applyUpdate` 默认就是 `update.ApplyAndRestart`，
      而它**只启动新进程、不退出自己**；handler 随后回 200 就结束了。旧进程继续持单实例锁，
      新进程带着 `RFERP_UPDATE_RESTART=1` 等满 20 秒后弹"已在运行"并自己退出——**用户在界面上
      点了安装，程序却没换掉**。原生弹框那条路早就有 `os.Exit(0)`（Fyne 版同位置也有），
      只有这个 HTTP 端点漏了。
      修法：回完响应后延迟 `exitAfterResponseDelay`（200ms）再退出，退出动作**可注入**
      （`WithProcessExit`）——否则测试进程会被自己杀掉。apply **失败**与三条前置校验失败
      （缺 `confirm` / 尚未下载 / 哈希不符）一律**不退出**：程序还好好跑着，用户可以直接重试。
      反向验证：临时去掉 `exitAfterRestart()` 后 `TestApplySchedulesProcessExit` 与
      `TestApplyExitIsDeferredNotImmediate` 立刻失败。
      **验证范围的准确说法（2026-10-15 复核后改写）**：本条**只有单元测试覆盖**，
      **不能做端到端实机验证 —— 因为当前没有调用者**。`POST /api/update/download` 与
      `/apply` 在 `web/src` 里零命中（界面走的是 `POST /api/update/prompt` 原生弹框那条），
      而 `-serve` 无头形态不注册更新端点（`cmd/desktop-wails/main.go` 的 `runHeadless`
      没有 `WithUpdate`），窗口形态又绑随机端口 + 启动 token 网关，外部无法注入请求。
      换句话说：**在界面真正走 HTTP 路径之前，这条端到端链无法被触发**。
      要端到端验，需要先有一个界面去调它（例如设置屏里做"下载并安装"按钮，以便显示
      进度与失败重试）；那时这条修复也才真正有用户可感的分量。
- [ ] **② 清掉或豁免 §4.5 的 S1 / S2**。`POST /api/backup`、`/api/export/all`、
      `/api/export/audit` 三个写端点没有本机闸门；`GET /api/datadir`、`POST /api/export/all`、
      `POST /api/export/audit` 回传服务器绝对路径。二选一并在文字上定案：补
      `requireHostOperations`，或按 §4.5「本地 / 远程能力矩阵」明确豁免。验收要点见 §4.5 该条末尾
      （跨机形态 403 或矩阵豁免 + 路径不出现绝对盘符）。
- [x] **③ 确认剪枝策略对 legacy 备份成立**（**2026-10-15 已补断言**）。结论：现有代码**成立**，
      但原本**只有注释里的承诺**、没有任何断言守着。核查到的四条剪枝路径全部枚举如下，
      没有一条会碰 `备份\` 根下的 legacy 文件：
      1. `internal/service/autobackup.go:107` — `PruneSnapshots(备份\auto, "auto", AutoBackupKeep)`；
      2. `internal/service/restore.go:288` `pruneSafetyCopies` — **有 `switch` 白名单**，
         只清 `pre_restore` / `pre_clear` / `pre_migrate`，`backup`（手动备份）不在其中；
      3. `internal/bootstrap/bootstrap.go:274` — 升级前快照，prefix `pre_migrate`；
      4. `internal/backuplayout.MigrateLegacy` — 只按前缀搬，**搬不动的留下**。
      新增 `internal/backuplayout/prune_legacy_test.go`：把四条路径真跑一遍（分类目录里塞满
      超过上限的文件，逼策略真的动手），断言 legacy 的三个文件仍在**且仍被 `List()` 列出**
      （还在磁盘上但界面看不到，等于丢失）。另有对照断言"剪枝确实删得掉自己那类"，
      否则上一条可能只是因为剪枝整体失灵才通过。
      **反向验证时踩到的坑值得记**：第一版探针用**新**时间戳（`20260924`）造 legacy，
      它混进"保留最新 keep 个"里被顺手救下来 → 断言成了假阴性。legacy 必须用**旧**
      时间戳，才真正落在"会被删"的那一侧。
      背景仍然成立：1.3.0 写 `备份\backup_<ts>.sql`，当前源码写 `备份\safety\pre_migrate_<ts>.sql`，
      老用户升级后两代并存。
- [ ] **④ 更新失败也要能回退**。`ApplyAndRestart`（`internal/update/update.go:225-253`）在
      `cmd.Start()` 失败时直接返回错误，此时 exe 已被换成新的、只剩 `.old`；而下次启动
      `CleanupOld()`（`cmd/desktop-wails/main.go:113`）会**先删 `.old`**。→ 要么启动失败自动回退，
      要么在确认新 exe 起得来之前不删 `.old`。**未实测**，需实机构造（目标 exe 启动即退出）验证。
- [ ] **⑤ 让「验签被拒」不再伪装成「没有更新」**。`Checker.Check()`
      （`internal/update/update.go:76-90`）先试配置源、失败再试 `DefaultFallbackURL`，返回**第一个
      成功**的结果；演练中配置源被生产公钥拒 → 回退源成功 → 净结果「没有更新」。至少在日志里
      区分这两者。可观测性问题，排最后，不影响安全。

#### 本轮交付

- [x] CI 覆盖前端（**Action 仍固定到 commit SHA**）：`npm run build:embed` + 前端测试
      已经在 `verify` job 里顺序执行；**没有**拆成并行 job——拆不拆不影响结论，
      现在这样只需一处 checkout 与一次依赖安装。
- [ ] `wails build` 纳入发布脚本；`release.bat` 产出改为「Wails exe + 安装包」
      （现状：`release.bat:72` 仍构建 `cmd/desktop/main.go`，产出的是 Fyne 版）
- [x] 服务用 `embed` 托管前端 dist：Round 1 就已落地——
      `internal/webassets/webassets.go` 的 `//go:embed all:dist` +
      `api.WithStaticFS`，`cmd/desktop-wails` 的窗口与 `-serve` 两种形态共用
- [ ] 部署文档：单实例 / 重启即全员登出 / 跨机需 VPN / 不暴露公网
      （`docs/` 下目前没有部署文档；相关约定散在本文档 §2 与 decisions 里）
- [ ] README 更新（构建者需 Node；**端用户无需 Node/Go 工具链**，但需 WebView2）
      （现状：两份 README 都没有 Node / WebView2 字样）
- [x] **保留 Fyne 版安装包（v1.3.0）**挂在 Release 页作为**用户可自行下载的降级通道**
      （v1.3.0 的资产已含 `RFERP-1.3.0.zip` 与 `Setup-RFERP-1.3.0.exe`）
- [x] 保留 `cmd/desktop` 与其构建脚本（代码不删，便于紧急修复时回退）

**关口**：全新机器从零安装 + 旧版升级，两条路径均通过；并补上本次演练未跑的两项——
用生产私钥签的「出厂 1.3.0 自助升级」与真实 2.0.0 发布版（见 Round 7 关口表末行）。

> **顺延（不在本轮）**：零文件上传（§4.5 缺口 3）与远程形态完工（含
> `REMOTE_EXPORT_DOWNLOAD_AVAILABLE = false` 的下载按钮），随「运行模式选择 / 远程服务器
> 联机」一起做。

### Round 9 · 移除 Fyne 构建入口

- [ ] Web UI 稳定一个版本后，移除 `cmd/desktop` 的构建入口
- [ ] 此时方可按 §7 的「条件性表格」核对依赖树与漏洞告警是否真的收敛
- [ ] Fyne 版仅作为历史版本存档

### Round 10 · 国际化（i18n）— 最后做

**定序理由（2026-10-11）**：放在最后是刻意的，不是排不下。

- 现在做要改遍**每一屏的每一个字符串**（Round 3–8 会持续新增屏），做一半的屏开始
  就要维护两套写法，越往后返工越大
- 当前**全中文硬编码是正确状态**，不是待修的缺陷。用户是国内仓管，D-008 已明记
  「国内用户等于没提示」
- 半成品最糟：部分屏有翻译、部分没有，用户切到 en 会看到中英混杂

**前置条件**：Round 3–8 全部交付并稳定一个版本后，**一次性**抽取全部字符串。
中途不做局部抽取。

- [ ] 抽出 `web/src/i18n/`：语言文件（`zh-CN.json` / `en-US.json`）+ `t(key)` + 语言上下文
- [ ] **一次性**替换全部屏的硬编码中文，包括：导航、表格列头、按钮、错误提示、
      空状态、服务端返回错误的中文文案（后者需后端返回错误码而非文案）
- [ ] 设置屏提供语言选择（侧栏左下角「设置」→ 语言）
- [ ] 语言持久化到 `config.json`
- [ ] 关口：切换语言后**无任何残留中文**；缺 key 时回落中文而非显示 key 本身

> **注意**：最后一条「服务端错误文案」是这轮最大的工作量。`internal/api` 现在
> 直接返回中文错误串（`writeErr(w, 400, "invalid days")` 之类是英文，但业务层是中文），
> 要正确 i18n 必须改成返回**错误码 + 参数**，由前端查表翻译。这会牵动
> 全部 `internal/api` 与 `internal/service` 的错误路径，排期时不要低估。

### 每轮的工程节奏（与协作者的同步方式）

| 做法 | 理由 |
|---|---|
| **每轮一个独立小 PR** | 24 屏的迁移做成一个大分支，diff 会大到无法真正审阅 |
| **合并代码不发给用户** | 只有 `release.bat` 产生用户可见物（GitHub Release + 签名清单）。合并 PR 对用户零影响 |
| 提交 `docs/plans/wails-migration-progress.md` | 实机测试（Windows GUI + 真实数据库）**CI 做不了**，结果不落成文字就只存在于记忆里 |
| 一个跟踪 issue 勾选十轮状态 | 给出全局进度视图，链接各轮 PR |
| PR 描述固定四段 | 本轮范围 / 关口验收清单 / 实机测试结果（环境矩阵 + 差异）/ 需要决策的点 |

进度文件每轮追加形如：

```
## Round 3 · 主数据 CRUD
状态: 关口未过
范围: 产品/零件/BOM 列表与编辑，BOM 搜索选择器
实机验证:
  - 环境: Win11 23H2 / MySQL 8.0 / 本地构建
  - 用例: BOM 级联一致性 12/12 通过
  - 用例: 并发编辑 3/3 通过（2 处差异，已修）
遗留: SQLite 路径下 BOM 级联未验证 -> 顺延 Round 4
需要决策: 无
```

**纪律**：发现与计划不符的地方立刻开 issue，不要埋在 PR 评论里——
本计划 §0.1 的基线曾标注为 `eef5905`，一度落后主干 40 余个提交。

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
| 3 | **零文件上传** | 全仓库 `multipart` / `FormFile` 零命中 | Round 5 前补端点 |
| 4 | **5 个用例无端点** | `RestoreDatabase` / `ClearDatabase` / `DataDir` / `SetDataDir` / MySQL 启停；权限点已定义但无路由消费 | Round 5 前补端点；其中恢复/清空在阶段 B/C 需按下方矩阵禁用 |
| 5 | **备份下载返回服务器路径** | `client.go:424-433` 丢弃 `saveDir`，返回服务端绝对路径 | 改为文件流；现形态属信息泄露 |
| 6 | **无登出端点** | `internal/api` 中 `logout` 零命中；`ui/app.go` 的"退出登录"只调本地 `auth.Logout()`，**`Client.tok` 不清** | Round 1 补（见 §4.6 会话设计）|
| 7 | **无超时 / 无限流 / 无安全响应头** | 无 `ReadTimeout`/`WriteTimeout`/`IdleTimeout`；登录失败不计数不锁定 | Round 1 补 |
| 8 | **`nativefiledialog` 无对应物** | `SHBrowseForFolderW` 仅 `ui/backup.go`、`ui/setup.go` 两处调用 | ✅ Round 5 已按宿主注入解决（见 §2.4.1）；`ui/setup.go` 仍走文本输入 |

### ✅ T2 已修：`update/apply` 接受任意路径（2026-10-13 审计发现）

**这是 CodeQL 完全没报的一个真实问题** —— 工具报了 30 条误报，却漏掉了这一条。
它也是「不能靠静音把 CI 弄绿」的实证：若当初把那 30 条压掉、CI 变绿，这次审计
根本不会发生。

**问题**：`POST /api/update/apply` 把请求体里的 `path` 直接交给
`update.ApplyAndRestart`，而那条链是

```
从该路径的 zip 里取出任意 .exe  →  覆盖程序自身  →  启动它
```

且 apply 阶段**不验签**。下载阶段确实验了 SHA-256 与 ed25519 清单，但
**验证绑在下载产生的临时文件上，与 apply 传进来的路径毫无关联** ——
「校验过的文件」与「即将执行的文件」被割开了。于是任何已登录会话
（当时该路由要求 `System.ApplyUpdate`，即仅管理员；该权限点已于 2026-10-15 随
「更新对齐旧版」删除，改为登录即可，见 Round 8 先决项 ①），
只要目标机器上存在一个 zip 且知道路径，就能让 ERP 以自己身份运行
攻击者的程序；随之可读 `config.json`（含数据库凭证、当前用户 DPAPI 可解）、
连库、读写全部备份与导出。

它同时也违背了本仓库自己的约定：恢复 / 清空 / 改数据目录都是「客户端说什么不算、
服务端独立复核」，而这里 `confirm` 是**客户端自述** —— 代码注释还写着它是
"唯一的闸门"，而闸门在客户端手里就等于没有。

**修复（方案甲：服务端记住下载产物）**

- `Server.pendingUpdate` 保存**下载步骤刚产生并通过校验**的 `{path, sha}`
- `handleApplyUpdate` **忽略请求体的 path**，只应用记住的那一个；没下载过回 400
- apply 前用 `update.FileSHA256` **再核对一次哈希**，连"下载与 apply 之间被换文件"
  这个窗口一并关掉；不一致则**作废待办**并提示重新下载
- applier 失败**不清**待办（文件还在，可直接重试）；成功才清
- 新增 `update.FileSHA256` 作为**唯一**的哈希实现，`downloadOnce` 一并改用它 ——
  两处各写一份会漂移（一边大小写不敏感、另一边没改），而漂移后果正是上面那个割裂
- `WithUpdateApplier` 注入点：真实的 `ApplyAndRestart` 会**替换正在运行的进程**，
  测试绝不能走真实路径（这次是主动加的，不是踩坑后补的）

**未做、明确留作后续**：本机闸门（方案丙）。远程形态下该功能本就因
`WithUpdate` 未配置而关闭（`s.updater.url == ""` → 400），闸门不是必需；
真正拦住任意路径的是上面那条因果链。

> **一个巧合值得记**：`update_test.go` 里的 apply 用例全是负例
> （缺 confirm / confirm:false / 空 body），**没有一个传 `confirm:true`**。
> 否则按修复前的实现，跑测试会真的把测试二进制替换掉。这是运气，不是设计 ——
> 也说明"有副作用的端点"要么有注入点，要么就没被测试真正覆盖。

### 🔴 遗留待办：远程服务器联机启动后处理（2026-10-13 记录）

本轮安全自检发现两项**既有**问题（都不是本轮引入），用户决定**记录下来、等远程服务器
联机计划开始后一并处理**。记在这里是为了它们不在合并中消失。

| # | 问题 | 证据 | 为什么现在不修 |
|---|---|---|---|
| S1 | **三个写操作端点没有本机闸门** | `POST /api/backup`、`POST /api/export/all`、`POST /api/export/audit` 都没有 `requireHostOperations`；同组的 `restore` / `clear` / `set-datadir` / `pick` / `export/all/open` 都有 | 远程形态的导出流程尚未设计（§4 Round 2「运行模式选择」暂缓）。现在加闸门会挡住一条还没定型的路径。远程形态定稿时必须一并决定这三个是否纳入能力矩阵 |
| S2 | **远程形态泄露服务器绝对路径** | `GET /api/datadir`、`POST /api/export/all`、`POST /api/export/audit` 都回绝对路径（`dataDir` / `backupDir` / `backupDirs` / `dir`） | 单机形态下这是**必需**的：界面上不显示路径，用户就不知道东西去哪了、也不知道该去哪里取回。远程形态下这些是**服务器**的路径布局。需在远程形态里区分「本机路径照常显示」与「远程路径只显示相对位置」 |

影响评估：S1 是磁盘占用向量（客户端可让服务器写文件），S2 泄露目录布局但**不含任何口令**
（`config.json` 的 `password_enc` 从来不经端点回传）。两项都**不影响当前的单机形态**——
单机形态下 `hostOperations` 恒为 true，闸门不改变行为；路径也只有用户自己看得到。

处理时的验收要点：① 三个端点在 `cmd/server`（跨机形态）下必须与 `restore`/`clear` 一样
被 403 挡下，或按矩阵明确豁免；② 关口需补远程形态下"路径不出现绝对盘符"的断言。

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

### 与多实例无关、Round 1 就应补齐的项

| 项 | 现状 | 决定 |
|---|---|---|
| **登出端点** | 缺失（`delete(s.sessions, tok)` 仅存在于惰性过期清理路径） | 补 `DELETE /api/session`：删服务端会话 + 前端清凭据 + 中止该会话在途请求 |
| **过期会话清理协程** | 当前 map 无界增长（内存泄漏） | 补 |
| **会话有效期** | 绝对上限 24 小时，滑动空闲 4 小时 | 原为 12h 绝对上限 + **按 30 分钟滑动空闲续期**；2026-10-12 由用户改为 24h/2h，再把空闲调到 4h（12h 每天咬人一次；4h 覆盖吃饭/办事/短时离开） |
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

## 4.7 Round 1 期间应一并完成的"阶段 B 准备"

| 优先 | 项 | 现在做的理由 |
|---|---|---|
| **T1（Round 1 内做）** | 前端由内嵌服务托管 | 本地与远端同一份代码路径，一次投入两处受益 |
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
| **1（关口）** | 见 §4 Round 1 的验证表与实机验收矩阵。**未通过不进入 Round 2** |
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
> 单元测试与空白检查；上表的多数项必须实机跑。实机结果须落成文字（见 §4 的「每轮的工程节奏」）。

---

## 7. 决策状态

### 已定（2026-10-02，见 §4 Round 0 完整表）

| 项 | 决定 |
|---|---|
| 交付形态 | **桌面端内嵌 WebView2**（Wails v2 单 exe）|
| 首轮范围 | **阶段 A 单机桌面**，能力不缩水 |
| 前端框架 | **React + TypeScript + shadcn/ui + Tailwind** |
| 前端托管 | **内嵌 HTTP 服务 + 前端从 `127.0.0.1:随机端口` 加载**（实现待 Round 1 关口验证后写死）|
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
| 内嵌服务的**具体实现**（端口分配策略、就绪等待机制） | Round 1 关口实测后 |
| 会话是否落库（阶段 B 多客户端场景） | 阶段 B 启动前 |
| `nativefiledialog` 的替代方案 | Round 2 |
| 阶段 B 是否需要"就地升格"以外的部署路径（B 路径需导出导入工具） | 阶段 B 启动前 |
| API 版本前缀的具体形态 | 首次跨机部署前 |

> 早期版本此处写「无阻塞项」。该说法**不准确**——上表五项以及 §4 Round 1 关口的七个验证项，
> 都是真实的未决事项。区别在于：**它们都有明确的验证时点与责任人**，而不是"没有"。

### 依赖收敛的条件性表述（2026-10-02 修订）

删除 Fyne 版后，以下四个模块**预计**随之消失——但**在 Fyne 入口真正删除之前不能断言归零**：

- `golang.org/x/net`、`golang.org/x/image`、`golang.org/x/text`、`github.com/yuin/goldmark`
  四个模块（33 个已知漏洞）均由 Fyne 引入
- 唯一例外：`filippo.io/edwards25519`（2 个漏洞）随 `go-sql-driver/mysql` 引入，需保留 → 届时顺手升级

> **过渡期保留 Fyne 可构建入口**（见 §4 Round 0「旧版 UI 定位」），因此这期间依赖树与告警**不会**归零。
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
| 1 | 明确 Wails 如何加载前端 | ✅ §2 定为「内嵌 HTTP 服务 + 随机端口」；**具体实现标为待 Round 1 关口验证**，不再宣称已定案 |
| 2 | 启动 token 与会话 token 分开 | ✅ §2/§4.6 分开说明：前者管传输层信任（仅阶段 A），后者管身份与授权 |
| 3 | `wails.Options{JS:...}` 应标为伪代码 | ✅ §5 改为要点描述，并说明启动 token 走 `HttpOnly` Cookie 而非注入 JS 变量 |
| 4 | §4.5 增加本地/远程能力矩阵 | ✅ 新增矩阵表，恢复/清空/改目录/MySQL 启停在阶段 B/C 禁用 |
| 5 | 按最新主线校准契约 | ✅ §0.1 基线更新至 `f3448cd`；§6 验收清单补入 #42 三分类与 #48 数据库不可用 |
| 6 | §4.6/§4.7 收敛"只改绑定地址" | ✅ 改为「不止绑定地址」，列出认证、文件传输、兼容验证；补 API 版本前缀 ≠ 版本协商 |

### 三、执行与验收

| # | 意见 | 处理 |
|---|---|---|
| 1 | 全面逐屏迁移前加纵向试验关口 | ✅ §4 新增「Round 1 · 纵向关口」，含 7 项验证条件与实机验收矩阵 |
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
| 屏幕清单是否含工具与测试文件 | §0.1 的「UI 屏幕（24 个 .go 文件）」把非屏幕文件计入了，需在 Round 1 前重新清点 |
| 工期估算 | 各轮天数仍为 2026-09-29 的粗估，**待 Round 1 关口实测后校准** |
| 列设置 / 自定义列 | 2026-10-10 定：Round 1 **不做**。用户偏好存储是独立一块工作量，另排 |

### Round 1 实机验收待办（2026-10-10）

Round 1 的关口已在 Win11 上逐项验证（SQLite 与 MySQL 双通道、安装包、登录闭环、
启动 token、列表屏、权限拒绝路径、端口生命周期）。以下两项**必须换机器或改系统
状态**才能验，无法在开发机上完成，**合并前必须补上**。

| 待办 | 为什么这台机器做不了 | 验收方法 | 阻塞级别 |
|---|---|---|---|
| **Win10 实机验收** | 本机为 Win11，没有 Win10 环境 | 在 Win10 上装 `Setup-RFERP-*.exe`，走完：首启配置 → 建管理员 → 登录 → 零件列表 → 建零件 → 登出。确认 WebView2 缺失时能正确引导安装 | **合并前必做** |
| **WebView2 缺失场景** | 本机已装 WebView2 154，无法触发缺失分支 | 在 Win10 干净镜像（无 Edge 更新）上卸载 WebView2 运行时后启动，确认弹出的是中文提示并能跳到下载页 | **合并前必做** |
| **旧版数据库 → 新 UI 版本兼容性** | 需要一份**旧 Fyne 版留下的数据目录**；本机只有新构建 | 见下方专项（3 步） | **发布前必做** |

#### 旧版本数据库 → 新 UI 版本（专项）

**范围（2026-10-11 收窄）**：第一版新 UI **不涉及数据库结构改动**，因此不测
「装回旧版能否继续用」——用户没有回退需求，新旧版本之间也不存在必须维持的
回退承诺。**只需确认一件事：Fyne 版留下的数据库结构能被新版正常打开与使用。**

| 步骤 | 做法 | 通过标准 |
|---|---|---|
| 1 | 装已发布的 `Setup-RFERP-1.3.0.exe`（Fyne 版），建管理员，录入种子数据（产品/零件/BOM/批次），**记下各表条数** | — |
| 2 | **不卸载、不清数据**，用新 UI 版本打开同一个数据目录 | 装配成功，**无迁移错误**，不弹任何报错 |
| 3 | 登录后逐屏核对产品/零件/BOM/批次的**条数与内容** | 与步骤 1 记录的**逐项全等** |

> **不做**（已从原 6 步中移除）：迁移幂等复启动、装回旧版验证回退可用性。
> 理由：第一版不改结构，不存在新版写坏旧库的情况；用户也无回退诉求。
>
> 若将来某一版**引入了破坏性迁移**（删列、改类型、重写表），必须把这两项加回来
> ——那时「新版升级失败能否退回」就重新变成一个真实的用户风险。

已在本机验证、不需再验的部分：WebView2 提示**文案**已由单测锁定（非空、中文、
带下载链接），缺的只是「真的缺失时确实弹出这一段」这一步。

> 这两项只影响**安装交付**关口，不影响代码正确性：合并 PR 不会发布任何用户可见
> 版本（只有 `release.bat` 会），而 D-008 决定旧安装包继续作为回退出口。

---

## 附录 A · 视觉方案原型（2026-10-10）

定稿前用可交互原型做了配色 / 布局 / 密度 / 圆角的正交对比，并逐屏讲解 RFERP 特有语义
（恢复四道门、三分类结果、权限矩阵）。原型不随仓库提交，Round 1 依 §Round 1.1 实现。

**参考来源**

- Ant Design `Table` 企业级规范：筛选区、`tableAlertRender` 批量栏、`fixed:'right'` 固定列、
  `children` 树形数据、分页与每页条数、`size='small'|'middle'` 紧凑档
- shadcn-admin 壳层：inset / dark / floating 三种侧栏 + 图标轨
- 中后台通行页结构：页头 → 筛选区 → 批量栏（选中时）→ 表格 → 分页

**原型暴露并已修正的两个错误**

1. BOM 原型最初画成平表 —— **BOM 本身是树**（产品 → 组件 → 零件），平表丢掉
   「这个零件用在哪几个组件里」的信息。已改为树形表。
2. `fixed:'right'` 缺 `scroll.x` 等价物时，固定操作列会**直接盖住最后一列**
   （当时盖掉了「更新时间」）。根因是表格无最小宽度、被压缩到与容器等宽。
   已通过 `min-width` + 实测阈值修正（§Round 1.1）。

**对比中排除的选项**

- 赭红 `#B91C1C`：主按钮与「已撤销」状态红同族，语义撞车
- 顶部导航 + 二级页签：9 个一级模块在 940–1200px 目标区间**必然换行**，需额外设计收纳
- 图标轨 50px：比 166px 侧栏多让出 116px 表格宽度，但 9 个模块只能靠图标辨识，必须配 tooltip
