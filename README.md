# RFERP
# RenFeng ERP(Warehouse Management System)

**English** | [中文](README.zh-CN.md)

---

A desktop **inventory & batch-traceability** application for small manufacturing — covering products, parts, BOM, production batches, stock, operation logs and data backup/export.

Built with **Go + Fyne**; data is stored in **MySQL 8** or in a local **SQLite** file; supports one-click install and auto-update.

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
- **Backup & export**: whole-database backup and restore (MySQL SQL dump, SQLite snapshot), per-table CSV export; data directory configurable

## Download & install

1. Open the [latest release](https://github.com/LzYita/RFERP/releases/latest)
2. Download `Setup-RFERP-x.y.z.exe`
3. Run it (Chinese UI, custom install path supported)

> Requires **Windows 10 / 11 (64-bit)**

## Storage

| Storage | When to use |
|---|---|
| **MySQL** (default) | Several people / several machines share one database. Requires MySQL 8 on the machine. |
| **SQLite** | Single machine, no extra install. The whole database is one file: `<data directory>\rferp.db`. |

- The **first-run wizard** offers both; you can also set `"storage": "sqlite"` in `config.json`.
- A config with no `storage` key is always treated as **MySQL** — the app never switches silently.
- **Switching storage only changes the configuration; it does not move data.** Export from the old storage and import into the new one yourself.
- Backup/restore follows the storage: MySQL uses `.sql` dumps (append-style `INSERT` import), SQLite uses whole-file `.db` snapshots (full rollback, then the app restarts). The two are **not** interchangeable.

## First run

**1) Choose a storage** — MySQL or SQLite (see *Storage* above)

**2) Database wizard (MySQL only)**

1. Auto-detects a local MySQL (service name, port, `mysqldump` path)
2. Enter connection info (host, port, user, password, database) and the **data directory**
3. Click "Initialize and start":
   - Creates the database and tables
   - When connecting as root, creates a dedicated least-privilege account
   - Stores the connection info locally, encrypted with **Windows DPAPI**

**3) Create the administrator account**

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
| Go | **1.26 or newer** (releases and CI are built with **go1.26.5**) |
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
| Database | MySQL 8.0, or embedded SQLite (`modernc.org/sqlite`, pure Go, no CGO) |
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

[↑ Back to top](#rferp)
