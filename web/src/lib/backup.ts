/**
 * 备份 / 恢复 / 清空 / 数据目录的接口封装。
 *
 * 端点与 internal/api/backup.go 一一对应。两处形状上的刻意选择：
 *
 * 1. **恢复只接受文件名**。服务端在备份目录内解析并拒绝穿越，这里不提供任何
 *    拼路径的能力——前端如果能传绝对路径，就等于把目录穿越的决定权交给页面。
 * 2. **拿不到绝对路径**。服务端只回文件名（§4.5 缺口 5：回路径等于泄露服务器
 *    目录布局）。唯一的例外是数据目录本身：用户要知道自己把备份放在哪，
 *    而 GET /api/datadir 已鉴权且要求 backup 模块读权限。
 */
import { ApiError, api, saveBlob, type OperationState } from './api'

export interface BackupFile {
  name: string
  size: number
  modified: string

  /**
   * 物理分类：manual（手动）/ auto（自动）/ safety（操作前与升级前）/ legacy（旧布局）。
   *
   * 下拉框按它分组。这不是装饰：同一时刻的 .sql / .upgraded.sql / .final.sql
   * 三个文件光看名字分不出好坏，而它们恰恰是回滚结果不同的那几份
   * （覆盖度不同，见 missingTables）。
   */
  category?: BackupCategory
  /** true = 还在 备份\ 根下（旧布局，没被归类迁移）。 */
  legacy?: boolean

  // ── 预检结论（服务端算，见 internal/api/backup.go handleListBackups）──
  // 恢复的实现是「先 DELETE 全部业务表，再回灌 INSERT」，所以一份早于某张表
  // 引入的备份，恢复后那张表会被清空且无处回填。这些字段把这件事提前说出来，
  // 而不是等用户输完确认口令、点完恢复之后才发现。

  /** false = 预检只读到文件开头、没读到结尾，覆盖度**未知**。 */
  schemaKnown?: boolean
  /** 当前业务表里、这份备份 schema 中没有的表。schemaKnown 为 false 时无意义。 */
  missingTables?: string[]
  /** false = 不能作为恢复来源（典型：非 UTF-8 编码）。 */
  restorable?: boolean
  unusableReason?: string
}

export type BackupCategory = 'manual' | 'auto' | 'safety' | 'legacy'

/**
 * 分类的显示名与顺序。
 *
 * 顺序即优先级：先看自动备份（最近的现场），再看操作前副本（最近的回退点），
 * 最后才是用户手动留的。与服务端 paths.BackupCategories() 一致。
 */
export const CATEGORY_LABELS: Record<BackupCategory, string> = {
  auto: '自动备份',
  safety: '操作前副本',
  manual: '手动备份',
  legacy: '旧布局（未归类）',
}

export const CATEGORY_ORDER: BackupCategory[] = ['auto', 'safety', 'manual', 'legacy']

/**
 * 按分类分组，供下拉框的 <optgroup> 使用。
 *
 * 组内保持传入顺序（服务端已按新→旧排好），空分类**不**出现——
 * 界面上留一个永远空的分组只会让人以为那里坏了。
 */
export function groupByCategory(
  files: BackupFile[],
): { category: BackupCategory; label: string; files: BackupFile[] }[] {
  const out: { category: BackupCategory; label: string; files: BackupFile[] }[] = []
  for (const cat of CATEGORY_ORDER) {
    const fs = files.filter((f) => (f.category ?? 'legacy') === cat)
    if (fs.length > 0) out.push({ category: cat, label: CATEGORY_LABELS[cat], files: fs })
  }
  // 服务端将来新增分类时，别把文件悄悄藏起来：未识别的归到最后一组。
  const known = new Set<string>(CATEGORY_ORDER)
  const rest = files.filter((f) => !known.has(f.category ?? 'legacy'))
  if (rest.length > 0) out.push({ category: 'legacy', label: CATEGORY_LABELS.legacy, files: rest })
  return out
}

/** 自动定期备份的设置与状态。 */
export interface AutoBackupSchedule {
  enabled: boolean
  days: number
  last?: string
  nextDue?: string
  count: number
  keep: number
  reason?: string
}

/** 允许的间隔（天）；0 = 关闭。与服务端 config.AllowedAutoBackupDays 一致。 */
export const AUTO_BACKUP_DAY_OPTIONS = [0, 1, 2, 3, 5, 7, 30]

