# RFERP — Renfeng Warehouse Management System / 仁风仓库管理系统

**English** | **中文**

---

# English

A desktop **inventory & batch-traceability** application for small manufacturing — covering products, parts, BOM, production batches, stock, operation logs and data backup/export.

Built with **Go + Fyne**; data is stored in **MySQL**; supports one-click install and auto-update.

## Preview

![Main window](docs/screenshot.png)

## Features

- **Users & permissions**: login, four roles (admin / warehouse / production / viewer), per-role feature visibility, real operator recorded in the audit log
- **Products / parts**: codes, specs, units, status, stock warnings
- **BOM**: product-part usage, loss rate, replaceable flag, usage mode
- **Production batches**: planned / produced quantity, status flow, material feeding
- **Batch traceability**: trace material source by product batch or part
- **Inventory**: stock-in, stock-out, stocktaking, warning list
- **Operation log**: structured view (type / action / object / changes) with a detail dialog
- **Backup & export**: database backup (SQL) and per-table CSV export; data directory configurable

## Download & install

1. Open the [latest release](https://github.com/LzYita/RFERP/releases/latest)
2. Download `Setup-RFERP-x.y.z.exe`
3. Run it (Chinese UI, custom install path supported)

> Requires **Windows 10 / 11 (64-bit)**

## First run

**1) Database wizard**

1. Auto-detects a local MySQL (service name, port, `mysqldump` path)
2. Enter connection info (host, port, user, password, database) and the **data directory**
3. Click "Initialize and start":
   - Creates the database and tables
   - When connecting as root, creates a dedicated least-privilege account
   - Stores the connection info locally, encrypted with **Windows DPAPI**

**2) Create the administrator account**

Once the database is ready, if there are no accounts yet, you are guided to create an **administrator** (username + password). On later launches a **login window** appears; tick "remember me" to sign in automatically.

> If MySQL is not installed, the wizard offers a link to the official installer.

## Users & permissions

| Role | Description |
|---|---|
| Admin | Everything, including user management and backup/restore |
| Warehouse | Writable for parts/inventory, read-only elsewhere |
| Production | Writable for batch traceability, read-only elsewhere |
| Viewer | View and export only |

- Features you lack permission for are **hidden** (not greyed out)
- Every create/update/delete/stock operation **records the operator**
- Accounts live in the database and are **shared across machines**; at least one enabled admin is always kept

## Auto-update

- Checks for a new version on startup
- The package and manifest are verified with **Ed25519 signatures**; updates are rejected on mismatch
- Prompts when a new version is found; you can install immediately

## Build from source

### Requirements

| Item | Requirement |
|---|---|
| OS | Windows 10 / 11 (64-bit) |
| Go | 1.22 or newer (latest stable recommended) |
| C compiler | **64-bit mingw-w64 GCC** (required by CGO). **Verified: TDM-GCC 64-bit (10.3.0)**; other GCC distributions may fail to produce a runnable binary |
| Inno Setup | 6 (only to build the installer) |

> These tools are needed by **builders only**; **end users need neither Go nor GCC** (releases bundle their runtime).

### Build

```bat
build.bat
```

Output: `RFERP.exe`

> Scripts find `go` / `gcc` / `gh` / `ISCC` on the system PATH and run relative to their own directory, so they work from any location.
> Override with environment variables: `GOROOT`, `GCC_DIR`, `GH`, `ISCC`.

### Build the installer

```bat
build-installer.bat
```

Output: `dist\Setup-RFERP-<version>.exe`

## Tech stack

| Layer | Technology |
|---|---|
| Language | Go |
| Desktop UI | Fyne v2 |
| Database | MySQL 8.0 |
| Password hashing | PBKDF2-SHA256 |
| Credential protection | Windows DPAPI |
| Update signing | Ed25519 |
| Installer | Inno Setup 6 |

## Layout

```
cmd/
  desktop/       application entry point
  keygen/        generate the update signing keypair
  signmanifest/  generate and sign the update manifest
internal/
  auth/          users, roles, permissions, password hashing, remember-me
  config/        config loading and encrypted storage
  secret/        DPAPI encrypt/decrypt
  repository/    data access
  service/       business logic
  migrate/       versioned database migrations
  update/        auto-update
  winappid/      Windows taskbar AppUserModelID
  nativefiledialog/  native folder picker
  ui/            interface (incl. login, user management)
  ...
setup.iss        installer script
build*.bat       build scripts
release.bat      release script
```

## License

No open-source license is specified. Please contact the author before using, distributing or modifying.

---

# 中文

面向小微生产的**进销存与批次追溯**桌面应用，覆盖产品、零件、BOM、生产批次、库存、操作记录与数据备份导出。

使用 **Go + Fyne** 构建，数据存储于 **MySQL**，支持一键安装与自动更新。

