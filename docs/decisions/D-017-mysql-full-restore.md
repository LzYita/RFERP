# D-017: MySQL SQL 导入 = 整库恢复 (MySQL SQL import is a whole-database restore)

**Status / 状态:** Accepted / 已接受
**Scope / 范围:** restore & clear semantics + safety copies / 恢复与清空语义 + 安全副本
**Issue / 关联:** #26

---

## English

### Context

The MySQL GUI restore was implemented as an **append-only INSERT replay**: the
dump is read line by line and every `INSERT INTO` is executed inside one
transaction. Nothing is deleted first.

That made "restore a complete backup" impossible in practice:

- the export is a full `mysqldump`, so it contains `INSERT` rows for **every**
  table, including `schema_migrations` and `users`;
- the target database is non-empty, so those rows collide with existing primary
  keys (`schema_migrations.version` is `PRIMARY KEY`);
- the collision happens on the **last** table in the dump (alphabetical order),
  by which time every business row has already been inserted;
- one failure rolls the whole transaction back, so the user ends up with
  nothing imported and an error message.

Meanwhile the same screen advertised the feature as "import a backup", and the
"clear data" action claimed to delete "all tables" while actually preserving
`users`, `schema_migrations` and `db_identity`. SQLite, on the other hand,
already performed a genuine whole-database restore. The two backends therefore
disagreed on what "restore" means.

### Decision

1. **Restore is a whole-database restore on both backends.** The business
   tables are cleared and then repopulated from the backup, in one transaction.
   Nothing is merged.
2. **Only data is restored; the schema is never touched.** The dump's
   `CREATE TABLE` / `DROP TABLE` / session statements are ignored, exactly as
   before. A dump taken on an older schema still loads, because every migration
   in this project is additive.
3. **Three system tables are preserved**, and their `INSERT` statements in the
   dump are skipped:

   | Table | Why it is preserved |
   |---|---|
   | `users` | Accounts are not rolled back. Restoring an old backup that predates your only account would leave a full database you cannot log into. |
   | `schema_migrations` | It describes the **live** schema. Since the DDL is not replayed, the live schema is still the current version, so the ledger must stay current; otherwise the next launch would re-run migrations against an already-migrated database. |
   | `db_identity` | The database's permanent identity (v13). It backs snapshot-ownership checks and server pre-flight; rolling it back makes the database look like a different one. |

4. **Every destructive operation first takes a safety copy, and aborts if that
   copy fails.** The copy is a *full* dump written as
   `pre_restore_<ts>.sql` (or `pre_clear_<ts>.sql` / `.db`) next to the regular
   backups. This mirrors the fail-closed rule already used by the pre-upgrade
   backup (#28).
5. **Safety copies are capped at 5 per kind.** The newest one is created first;
   older ones are only pruned after a new copy exists, so a failure can never
   leave the user with none.
6. **Restore is guarded in the UI by four gates:** the file must be a verified
   backup (non-empty, completion marker present), the operator must type a
   token derived from the file's timestamp, a confirmation dialog spells out the
   consequences, and the buttons are disabled while the operation runs.
7. **Both destructive operations are audited.** `audit_log` is itself cleared
   and rebuilt from the backup, so the record is written *after* the commit —
   which means the action remains visible in the restored database.

### Consequences

- Restoring a backup is now a real recovery path: the data really goes back to
  the backup moment, and the previous state is still available as a file.
- The two backends finally agree on what "restore" means, so the UI copy no
  longer has to hedge.
- A restore can no longer be used to *merge* a second database into the current
  one. That was never possible in practice (primary-key collisions), so nothing
  working is lost.
- `RestoreDatabase` and `ClearDatabase` changed signature; the new results carry
  the safety-copy path and the post-restore row counts for the user to verify.

---

## 中文

### 背景

MySQL 的「导入备份」原本是**只追加 INSERT**：逐行解析备份文件，把每条
`INSERT INTO` 放进一个事务里执行，前面不做任何删除。

结果是「恢复一份完整备份」在实践中根本做不到：

- 导出是完整 `mysqldump`，**每张表**都有 INSERT，包括 `schema_migrations`
  和 `users`；
- 目标库非空，这些行必然撞主键（`schema_migrations.version` 是主键）；
- 撞车发生在备份的**最后一张表**（按表名字母序），此时业务数据已全部插完；
- 一条失败整笔回滚，用户得到的是"什么都没导入"加一个报错。

与此同时，同一个界面把它描述成「导入备份」，而「清空数据」宣称删除
「所有表」，实际却保留了 `users`、`schema_migrations` 和 `db_identity`。
SQLite 那边则本来就是真正的整库恢复——**两个后端对「恢复」的含义不一致**。

### 决定

1. **两种后端的恢复都是整库恢复。** 先清空业务表，再用备份回灌，全程单事务。
   不做任何合并。
2. **只恢复数据，绝不改动表结构。** 备份里的 `CREATE TABLE` / `DROP TABLE` /
   会话语句一律忽略（与原行为一致）。旧版本备份照样能装载，因为本项目的
   迁移都是增量式的。
3. **保留三张系统表**，并跳过备份里对它们的 INSERT：

   | 表 | 保留原因 |
   |---|---|
   | `users` | 账号不回滚。若恢复到一份早于你唯一账号的备份，会得到一个「数据齐全但登录不进去」的库。 |
   | `schema_migrations` | 它描述的是**现场**结构。既然不重放 DDL，现场结构仍是当前版本，版本记录就必须是当前的；否则下次启动会对已迁移过的库再跑一遍迁移。 |
   | `db_identity` | 数据库的永久身份（v13）。它用于快照归属校验与服务器接入预检；回退它会让这份库被当成另一个库。 |

4. **破坏性操作前必须先生成安全副本，副本失败即中止。** 副本是**完整**
   dump，命名为 `pre_restore_<时间戳>.sql`（或 `pre_clear_<时间戳>.sql` / `.db`），
   与常规备份放在一起。这与 #28 升级前备份的 fail-closed 原则一致。
5. **每类安全副本最多保留 5 份。** 先建新的，成功后才清理旧的——这样中途
   失败也不会让用户一份副本都不剩。
6. **UI 四道闸门**：文件必须是校验通过的备份（非空 + 完成标记）；操作者必须
   输入由文件时间戳推导出的口令；确认弹窗逐条列明后果；执行期间禁用按钮。
7. **两种操作都留审计。** `audit_log` 本身会被清空并由备份重建，因此记录必须
   写在**提交之后**——这样恢复完成后仍能在库里看到这次操作。

### 影响

- 恢复备份成为一条真正可用的补救路径：数据确实回到备份时刻，而恢复前的状态
  仍以文件形式留存。
- 两个后端对「恢复」的含义终于一致，界面文案不必再含糊其辞。
- 恢复不再能用来把另一个库**合并**进当前库。实践中这本来就做不到（主键冲突），
  没有可用能力因此丢失。
- `RestoreDatabase` / `ClearDatabase` 签名已变更；新的返回值携带副本路径与恢复
  后各表行数，供用户核对。