export function autoBackupDayLabel(days: number): string {
  if (days === 0) return '关闭'
  if (days === 1) return '每天'
  if (days === 30) return '每 30 天'
  return `每 ${days} 天`
}

/**
 * 这份备份恢复后会丢哪些表的数据。
 *
 * 返回空数组有两种含义，界面的措辞必须区分：
 *   - 覆盖完整
 *   - **无法确认**（schemaKnown=false，预检只读了文件开头）
 * 所以调用方要看 schemaKnown，别只看这个数组的长度。
 */
export function tablesLostOnRestore(f: BackupFile): string[] {
  if (f.schemaKnown === false) return []
  return f.missingTables ?? []
}

export interface DataDirInfo {
  dataDir: string
  backupDir: string
}

export interface RestoreResult {
  requiresRestart?: boolean
  statements: number
  tableCounts?: Record<string, number>
  /** 操作前副本的**文件名**（不是路径） */
  preRestore?: string
}

export interface ClearResult {
  cleared?: string[]
  /** 清空前副本的**文件名** */
  preClear?: string
}

/** 破坏性操作失败时抛出，带三分类结局。 */
export interface OutcomeError {
  message: string
  state: OperationState
}

export const backupApi = {
  listFiles: () => api.get<{ files: BackupFile[] }>('/api/backup/files'),

  dataDir: () => api.get<DataDirInfo>('/api/datadir'),

  setDataDir: (dir: string) => api.post<DataDirInfo>('/api/datadir', { dir }),

  /**
   * 请求宿主弹**原生**目录选择框。
   *
   * 前端刻意不认识 Wails：原生框只能由宿主进程弹出，所以能力从宿主注入、
   * 服务端裁决，这里只是一个普通 HTTP 请求（方案丙）。因此这个方法在
   * 桌面形态之外必然失败，调用方要负责降级：
   *   - 用户取消 → 返回空串（正常结果，不算错）
   *   - 没有窗口 → 501，文案里带「手动输入」，此时应回退到文本输入框
   */
  pickDataDir: () => api.post<{ dir: string }>('/api/datadir/pick'),

  /** 生成备份并下载到本机。服务端以文件流返回，因此这里拿到的是字节。 */
  downloadBackup: async () => {
    const { blob, filename } = await api.download('/api/backup', undefined, 'backup.sql')
    saveBlob(blob, filename)
    return filename
  },

  restore: (file: string) => api.post<RestoreResult>('/api/restore', { file }),

  /** confirm 必须是 'drop' 逐字匹配；服务端独立复核，绕不过去。 */
  clear: () => api.post<ClearResult>('/api/clear', { confirm: CLEAR_CONFIRM_TOKEN }),

  /**
   * 导出全部数据。
   *
   * 返回**文件夹路径 + 文件名列表**，不返回文件内容：全量导出是**多份** CSV
   * 落在数据目录里，打包成一个下载反而让人多一道"解压再找"。
   * （此前这里调 api.download，把服务端回的 JSON 当成 all_data.csv 下载了，
   * 打开是一段含服务器路径的 JSON —— 既没用又把目录布局泄了出去。）
   */
  exportAll: () => api.post<{ dir: string; files: string[] }>('/api/export/all'),

  /** 在资源管理器里打开导出目录（本机操作，远程形态会被闸门挡下）。 */
  openExportFolder: (dir: string) => api.post<{ dir: string }>('/api/export/all/open', { dir }),

  /**
   * 导出审计日志到**数据目录**，返回文件夹与文件名。
   *
   * 不返回文件内容：与全量导出一致，文件已经在磁盘上了，再往浏览器的「下载」
   * 里塞一份只会多出一个用户认不出的文件，还每次导出多一份重复数据。
   * 确实需要字节的调用方（远程形态）另有 /export/audit/download。
   */
  exportAudit: (range: { start: string; end: string }) =>
    api.post<{ dir: string; files: string[]; count: number }>('/api/export/audit', range),

  autoSchedule: () => api.get<AutoBackupSchedule>('/api/backup/autoschedule'),

  /**
   * 设置自动备份间隔。days=0 关闭。
   *
   * 非法值由服务端拒绝并回 400 —— 前端**不**静默改成邻近的合法值：
   * 用户以为每 4 天有一份、实际每 5 天，而真正需要那份备份的时刻正好落在空档里。
   */
  setAutoSchedule: (days: number) => api.post<AutoBackupSchedule>('/api/backup/autoschedule', { days }),
}

