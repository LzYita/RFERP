# RFERP 客户端安全升级指南（供用户端 Agent 执行）

> 本文件面向**运行在用户机器上的 Agent**：目标是"对比本机旧版本与当前最新版本，并在保证数据安全的前提下完成升级"。
> 仓库：<https://github.com/LzYita/RFERP> ｜ 面向版本：截至 v1.2.2 及之后（含 #40 统计口径修复）

---

## 0. 铁律（先读，违反即事故）

1. **先备份，后动手。** 任何会改写数据库结构的动作之前，必须有一份**已校验完整**的 SQL 备份。
2. **只读优先。** 第 1 步全部为只读盘点，不得修改任何文件、不得启动/关闭程序。
3. **拿不准就停。** 遇到本文件未覆盖的情况，**停止并向人类确认**，不要自行猜测。
4. **禁止破坏性 SQL。** Agent 不得自行执行 `DROP` / `TRUNCATE` / 清空数据 / 删除 `schema_migrations` 或 `users`。
5. **禁止降级。** 程序没有"库版本高于程序则拒绝启动"的保护，装回旧版可能破坏数据。
6. **不得把业务数据、密码、连接串写入任何对外文本**（issue / PR / 日志摘要 / 提交信息）。
7. **不静默切库。** 默认存储是 MySQL；不要把 `storage` 改成 `sqlite`，也不要新建 SQLite 库。
8. 全过程**不修改 `config.json`**（除第 3 步在人类确认后补 `mysqldumpPath`）。

---

## 1. 第 1 步：只读盘点

### 1.1 定位程序与当前版本

按以下顺序取版本号，任一成功即可：

| 优先级 | 方法 | 说明 |
|---|---|---|
| 1 | 启动日志 | `<DataDir>\日志\rferp.log` 中形如 `RFERP 1.2.2 starting` 的行；`DataDir` 见 1.2 |
| 2 | 窗口标题 | `RFERP-仁风仓库管理系统 v<版本号>` |
| 3 | 卸载注册表 | `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\{8DDCEC9B-7A13-408B-956F-01DC77151125}` 的 `InstallLocation` / `DisplayVersion` |

```powershell
# 程序安装位置（已安装版）
$key = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\{8DDCEC9B-7A13-408B-956F-01DC77151125}'
if (Test-Path $key) { Get-ItemProperty $key | Select-Object DisplayVersion, InstallLocation }
```

**无版本号 = 极老版本**（早于配置文件机制）：它把数据库连接**硬编码在程序里**，没有 `config.json`，也没有自动更新。后续步骤按"极老版本"分支处理。

### 1.2 定位 `config.json`（配置查找顺序）

程序按此顺序查找，第一个可解析者生效：

1. `<exe 同目录>\config.json`
2. `<当前工作目录>\config.json`
3. `%AppData%\RFERP\config.json`（`os.UserConfigDir()`）

```powershell
$cands = @(
  (Join-Path $env:LOCALAPPDATA 'Programs\RFERP\config.json'),
  (Join-Path $env:APPDATA 'RFERP\config.json'),
  (Join-Path (Get-Location) 'config.json')
)
$cands | ForEach-Object { "{0}  {1}" -f (Test-Path $_), $_ }
```

- **存在且有 `host`/`user`/`dbname`** → 正常用户，照常升级。
- **完全不存在** → 极老版本。程序会退回默认值：`127.0.0.1:3306` / 用户 `root` / **空密码** / 库 `appliancedb`，且**不会**弹运行模式选择。
  - 旧版硬编码的连接**恰好是这套默认值** → 可直接启动 ✅
  - **不一致**（主机/密码/库名不同）→ 首次启动会弹**配置界面**，在那里重新填写即可；该界面会自动探测本机 MySQL 并填好备份工具路径 ✅
  - ⚠️ **小坑**：若旧版库名不是 `appliancedb`，而本机恰好存在一个空的 `appliancedb`，程序会连上它并初始化空库——**看起来像"数据没了"，实际数据在另一个库**。此时应停止并向人类确认正确库名。

