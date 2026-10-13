// 操作记录的中文标签、字段级变更与摘要。
//
// 移植自旧版 internal/ui/audit_localisation_test.go（#26 整库操作中文化）与
// audit_test.go 覆盖的渲染意图。
//
// 这一组测试的存在理由：审计列表曾经被写成"只认 code/name/batch_no 的身份摘要"，
// 出入库、盘点、状态变更、整库恢复全部退化成 `#记录id`。下面每条都钉住一类
// 会被静默吞掉的信息。
import { describe, it, expect } from 'vitest'
import {
  actionLabel,
  auditChanges,
  auditObject,
  changeText,
  shortSummary,
  tableLabel,
  type AuditData,
} from '../src/lib/auditLabels'

const log = (over: {
  table_name?: string
  action?: string
  old_data?: AuditData
  new_data?: AuditData
  record_id?: number
}) => ({
  table_name: 'parts',
  record_id: 1,
  action: 'UPDATE',
  old_data: null as AuditData,
  new_data: null as AuditData,
  ...over,
})

describe('标签（#26 整库操作）', () => {
  // 不能把 RESTORE / CLEAR 这种内部动作名暴露给用户
  it('整库恢复与清空的动作名是中文', () => {
    expect(actionLabel('RESTORE')).toBe('整库恢复')
    expect(actionLabel('CLEAR')).toBe('清空业务数据')
    expect(tableLabel('database')).toBe('整库操作')
  })

  it('既有动作标签没有回归', () => {
    expect(actionLabel('INSERT')).toBe('新增')
    expect(actionLabel('UPDATE')).toBe('修改')
    expect(actionLabel('DELETE')).toBe('删除')
    expect(actionLabel('STOCK_IN')).toBe('入库')
    expect(actionLabel('STOCK_DEDUCT')).toBe('出库')
    expect(actionLabel('STOCK_ADJUST')).toBe('盘点')
    expect(actionLabel('REVOKE')).toBe('撤销')
    expect(actionLabel('UPDATE_STATUS')).toBe('状态变更')
  })
})

describe('auditChanges —— 列表的「变更明细」', () => {
  // 库存在 new_data 里，没有 code/name。只认身份字段的摘要在这里必然失效。
  it('入库：给出数量与新库存，不依赖 code/name', () => {
    const items = auditChanges(
      log({
        action: 'STOCK_IN',
        old_data: { code: 'P-1', name: '螺丝' },
        new_data: { in_qty: 3, old_stock: 1, new_stock: 4 },
      }),
    )
    expect(changeText(items)).toBe('入库数量：3；库存：1 → 4')
  })

  it('出库：给出消耗量与库存变化', () => {
    const items = auditChanges(
      log({ action: 'STOCK_DEDUCT', new_data: { deduct: 2, old_stock: 5, new_stock: 3 } }),
    )
    expect(changeText(items)).toBe('消耗数量：2；库存：5 → 3')
  })

  it('盘点：给出差异', () => {
    const items = auditChanges(log({ action: 'STOCK_ADJUST', new_data: { old_stock: 10, new_stock: 8 } }))
    expect(changeText(items)).toBe('库存：10 → 8')
  })

  // 批次是五态，不能落进"1=启用，否则停用"的二态分支
  it('批次状态变更：用五态文案而不是启用/停用', () => {
    const items = auditChanges(
      log({
        table_name: 'product_batches',
        action: 'UPDATE_STATUS',
        old_data: { status: 0 },
        new_data: { status: 1 },
      }),
    )
    expect(changeText(items)).toBe('状态：待生产 → 生产中')
  })

  it('零件状态变更：走二态文案', () => {
    const items = auditChanges(log({ action: 'UPDATE_STATUS', old_data: { status: 1 }, new_data: { status: 0 } }))
    expect(changeText(items)).toBe('状态：启用 → 停用')
  })

  it('修改：只列出真正变了的字段，按业务顺序', () => {
    const items = auditChanges(
      log({
        action: 'UPDATE',
        old_data: { code: 'P-1', name: '螺丝', stock_qty: 1, supplier: 'A厂' },
        new_data: { code: 'P-1', name: '螺丝', stock_qty: 4, supplier: 'A厂' },
      }),
    )
    expect(changeText(items)).toBe('库存：1 → 4')
  })

  it('修改：供应商变更带中文标签', () => {
    const items = auditChanges(
      log({ action: 'UPDATE', old_data: { supplier: 'A厂' }, new_data: { supplier: 'B厂' } }),
    )
    expect(changeText(items)).toBe('供应商：A厂 → B厂')
  })

  it('新增：列出非空字段', () => {
    const items = auditChanges(
      log({ action: 'INSERT', new_data: { code: 'P-9', name: '新件', part_type: '五金', stock_qty: 7 } }),
    )
    expect(changeText(items)).toBe('编码：P-9；名称：新件；分类：五金；库存：7')
  })

  it('删除：用 old_data 列出字段', () => {
    const items = auditChanges(log({ action: 'DELETE', old_data: { code: 'P-9', name: '旧件' } }))
    expect(changeText(items)).toBe('编码：P-9；名称：旧件')
  })

  it('无数据时显示破折号而不是空白', () => {
    expect(changeText(auditChanges(log({ action: 'DELETE' })))).toBe('—')
  })

  // 枚举值要翻成中文，否则用户在审计里看到 0/1 不知道什么意思
  it('可替换与用量模式翻成中文', () => {
    const items = auditChanges(
      log({ table_name: 'bom_items', action: 'INSERT', new_data: { replaceable: 1, use_mode: 0 } }),
    )
    expect(changeText(items)).toContain('可替换：是')
    expect(changeText(items)).toContain('用量模式：每台用N个')
  })
})