## 界面预览

![主界面](docs/screenshot.png)

## 功能特性

- **用户与权限**：账号登录、四种角色（管理员 / 仓管 / 生产 / 只读）、按角色控制功能可见性、操作人留痕
- **产品 / 零件管理**：编码、规格、单位、状态、库存预警
- **BOM 管理**：产品与零件的用量关系、损耗率、可替换与用量模式
- **生产批次**：计划 / 已产数量、批次状态流转、投料记录
- **批次追溯**：按产品批次或零件追溯用料来源
- **库存管理**：入库、出库、盘点、预警列表
- **操作记录**：结构化展示（类型 / 操作 / 对象 / 变更明细），可查看详情
- **备份导出**：数据库备份（SQL）与各表导出（CSV），数据目录可自选

## 下载安装

1. 打开 [最新版本](https://github.com/LzYita/RFERP/releases/latest)
2. 下载 `Setup-RFERP-x.y.z.exe`
3. 双击安装（中文界面，可自选安装路径）

> 系统要求：**Windows 10 / 11（64 位）**

## 首次运行

**① 数据库配置向导**

1. 自动检测本机 MySQL（服务名、端口、`mysqldump` 路径）
2. 填写连接信息（主机、端口、用户名、密码、数据库名）与**数据目录**
3. 点击「初始化并启动」：
   - 自动创建数据库与数据表
   - 使用 root 连接时，自动创建专用最小权限账号
   - 连接信息以 **Windows DPAPI 加密**后保存在本机

**② 创建管理员账号**

数据库就绪后，若系统还没有任何账号，会引导创建**管理员**（用户名 + 密码）。
之后每次启动进入**登录窗**，可勾选「记住登录」下次自动进入。

> 若本机尚未安装 MySQL，向导提供官方安装器下载入口。

## 用户与权限

| 角色 | 说明 |
|---|---|
| 管理员 | 全部功能，含用户管理与备份恢复 |
| 仓管 | 零件/库存可写，其余只读 |
| 生产 | 批次追溯可写，其余只读 |
| 只读 | 仅查看与导出 |

- 无权限的功能**不会显示**（而非置灰）；
- 所有新增/修改/删除/出入库等操作**自动记录操作人**；
- 账号存于数据库，**多机共享**；至少保留一个启用的管理员。

## 自动更新

- 程序启动后会自动检查新版本
- 更新包与更新清单使用 **Ed25519 签名**校验，签名不符则拒绝更新
- 发现新版本时弹窗提示，可选择立即更新

## 从源码构建

### 环境要求

| 项 | 要求 |
|---|---|
| 操作系统 | Windows 10 / 11（64 位） |
| Go | 1.22 或更高（建议使用最新稳定版） |
| C 编译器 | **64 位 mingw-w64 GCC**（CGO 必需）。**已验证：TDM-GCC 64 位（10.3.0）**；其他 GCC 发行版可能无法生成可运行的程序 |
| Inno Setup | 6（仅构建安装包时需要） |

> 以上工具仅**构建者**需要；**最终用户安装和运行不需要 Go 或 GCC**（发布产物已自带运行库）。

### 构建

```bat
build.bat
```

产物：`RFERP.exe`

> 脚本通过系统 PATH 查找 `go` / `gcc` / `gh` / `ISCC`，并相对脚本自身目录运行，可在任意路径执行。
> 如需覆盖，可设置环境变量：`GOROOT`、`GCC_DIR`、`GH`、`ISCC`。

### 构建安装包

```bat
build-installer.bat
```

产物：`dist\Setup-RFERP-<版本>.exe`

## 技术栈

| 层 | 技术 |
|---|---|
| 语言 | Go |
| 桌面 UI | Fyne v2 |
| 数据库 | MySQL 8.0 |
| 密码哈希 | PBKDF2-SHA256 |
| 凭据保护 | Windows DPAPI |
| 更新签名 | Ed25519 |
| 安装包 | Inno Setup 6 |

## 目录结构

```
cmd/
  desktop/       程序入口
  keygen/        生成更新签名密钥对
  signmanifest/  生成并签名更新清单
internal/
  auth/          用户、角色、权限、密码哈希、记住登录
  config/        配置加载与加密保存
  secret/        DPAPI 加解密
  repository/    数据访问
  service/       业务逻辑
  migrate/       版本化数据库迁移
  update/        自动更新
  winappid/      Windows 任务栏 AppUserModelID
  nativefiledialog/  原生文件夹选择器
  ui/            界面（含登录、用户管理）
  ...
setup.iss        安装包脚本
build*.bat       构建脚本
release.bat      发布脚本
```

## 许可证

本项目未指定开源许可证。如需使用、分发或修改，请先与作者联系。
