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
import { api, saveBlob, type OperationState } from './api'

export interface BackupFile {
  name: string
  size: number
  modified: string
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
 * 识别规则同样只认 `backup_` / `pre_restore_` / `pre_clear_` 三个前缀：
 * 无法导出时间戳时返回 null，界面据此**拒绝恢复**（这正是第一道门的一部分）。
 */
export function restoreTokenFor(fileName: string): string | null {
  const m = /^(?:backup|pre_restore|pre_clear)_(\d{8}_\d{6})\.(?:sql|db)$/.exec(fileName.trim())
  return m ? `restore ${m[1]}` : null
}
