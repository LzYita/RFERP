// 恢复确认口令的导出规则。
//
// 这是四道门里的第二道。旧版 Fyne 有一份等价实现（ui/backup.go:427-436）与
// 对应测试（ui/backup_gates_test.go），这里把那些用例搬过来，确保 Web 版不是
// 自己拍脑袋写的规则——同一份命名约定（backup_ / pre_restore / pre_clear + 时间戳）
// 两端必须一致，否则用户照着旧文档猜口令会一直失败。
import { describe, it, expect } from 'vitest'
import {
  AUTO_BACKUP_DAY_OPTIONS,
  autoBackupDayLabel,
  classifyDirPick,
  groupByCategory,
  isDirPickUnavailable,
  restoreTokenFor,
  tablesLostOnRestore,
  type BackupCategory,
  type BackupFile,
} from '../src/lib/backup'
import { ApiError } from '../src/lib/api'

describe('restoreTokenFor', () => {
  it('从三种合法前缀里导出时间戳', () => {
    expect(restoreTokenFor('backup_20260925_101530.sql')).toBe('restore 20260925_101530')
    expect(restoreTokenFor('pre_restore_20260929_110000.sql')).toBe('restore 20260929_110000')
    expect(restoreTokenFor('pre_clear_20260929_105500.sql')).toBe('restore 20260929_105500')
  })

  // 真实数据里就是这些名字。D:\仓库数据 中同一时刻 backup_20260924_170830 有三个
  // 世代：.sql / .upgraded.sql / .final.sql（后两个是迁移期间手工改名留下的，
  // 本程序只写 `%s_%s.sql`）。早先只认结尾 `.sql`，于是旧备份一个都恢复不了，
  // 界面只回一句「请选择本应用生成的文件」——那是在教用户做软件该做的事。
  // 三个世代导出**同一个**口令：口令确认的是"你读过后果"，不是"你选了哪个版本"。
  it('D:\\仓库数据 里的真实文件名都能识别，且同一时刻口令一致', () => {
    const three = [
      'backup_20260924_170830.sql',
      'backup_20260924_170830.upgraded.sql',
      'backup_20260924_170830.final.sql',
    ]
    three.forEach((n) => expect(restoreTokenFor(n)).toBe('restore 20260924_170830'))
    expect(restoreTokenFor('backup_20260721_154617.sql')).toBe('restore 20260721_154617')
    expect(restoreTokenFor('pre_restore_20260924_172919.sql')).toBe('restore 20260924_172919')
  })

  it('qualifier 之后仍可用 SQLite 快照（.db）', () => {
    expect(restoreTokenFor('backup_20260924_170830.final.db')).toBe('restore 20260924_170830')
  })

  // qualifier 只允许**一个**点分段、字符集 [A-Za-z0-9_-]。放宽到 .* 会把
  // `..`、`a.b` 这类一起放进来，而结尾仍须 .sql/.db，所以 .sql.bak 照拒。
  it('多段或含空格的 qualifier 一律拒绝', () => {
    expect(restoreTokenFor('backup_20260925_101530.a.b.sql')).toBeNull()
    expect(restoreTokenFor('backup_20260925_101530.a b.sql')).toBeNull()
    expect(restoreTokenFor('backup_20260925_101530..sql')).toBeNull()
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

  // 这些是备份目录里真实存在的**别的**文件，不能因为"在备份目录里"就能当备份恢复。
  it('不是备份的文件名不给令牌', () => {
    expect(restoreTokenFor('notes.txt')).toBeNull()
    expect(restoreTokenFor('backup_20260925_101530.zip')).toBeNull()
    expect(restoreTokenFor('backup_20260925_101530.sql.bak')).toBeNull()
    expect(restoreTokenFor('audit_log_20260721_20260721.csv')).toBeNull()
    expect(restoreTokenFor('batch_trace.csv')).toBeNull()
    expect(restoreTokenFor('rferp.log')).toBeNull()
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

// 覆盖度预检。恢复是「先 DELETE 全部业务表，再回灌 INSERT」，所以一份早于某张表
// 引入的备份，恢复后那张表会被清空且无处回填——这是静默数据丢失。
describe('tablesLostOnRestore', () => {
  const f = (o: Partial<BackupFile>): BackupFile => ({
    name: 'backup_20260922_123649.sql',
    size: 1,
    modified: '2026-09-22 12:36:49',
    ...o,
  })

  it('覆盖完整 → 没有会丢的表', () => {
    expect(tablesLostOnRestore(f({ schemaKnown: true, missingTables: [] }))).toEqual([])
    expect(tablesLostOnRestore(f({ schemaKnown: true }))).toEqual([])
  })

  it('schemaKnown=false（只读到开头）→ 返回空数组，绝不谎报缺表', () => {
    // 这条最容易写错：把"没读到"当成"备份里没有"，界面就会对着一份完全正常的
    // 大备份说"这张表恢复后会被清空"。
    expect(tablesLostOnRestore(f({ schemaKnown: false }))).toEqual([])
  })

  it('服务端没返回预检字段（旧版本）→ 空数组，不误报', () => {
    expect(tablesLostOnRestore(f({}))).toEqual([])
  })

  it('真的缺表时如实返回', () => {
    expect(
      tablesLostOnRestore(f({ schemaKnown: true, missingTables: ['batch_consumptions'] })),
    ).toEqual(['batch_consumptions'])
  })
})

// 二级级联分组。同一时刻的 .sql / .upgraded.sql / .final.sql 光看名字分不出
// 好坏，而它们回滚结果不同 —— 分组是让用户看出差别的关键一步。
describe('groupByCategory', () => {
  const f = (name: string, category?: BackupCategory): BackupFile => ({
    name,
    size: 1,
    modified: '2026-09-24 17:31:22',
    category,
  })

  it('按 auto → safety → manual → legacy 的固定顺序分组', () => {
    const groups = groupByCategory([
      f('backup_20260924_170830.sql', 'manual'),
      f('auto_20261001_120000.sql', 'auto'),
      f('pre_restore_20260924_172919.sql', 'safety'),
    ])
    expect(groups.map((g) => g.category)).toEqual(['auto', 'safety', 'manual'])
  })

  it('空分类不出现（留一个永远空的分组只会让人以为坏了）', () => {
    const groups = groupByCategory([f('backup_20260924_170830.sql', 'manual')])
    expect(groups).toHaveLength(1)
    expect(groups[0].category).toBe('manual')
  })

  // 旧布局文件（服务端没给 category）必须落在「旧布局」组里，
  // 绝不能悄悄混进 manual —— 它们其实还没被归类。
  it('没有 category 的文件（旧布局）归入 legacy', () => {
    const groups = groupByCategory([f('backup_20260721_154617.sql')])
    expect(groups.map((g) => g.category)).toEqual(['legacy'])
  })

  it('legacy 永远排在最后', () => {
    const groups = groupByCategory([
      f('unknown.sql'),
      f('auto_20261001_120000.sql', 'auto'),
    ])
    expect(groups.map((g) => g.category)).toEqual(['auto', 'legacy'])
  })

  // 组内保持服务端给的新→旧顺序；打乱它等于让用户以为最新的是最下面那个。
  it('组内保持传入顺序（服务端已排好新→旧）', () => {
    const newer = f('auto_20261003_120000.sql', 'auto')
    const older = f('auto_20261001_120000.sql', 'auto')
    const groups = groupByCategory([newer, older])
    expect(groups[0].files.map((x) => x.name)).toEqual([newer.name, older.name])
  })

  it('空列表不产生任何分组', () => {
    expect(groupByCategory([])).toEqual([])
  })
})

describe('autoBackupDayLabel', () => {
  it('0 是关闭，1 与 30 用更自然的说法', () => {
    expect(autoBackupDayLabel(0)).toBe('关闭')
    expect(autoBackupDayLabel(1)).toBe('每天')
    expect(autoBackupDayLabel(30)).toBe('每 30 天')
  })

  it('其余直接说天数', () => {
    expect(autoBackupDayLabel(2)).toBe('每 2 天')
    expect(autoBackupDayLabel(7)).toBe('每 7 天')
  })
})

describe('AUTO_BACKUP_DAY_OPTIONS', () => {
  // 选项集必须与服务端 config.AllowedAutoBackupDays 一致，否则用户能选出一个
  // 服务端会拒绝的档位（表现为"选了就报错"，而不是"根本不让你选"）。
  it('恰好是需求指定的六档 + 关闭', () => {
    expect(AUTO_BACKUP_DAY_OPTIONS).toEqual([0, 1, 2, 3, 5, 7, 30])
  })
})