describe('auditObject —— 列表的「对象」', () => {
  it('编码 + 名称', () => {
    expect(auditObject(log({ new_data: { code: 'P-1', name: '螺丝' } }))).toBe('[P-1] 螺丝')
  })

  it('只有编码', () => {
    expect(auditObject(log({ new_data: { code: 'P-1' } }))).toBe('P-1')
  })

  it('批次号', () => {
    expect(auditObject(log({ table_name: 'product_batches', new_data: { batch_no: 'B-1' } }))).toBe('B-1')
  })

  // BOM / 投料按 id 关联，没有 code/name——必须退回 id 形式，否则整行无法辨认
  it('按 id 关联的跳过零件：批次#x / 零件#y', () => {
    expect(auditObject(log({ table_name: 'batch_skip_parts', new_data: { batch_id: 2, part_id: 7 } }))).toBe(
      '批次#2 / 零件#7',
    )
  })

  it('兜底是表名而不是空串', () => {
    expect(auditObject(log({ new_data: null }))).toBe('零件')
  })
})

describe('shortSummary —— 工作台的「内容」', () => {
  const restore = {
    table_name: 'database',
    record_id: 1,
    action: 'RESTORE',
    new_data: {
      file: 'D:\\仓库数据\\备份\\backup_20260925_101530.sql',
      statements: 42,
    },
  }

  it('整库恢复：中文、带文件名、不暴露路径与原始动作名', () => {
    const s = shortSummary(restore)
    expect(s).toContain('整库恢复')
    expect(s).toContain('backup_20260925_101530.sql')
    expect(s).not.toContain('\\')
    expect(s).not.toContain('RESTORE')
  })

  it('清空业务数据：中文、带清空前副本名', () => {
    const s = shortSummary({
      table_name: 'database',
      record_id: 2,
      action: 'CLEAR',
      new_data: { pre_clear: 'D:\\仓库数据\\备份\\pre_clear_20260929_105500.sql' },
    })
    expect(s).toContain('清空业务数据')
    expect(s).toContain('pre_clear_20260929_105500.sql')
    expect(s).not.toContain('CLEAR')
  })

  it('路径分隔符混用时也能取出末段', () => {
    const s = shortSummary({
      table_name: 'database',
      record_id: 3,
      action: 'RESTORE',
      new_data: { file: 'C:/data/backup/pre_clear_1.sql' },
    })
    expect(s).toContain('pre_clear_1.sql')
    expect(s).not.toContain('/')
  })

  it('入库：带编码名称与供应商，数量不做四舍五入', () => {
    const s = shortSummary({
      table_name: 'parts',
      record_id: 4,
      action: 'STOCK_IN',
      old_data: { code: 'P-1', name: '螺丝', supplier: 'A厂' },
      // 旧版用 %.0f，0.5 会显示成 1。审计里四舍五入等于抹掉证据。
      new_data: { in_qty: 0.5, new_stock: 1.5 },
    })
    expect(s).toBe('[P-1]螺丝 (A厂) 入库 0.5，当前 1.5')
  })

  it('批次状态变更：批次号 + 产品 + 新状态 + 客户', () => {
    const s = shortSummary({
      table_name: 'product_batches',
      record_id: 5,
      action: 'UPDATE_STATUS',
      old_data: { batch_no: 'B-1', product_code: 'PR-1', product_name: '电机', customer: 'X公司' },
      new_data: { status: 1 },
    })
    expect(s).toBe('批次 B-1 [PR-1]电机 → 生产中 客户:X公司')
  })

  it('新建批次：批次号 + 产品 + 计划数 + 客户', () => {
    const s = shortSummary({
      table_name: 'product_batches',
      record_id: 6,
      action: 'INSERT',
      new_data: { batch_no: 'B-2', product_code: 'PR-2', product_name: '风机', plan_qty: 100, customer: 'Y公司' },
    })
    expect(s).toBe('批次 B-2 [PR-2]风机 计划100 客户:Y公司')
  })

  it('兜底不是空的，至少给出表名', () => {
    expect(shortSummary({ table_name: 'parts', record_id: 7, action: 'UPDATE' })).toBe('零件操作')
  })
})
