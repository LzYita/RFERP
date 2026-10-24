import { expect, it } from 'vitest'
import { restoreConsequences, restoreSummary } from '../src/lib/backup'

it('SQLite warns of identity rollback and never describes INSERT replay', () => {
  const warning = restoreConsequences('sqlite').join(' ')
  expect(warning).toContain('账号、表结构、迁移记录和数据库身份均随快照回退')
  expect(warning).toContain('必须退出并重新打开')
  expect(warning).not.toContain('账号保持现状')
  const summary = restoreSummary({ format: 'sqlite', statements: 0 })
  expect(summary).toContain('整库快照已替换')
  expect(summary).not.toContain('INSERT')
})
it('MySQL retains business replay and account preservation wording', () => {
  const warning = restoreConsequences('mysql').join(' ')
  expect(warning).toContain('账号保持现状')
  expect(warning).toContain('单个事务')
  expect(restoreSummary({ format: 'mysql', statements: 7 })).toBe('整库恢复完成，共执行 7 条 INSERT。')
})
it('unknown type does not promise universal account preservation', () => {
  const warning = restoreConsequences().join(' ')
  expect(warning).toContain('SQLite 会回退整个文件')
  expect(warning).toContain('请先核对备份类型')
})