### 1.3 读取数据库连接并做只读自检

密码字段是 `password_enc`（Base64 + DPAPI CurrentUser 加密），按需解密：

```powershell
Add-Type -AssemblyName System.Security
$cfg = Get-Content '<config.json 路径>' -Raw | ConvertFrom-Json
$pwd = $null
if ($cfg.password_enc) {
  $raw = [Convert]::FromBase64String($cfg.password_enc)
  $pwd = [System.Text.Encoding]::UTF8.GetString(
    [System.Security.Cryptography.ProtectedData]::Unprotect(
      $raw, $null, [System.Security.Cryptography.DataProtectionScope]::CurrentUser))
}
$env:MYSQL_PWD = $pwd
```

> 注：DPAPI 只能由**同一 Windows 用户**解密。解密失败时不要猜测密码，向人类索要。

只读自检（不写任何数据）：

```sql
SELECT MAX(version) AS db_version FROM schema_migrations;   -- 库结构版本
SELECT COUNT(*) FROM products;      -- 产品数
SELECT COUNT(*) FROM parts;         -- 零件数
SELECT COUNT(*) FROM users;         -- 用户数
SELECT COUNT(*) FROM product_batches;  -- 批次数
SELECT COUNT(*) FROM audit_log;     -- 审计条数
```

把上述**基线数字**记录下来，第 3 步要逐项比对。

**库结构版本对照**（用于判断"是否需要迁移"）：

| `schema_migrations` 最大版本 | 对应程序版本 | 是否有待执行迁移 |
|---|---|---|
| ≤ 11 | v1.1.0 及更早 | **有**（会触发升级前备份） |
| 12 或 13 | v1.2.0 ~ v1.2.2 | 视是否已记录该版本而定 |
| 13（已记录） | v1.2.2 及之后 | **无**（不会触发备份） |

### 1.4 查询当前最新版本

程序自身使用同一个清单文件：

```
https://ghfast.top/https://github.com/LzYita/RFERP/releases/latest/download/releases.json
# 直连回退：https://github.com/LzYita/RFERP/releases/latest/download/releases.json
```

```powershell
$mf = Invoke-RestMethod -Uri 'https://ghfast.top/https://github.com/LzYita/RFERP/releases/latest/download/releases.json' -TimeoutSec 60
$mf.version; $mf.sha256; $mf.url; $mf.size
```

同时取发行页资产列表（安装器 / 压缩包）：

```powershell
$r = Invoke-RestMethod -Uri 'https://api.github.com/repos/LzYita/RFERP/releases/latest' -Headers @{ 'User-Agent'='rferp-upgrade-agent' }
$r.tag_name; $r.assets | Select-Object name, size, browser_download_url
```

### 1.5 比对结论

用 `[version]` 语义比较（`1.2.10 > 1.2.9`），不要按字符串比：

```powershell
$cur = [version]'0.0.0'   # 填 1.1 节取得的版本号；取不到填 0.0.0
$lat = [version]$mf.version
if ($lat -gt $cur) { "需要升级: $cur -> $lat" } elseif ($lat -eq $cur) { "已是最新，无需操作" } else { "本机版本高于最新发布版，请向人类确认" }
```

| 结论 | 动作 |
|---|---|
| 已是最新 | 停止，向人类报告，无需任何改动 |
| 需要升级 | 进入第 2 步 |
| 本机版本高于最新 | **停止**并报告（可能是内部/预发布版本，不要降级） |

---

## 2. 第 2 步：升级前安全检查

### 2.1 关闭程序

程序是单实例的，重复启动只会提示"RFERP 已在运行，请勿重复启动"。升级前请人类**正常退出程序**（确认任务管理器中无 `RFERP.exe`）。

### 2.2 确认 `mysqldump` 可用（**最关键的一步**）

v1.2.2 起：库非空且有升级项时，程序会**先做一份校验过的备份，备份失败就中止升级**，并弹「RFERP 数据库升级失败」。备份工具取用顺序：

