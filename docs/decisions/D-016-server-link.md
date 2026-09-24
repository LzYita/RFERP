# D-016 — Local → Server link groundwork (本机 → 服务器接入前期准备)

**Status / 状态:** Accepted (groundwork only) / 已接受（仅前期准备）
**Scope / 范围:** seam + facts only; no data migration tooling / 只做接缝与事实读取；不含数据搬迁工具

---

## English

### Context

A user who picked **Local mode + MySQL** keeps their data in a MySQL database on
one machine. Later they may want several machines to share that data — that is
**Client mode against a server** (`RFERP-server.exe`, an HTTP front end over the
same use cases).

Two deployment paths are both legitimate and neither is chosen in advance:

| Path | Server runs on | Data movement |
|---|---|---|
| **A. Promote in place** | the same machine that holds the MySQL | **none** — the server reads the same `config.json`, hence the same DSN |
| **B. New external server** | another machine with its own MySQL | requires export → import (a later phase) |

Today the app cannot tell these apart, so switching run mode always warns
"本机库与服务器库是两套独立数据" — true for path B, misleading for path A.
The missing capability is not a feature, it is **the ability to state the facts**.

### Decision

1. **Promoting Local+MySQL to a server is an in-place change, not a data migration.**
   `cmd/server` already loads the same `config.json`, so a server started on the
   data machine serves the very same database. No copy is required.
2. **Every database carries a stable identity** (`db_identity`, one row, UUID,
   written once and never changed). It is created by dual-end migration **v13**
   (MySQL and SQLite both implement it, per D-015).
3. **The server describes itself** over an unauthenticated
   `GET /api/v1/serverinfo`: app version, API version, schema version,
   database id, storage kind. It exposes no business data.
4. **Run-mode switching must be preflighted, never silent.** `usecase.PlanLink`
   compares local facts with the probed server facts and returns one of:
   same database (no migration needed) / other database (migration required) /
   incompatible API version. This continues the "never switch silently" rule
   from D3 and E6.
5. **Cross-database data migration is out of scope here.** This decision only
   establishes the seams and the facts needed to decide; the transfer tool is a
   separate, later phase.
6. **Clients talk HTTP (8080) only.** MySQL (3306) stays private to the machine
   that hosts it.

### What ships in this phase (P0)

- `db_identity` table + dual-end step **v13** (idempotent; backfills a missing row)
- `usecase.ServerInfo` / `usecase.ServerDescriptor`, embedded in `Applications`
- `usecase.PlanLink` — a pure, unit-tested decision function
- `GET /api/v1/serverinfo` (no auth) and `api.Probe` (client side)
- `internal/dbfile` — leaf package for `IsSQLiteSnapshot`, so the UI no longer
  imports `internal/service`

### What deliberately does NOT change

- No UI behaviour change; the mode-switch dialog keeps its current wording until P1
- No new tables other than `db_identity`; no column changes
- No change to backup/restore semantics

### Consequences

- A Local+MySQL installation can be turned into a server without touching data.
- Switching mode can later say *why* it is safe (or not), instead of a blanket warning.
- Snapshots and backups can be checked against the owning database.
- Cost: one extra table, one extra endpoint, one pure function.

---

## 中文

### 背景

选择**本机模式 + MySQL** 的用户，数据在一台机器的 MySQL 里。以后可能想让多台机器
共用这份数据 —— 那就是**客户端模式 + 服务器**（`RFERP-server.exe`，同一套用例的
HTTP 前端）。

两条部署路线都成立，且**不预先二选一**：

| 路线 | 服务器在哪 | 要不要搬数据 |
|---|---|---|
| **A. 原地升级** | 就是存放 MySQL 的这台机器 | **不用搬** —— server 读同一份 `config.json`，连同一个 DSN |
| **B. 外接新服务器** | 另一台机器，用它自己的 MySQL | 需要导出 → 导入（后续阶段） |

现在程序分不清这两种情况，所以切换运行模式时一律警告「本机库与服务器库是两套独立
数据」—— 对路线 B 是对的，对路线 A 是误导。缺的不是功能，而是**把事实说清楚的能力**。

### 决策

1. **本机 + MySQL 升级为服务器是原地变更，不是数据迁移。**
   `cmd/server` 本来就读同一份 `config.json`，所以在数据所在机器上起一个 server，
   服务的就是同一个库，无需任何拷贝。
2. **每个数据库带一个稳定身份**（`db_identity`，一行、UUID，写入后永不改动），
   由双端迁移 **v13** 创建（MySQL 与 SQLite 都要实现，遵循 D-015）。
3. **服务器自描述**：免鉴权的 `GET /api/v1/serverinfo` 返回应用版本、接口版本、
   模式版本、数据库身份、存储类型。不暴露任何业务数据。
4. **切换运行模式必须先预检，禁止静默切换。** `usecase.PlanLink` 比较本机事实与
   探测到的服务器事实，给出三种结论之一：同一个库（无需迁移）/ 另一个库（必须迁移）/
   接口版本不兼容。这是 D3 与 E6「不静默切换」原则的延续。
5. **跨库数据搬迁不在本期范围。** 本决策只建立"做判断所需的接缝与事实"，
   搬迁工具是后续独立阶段。
6. **客户端只走 HTTP(8080)**，MySQL(3306) 保持只对本机开放。

### 本期（P0）交付

- `db_identity` 表 + 双端步骤 **v13**（幂等；缺行时补行）
- `usecase.ServerInfo` / `usecase.ServerDescriptor`，并入 `Applications`
- `usecase.PlanLink` —— 纯函数，带单测
- `GET /api/v1/serverinfo`（免鉴权）与客户端侧 `api.Probe`
- `internal/dbfile` —— 承载 `IsSQLiteSnapshot` 的叶子包，界面层不再 import `internal/service`

### 明确**不做**的事

- 不改界面行为；切换运行模式对话框的文案保持现状，直到 P1
- 除 `db_identity` 外不新增表、不改任何列
- 不改备份/恢复语义

### 影响

- 本机 + MySQL 的安装可以不动数据直接变成服务器。
- 以后切换模式能说明**为什么安全**（或不安全），而不是一句笼统警告。
- 快照与备份可以校验归属。
- 代价：一张表、一个端点、一个纯函数。
