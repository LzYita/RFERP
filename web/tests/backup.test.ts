// 恢复确认口令的导出规则。
//
// 这是四道门里的第二道。旧版 Fyne 有一份等价实现（ui/backup.go:427-436）与
// 对应测试（ui/backup_gates_test.go），这里把那些用例搬过来，确保 Web 版不是
// 自己拍脑袋写的规则——同一份命名约定（backup_ / pre_restore / pre_clear + 时间戳）
// 两端必须一致，否则用户照着旧文档猜口令会一直失败。
import { describe, it, expect } from 'vitest'
import { classifyDirPick, isDirPickUnavailable, restoreTokenFor } from '../src/lib/backup'
import { ApiError } from '../src/lib/api'

describe('restoreTokenFor', () => {
  it('从三种合法前缀里导出时间戳', () => {
    expect(restoreTokenFor('backup_20260925_101530.sql')).toBe('restore 20260925_101530')
    expect(restoreTokenFor('pre_restore_20260929_110000.sql')).toBe('restore 20260929_110000')
    expect(restoreTokenFor('pre_clear_20260929_105500.sql')).toBe('restore 20260929_105500')
  })

  it('SQLite 快照（.db）同样可用', () => {
    expect(restoreTokenFor('pre_restore_20260929_110000.db')).toBe('restore 20260929_110000')
  })

  it('首尾空白不影响识别', () => {
    expect(restoreTokenFor('  backup_20260925_101530.sql  ')).toBe('restore 20260925_101530')
  })

  // 无法导出时间戳必须返回 null 而不是猜一个：界面据此拒绝恢复。
  // 猜出来的话，用户会看到一句自己无从判断对错的提示。
  it('无法识别的时间戳返回 null（界面据此拒绝恢复）', () => {
    expect(restoreTokenFor('backup.sql')).toBeNull()
    expect(restoreTokenFor('backup_手动拷贝.sql')).toBeNull()
    expect(restoreTokenFor('backup_20260925.sql')).toBeNull()
    expect(restoreTokenFor('backup_20260925_1015.sql')).toBeNull()
    expect(restoreTokenFor('random_20260925_101530.sql')).toBeNull()
    expect(restoreTokenFor('20260925_101530.sql')).toBeNull()
    expect(restoreTokenFor('')).toBeNull()
  })

  it('不是备份的文件名不给令牌', () => {
    expect(restoreTokenFor('notes.txt')).toBeNull()
    expect(restoreTokenFor('backup_20260925_101530.zip')).toBeNull()
    expect(restoreTokenFor('backup_20260925_101530.sql.bak')).toBeNull()
  })

  // 令牌随文件变化：换一个文件，令牌必须跟着变，否则这道门就挡不住"恢复错文件"。
  it('不同文件得到不同令牌', () => {
    const a = restoreTokenFor('backup_20260925_101530.sql')
    const b = restoreTokenFor('backup_20260101_000000.sql')
    expect(a).not.toBe(b)
  })
})

// 原生目录框的失败处理（方案丙）。三种结果的含义互不相同，混在一起最容易写错：
// 取消不是错、没有窗口是常态、路径已选才是要落盘的那一种。
describe('classifyDirPick', () => {
  it('用户取消 → cancelled（不是错误，界面保持原状）', () => {
    expect(classifyDirPick({ dir: '' })).toEqual({ kind: 'cancelled' })
  })

  it('选中了 → chosen，带上路径', () => {
    expect(classifyDirPick({ dir: 'D:\\仓库数据' })).toEqual({ kind: 'chosen', dir: 'D:\\仓库数据' })
  })
})

describe('isDirPickUnavailable', () => {
  // 501：没有宿主窗口（cmd/server、浏览器）。500：弹框本身失败。
  // 这两种都该降级成手填，而不是把错误甩给用户。
  it('501 与 500 都算"弹不了"，应回退手填', () => {
    expect(isDirPickUnavailable(new ApiError(501, '当前服务无法弹出目录选择框，请手动输入路径'))).toBe(true)
    expect(isDirPickUnavailable(new ApiError(500, '目录选择框无法打开，请手动输入路径'))).toBe(true)
  })

  it('403 不算弹不了——那是权限问题，不该诱导向手填', () => {
    expect(isDirPickUnavailable(new ApiError(403, 'forbidden'))).toBe(false)
  })

  it('非 ApiError（网络断、解析失败）不降级', () => {
    expect(isDirPickUnavailable(new Error('network down'))).toBe(false)
  })
})