1. `config.json` 里的 `mysqldumpPath`
2. 否则 **PATH 中的 `mysqldump`**

```powershell
$c = Get-Command mysqldump -ErrorAction SilentlyContinue
if ($c) { "OK: " + $c.Source } else { "缺失：MySQL 的 bin 目录未加入 PATH，也没有配置 mysqldumpPath" }
```

- **缺失时**：优先请人类确认，然后二选一（**需人类确认后执行**）
  - 把 MySQL 的 `bin` 目录加入 `PATH`；或
  - 在 `config.json` 增加一行（**仅当人类确认时**）：
    ```json
    "mysqldumpPath": "D:\\MySQL\\MySQL Server 8.0\\bin\\mysqldump.exe"
    ```
- `mysqldump` 缺失会让升级在最后一步被拦下（数据不会坏，但程序起不来），所以**必须在升级前解决**。

### 2.3 手工做一份 SQL 备份（与程序的双保险）

即使程序会自动备份，也建议人工先做一份——程序那份是在迁移开始前生成的，人工这份由你掌控并立即校验。

```powershell
$stamp = Get-Date -Format 'yyyyMMdd_HHmmss'
$out   = "$env:USERPROFILE\Desktop\rferp_preupgrade_$stamp.sql"
& mysqldump --default-character-set=utf8mb4 --single-transaction --routines `
    -h <host> -P <port> -u <user> -p'<password>' <dbname> --result-file="$out"