/** 清空确认口令。与服务端 internal/api/backup.go 的 ClearConfirmToken 一致。 */
export const CLEAR_CONFIRM_TOKEN = 'drop'

/**
 * 恢复确认口令。
 *
 * 形如 `restore 20260925_101530`，时间戳取自**所选文件名**——换一个文件，令牌
 * 就得跟着换。这是四道门里的第二道（见 §Round 5）。
 *
 * 与 Fyne 版的**有意差异**：那边选中文件时会把口令自动预填（ui/backup.go:439-450），
 * 本计划要求「不可预填、不可复制粘贴」——预填 + 可粘贴等于把第二道门降级成
 * 一个勾选框，用户不需要读懂任何东西就能划过去。
 *
 * 识别规则只认 `backup_` / `pre_restore` / `pre_clear` 三个前缀：
 * 无法导出时间戳时返回 null，界面据此**拒绝恢复**（这正是第一道门的一部分）。
 *
 * ── 中间的可选点分段（`.final` / `.upgraded`）是为真实数据补的 ──
 * D:\仓库数据 里同时躺着 backup_20260924_170830.sql、.final.sql 与 .upgraded.sql：
 * 本程序自己只写 `%s_%s.sql`，后两个是迁移期间手工改名 / 外部工具留下的，同一时刻
 * 因此有三个世代并存。早先只认结尾 `.sql`，于是旧备份**一个都恢复不了**。
 * 限定为一个点分段、字符集 [A-Za-z0-9_-]，结尾仍须 .sql/.db（`xxx.sql.bak` 照拒）。
 *
 * ⚠️ 这是服务端 internal/api/backup.go 的 restoreFilePattern 的**镜像**，不是副本之外
 * 的独立规则；backup_contract_test.go 用真实文件名锁住两者一致。改这里必须同步改那边。
 */
export function restoreTokenFor(fileName: string): string | null {
  const m = /^(?:backup|pre_restore|pre_clear)_(\d{8}_\d{6})(?:\.[A-Za-z0-9_-]+)?\.(?:sql|db)$/.exec(
    fileName.trim(),
  )
  if (!m) return null
  // 只取**时分秒**，去掉日期：`restore 172919` 而不是 `restore 20260924_172919`。
  //
  // 为什么能砍掉日期又不削弱这道门：这道门的作用是让人停下来读一遍后果，
  // 不是校验文件身份——同一份目录里不会有两份备份落在同一秒，而选错文件
  // 本来就由下拉框里的文件名与时间戳负责区分。原来那串要打 23 个字符，
  // 而它换来的只是让人多点几下输入法；输入摩擦越大，这道门越容易被
  // 无意识地划过去。
  return `restore ${m[1].slice(9)}`
}

/**
 * 选目录失败后的处理方式。
 *
 * 原生框只能由宿主弹出（方案丙），所以这条路径**必然**会遇到失败。把三种结果
 * 各自对应什么动作定成纯函数，是因为它们含义互不相同、混在一起最容易写错：
 * 取消不是错（静默保持原状），没有窗口是常态（降级手填，不是错误）。
 */
export type DirPickOutcome =
  | { kind: 'cancelled' }
  | { kind: 'chosen'; dir: string }
  /** 该形态弹不出框（501）或弹框失败（500）→ 界面亮出手填输入框。 */
  | { kind: 'fallback'; reason: string }

/**
 * 把 pickDataDir 的结果/异常归一成一个界面动作。
 *
 * 取消返回空串、而不是抛错，是后端有意为之（见 internal/api/backup.go
 * handlePickDataDir）：用户按了「取消」不是故障，不该弹红色错误。
 */
export function classifyDirPick(result: { dir: string }): DirPickOutcome {
  return result.dir ? { kind: 'chosen', dir: result.dir } : { kind: 'cancelled' }
}

/** 无宿主窗口 / 弹框失败时，界面据此回退到文本输入并说明原因。 */
export function isDirPickUnavailable(e: unknown): boolean {
  return e instanceof ApiError && (e.status === 501 || e.status === 500)
}
