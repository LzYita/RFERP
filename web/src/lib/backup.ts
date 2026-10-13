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
  return m ? `restore ${m[1]}` : null
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