```

校验（两条都要满足）：

```powershell
(Get-Item $out).Length -gt 0                                   # 非空
Select-String -Path $out -Pattern 'Dump completed' -SimpleMatch  # 有 mysqldump 完成标记
```

任一不满足 → **停止升级**，向人类报告。mysqldump 在"退出码为 0"时也可能留下被截断的文件（例如磁盘写满），所以这两项检查缺一不可。

> PowerShell 5.1 用 `>` 重定向会写成 GBK，导致中文乱码。**必须用 `--result-file=`**，不要用 `>`。

### 2.4 记录基线

把 1.3 的基线数字再确认一次并记录（含时间戳）。这是升级后唯一的"数据没丢"判据。

---

## 3. 第 3 步：执行升级

### 3.1 下载并校验

```powershell
$zip = "$env:TEMP\RFERP-$($mf.version).zip"
Invoke-WebRequest -Uri $mf.url -OutFile $zip -TimeoutSec 600
(Get-FileHash $zip -Algorithm SHA256).Hash.ToLower() -eq $mf.sha256   # 必须为 True
```

不一致 → **停止**并重新下载。

### 3.2 两条升级路径（择一）

**A. 安装器（推荐）**：`Setup-RFERP-<版本>.exe`

- 安装目录：`%LocalAppData%\Programs\RFERP`，**无需管理员权限**
- 安装器**不会**搬运旧版 `config.json`，也**不会卸载**其它目录里的旧版程序
- 装完后请人类从**开始菜单/新桌面快捷方式**启动

**B. 免安装压缩包**（`RFERP-<版本>.zip`）：解压到**原目录**，覆盖 `RFERP.exe`。
适合旧版本来就是"随手放一个 exe"的场景，且能保留同目录的 `config.json`。

⚠️ 两条路径都**不会**删除旧版 exe。升级验证通过前，**不要**删除旧目录。

### 3.3 启动

正常启动即可。首次启动会自动执行迁移：

- `config.json` 不存在时，程序自动以"本机 + MySQL"模式进入，不会弹模式选择
- 库需要迁移时，会先生成并校验备份，再逐条应用

---

## 4. 第 4 步：升级后验证（逐项确认）

### 4.1 窗口标题

标题应显示新版本号：`RFERP-仁风仓库管理系统 v<新版本>`。

### 4.2 日志核验

`<DataDir>\日志\rferp.log` 中应出现：

| 日志关键字 | 含义 |
|---|---|
| `RFERP <新版本> starting` | 新版本已启动 |
| `migrate: pre-upgrade backup saved: <路径>` | 升级前备份成功（本次有迁移时） |
| `migrate: migration applied: [...]` | 迁移已应用 |
| `升级前备份失败，已中止迁移` | ❌ 升级被中止，**库未被改动**，见 5.1 |
| `升级前快照失败，已中止迁移` | ❌ SQLite 路径失败（本指南不涉及） |

```powershell
$log = '<DataDir>\日志\rferp.log'
Get-Content $log -Tail 60 -Encoding UTF8 | Select-String -Pattern 'starting|migration|backup|升级前'
```

### 4.3 数据一致性（与 2.4 的基线逐项比对）

```sql
SELECT COUNT(*) FROM products;      -- 必须与基线一致
SELECT COUNT(*) FROM parts;         -- 必须与基线一致
SELECT COUNT(*) FROM users;         -- 必须与基线一致
SELECT COUNT(*) FROM product_batches;
SELECT MAX(version) FROM schema_migrations;   -- 应已推进到最新
```

- `products` / `parts` / `users` **必须完全一致**。
- `audit_log` 只增不减（迁移本身会写审计）。
- 任一项不一致 → **立即停止**并向人类报告，不要继续操作。

### 4.4 冒烟检查

- 登录正常（沿用原有账号，无需重建用户）
- 零件/产品列表可打开、数量正确
- 批次列表可打开
- 「操作记录」页可正常打开（v1.2.2 修复的闪退）
- 「统计」页：撤销过的批次**不再计入**出库/入库（v1.2.2 之后的 #40 修复）
- 启动时会自动检查更新；此后**不再需要**手动下载新版

---

## 5. 失败处置

### 5.1 弹「RFERP 数据库升级失败」

- 含义：升级前备份或迁移步骤失败，**迁移已中止，数据库未被改动**（这是设计行为：宁可停下也不留半升级的库）
- 排查顺序：
  1. 看日志里 `升级前备份失败` 后面的具体错误；
  2. 若是 `mysqldump` 找不到 → 回 2.2 解决后重试；
  3. 若是权限/磁盘满 → 清理或换目录后重试
- 确认 `schema_migrations` 未推进后再重试：
  ```sql
  SELECT MAX(version) FROM schema_migrations;
  ```

### 5.2 备份文件缺失或未通过校验

**停止任何升级动作**，向人类报告。绝不能在没有可用备份的情况下继续。

### 5.3 需要从备份回滚

⚠️ **必须由人类确认后执行。** Agent 只负责说明情况与提供备份文件路径，**不自行执行恢复或清空**。

自 v1.2.3 起，两种存储的「导入备份」都是**整库恢复**（当前数据会被完全覆盖，不做合并）：

- 恢复前会自动生成 `pre_restore_<时间戳>.sql` / `.db` 副本，位于备份目录，可用于再次回退；
- 恢复在单个事务内完成，中途失败会整体回滚，数据库保持恢复前状态；
- `users`、`schema_migrations`、`db_identity` 三张系统表**不会被覆盖**（账号保持现状）；
- **不会**改变表结构：备份里的建表/改表语句一律忽略。

UI 需要三步确认：选择备份文件 → 输入 `restore <时间戳>` 确认口令 → 在弹窗中确认后果。

> 恢复前请先确认「你记得当前账号的密码」——账号不会回滚，但如果你打算连旧备份里的账号一起回退，本版本不支持。

历史版本（≤ v1.2.2）曾存在已知限制：MySQL 导入为追加式，完整备份无法导入非空库。遇到这类旧版本时，正确做法是**导入到一个新库**并让程序指向它。

### 5.4 装了新版本但连不上库

程序会弹配置界面（`ShowSetup`），在其中重新填写主机/端口/用户/密码/库名；该界面会自动探测本机 MySQL 并填好 `mysqldumpPath`。

⚠️ 若同时存在多个同名数据库，**先确认库名**再连接，避免连到空库（见 1.2 的小坑）。

---

## 6. 报告模板（Agent 完成后输出给人类）

```
本机版本：<旧版本>        最新版本：<新版本>
库结构版本：<旧> → <新>   存储：MySQL / SQLite（<连接的主机:端口/库名>）
升级前备份：<路径>（<大小>，完整性校验：通过 / 未通过）
数据校验：产品 <基线→现在>  零件 <基线→现在>  用户 <基线→现在>  批次 <基线→现在>
验证结果：窗口标题版本 / 日志迁移行 / 冒烟检查 / 自动更新
遗留事项：<如「旧版 exe 未删除，等确认后再清理」；无则写「无」>
```

---

## 附录 A：关键事实速查

| 项 | 值 |
|---|---|
| 默认连接 | `127.0.0.1:3306`，用户 `root`，**空密码**，库 `appliancedb` |
| 默认存储 | MySQL（`storage` 为空时按 MySQL 处理，不静默切 SQLite） |
| 配置文件查找 | exe 同目录 → 当前工作目录 → `%AppData%\RFERP\config.json` |
| 密码存储 | `password_enc` = Base64 + DPAPI(CurrentUser)，仅同一 Windows 用户可解 |
| 安装目录 | `%LocalAppData%\Programs\RFERP`（免管理员） |
| 卸载注册表 | `HKCU\...\Uninstall\{8DDCEC9B-7A13-408B-956F-01DC77151125}` |
| 日志 | `<DataDir>\日志\rferp.log`（`DataDir` 取自 `config.json` 的 `dataDir`，缺省用用户主目录） |
| 备份目录 | `<DataDir>\备份` |
| 备份工具 | `config.mysqldumpPath` → 否则 PATH 中的 `mysqldump` |
| 备份校验 | 文件非空 **且** 含 mysqldump 的 `Dump completed` 标记 |
| 单实例 | 重复启动提示"RFERP 已在运行，请勿重复启动" |
| 更新清单字段 | `version` / `url` / `size` / `sha256` / `notes` / `publishedAt` / `sig` |
| 升级触发备份的条件 | 库**非空** 且 **有** 待执行迁移/步骤 |

## 附录 B：命令模板

```powershell
# 1) 取最新版本与下载地址
$mf = Invoke-RestMethod 'https://ghfast.top/https://github.com/LzYita/RFERP/releases/latest/download/releases.json' -TimeoutSec 60
$mf.version; $mf.sha256; $mf.url

# 2) 查 mysqldump
Get-Command mysqldump -ErrorAction SilentlyContinue | Select-Object Source

# 3) 手工备份并校验
& mysqldump --default-character-set=utf8mb4 --single-transaction `
    -h <host> -P <port> -u <user> -p'<pwd>' <db> --result-file="$env:TEMP\pre.sql"
(Get-Item "$env:TEMP\pre.sql").Length -gt 0
Select-String -Path "$env:TEMP\pre.sql" -Pattern 'Dump completed' -SimpleMatch

# 4) 下载校验
$zip = "$env:TEMP\RFERP-$($mf.version).zip"
Invoke-WebRequest $mf.url -OutFile $zip -TimeoutSec 600
(Get-FileHash $zip -Algorithm SHA256).Hash.ToLower() -eq $mf.sha256

# 5) 升级后读日志
Get-Content '<DataDir>\日志\rferp.log' -Tail 60 -Encoding UTF8
```

## 附录 C：常见陷阱

1. **PowerShell 5.1 重定向**：`>` 写成 GBK，中文乱码 → 导出 SQL 一律用 `--result-file=`。
2. **中文路径**：部分 MySQL 客户端工具在中文路径下会失败 → 必要时把 SQL 复制到纯 ASCII 临时路径。
3. **`.bat` 是纯 ASCII**：不要往批处理里加中文注释。
4. **`config.json` 里不要留明文密码**：程序会把明文密码迁移为 `password_enc`。
5. **不要手改 `schema_migrations`**：会跳过迁移，导致结构与程序版本不匹配。
6. **不要用 `SELECT ... INTO OUTFILE`** 代替 `mysqldump`：前者不保证事务一致性与完整性。
7. **不要在程序运行中替换 exe**：先正常退出。
