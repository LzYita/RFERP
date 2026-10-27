import { describe, it, expect } from 'vitest'
import {
  bomConsume,
  findShortages,
  canChangeStatus,
  canManageParts,
  statusText,
  cascadeFor,
  resolveProduct,
  defaultBatchNo,
  BATCH_STATUS,
  type Batch,
} from '../src/lib/batch'

/**
 * bomConsume 必须与 `internal/service/service.go` 的 `bomConsume` 逐行等价。
 * 界面用它预警、服务端用它扣库存，两者不一致的话预警就是在骗人。
 */
describe('bomConsume', () => {
  it('use_mode=0：计划数 × 用量 × (1+损耗率)', () => {
    // 100 台 × 每台 2 个 × 5% 损耗 = 210
    expect(bomConsume(100, { quantity: 2, loss_rate: 5, use_mode: 0 })).toBe(210)
    expect(bomConsume(100, { quantity: 2, loss_rate: 0, use_mode: 0 })).toBe(200)
    expect(bomConsume(1, { quantity: 0.5, loss_rate: 0, use_mode: 0 })).toBe(0.5)
  })

  it('use_mode=1：ceil(计划数/每M台) × (1+损耗)，再向上取整', () => {
    // 每 10 台用 1 个箱子，100 台 = 10 箱，5% 损耗 → ceil(10.5) = 11
    expect(bomConsume(100, { quantity: 10, loss_rate: 5, use_mode: 1 })).toBe(11)
    // 不进位的情况：100/10 = 10，×1.0 = 10
    expect(bomConsume(100, { quantity: 10, loss_rate: 0, use_mode: 1 })).toBe(10)
    // 101 台 / 10 = 10.1 → ceil = 11
    expect(bomConsume(101, { quantity: 10, loss_rate: 0, use_mode: 1 })).toBe(11)
  })

  it('use_mode=1 且用量 ≤ 0 时退化为每 1 台 1 个（与 Go 的守卫一致）', () => {
    // Go: m := item.Quantity; if m <= 0 { m = 1 }
    expect(bomConsume(100, { quantity: 0, loss_rate: 0, use_mode: 1 })).toBe(100)
    expect(bomConsume(100, { quantity: -5, loss_rate: 0, use_mode: 1 })).toBe(100)
  })

  it('损耗率放大消耗', () => {
    expect(bomConsume(100, { quantity: 1, loss_rate: 100, use_mode: 0 })).toBe(200)
  })
})

const mkPart = (id: number, stock: number) => ({
  id,
  code: `P-${id}`,
  name: `零件${id}`,
  unit: '个',
  stock_qty: stock,
  warn_qty: 0,
  status: 1,
  version: 1,
})

const mkBom = (part_id: number, quantity = 1, use_mode = 0, loss_rate = 0) => ({
  id: part_id,
  product_id: 1,
  part_id,
  quantity,
  loss_rate,
  replaceable: 0,
  use_mode,
  version: 1,
  part_code: `P-${part_id}`,
  part_name: `零件${part_id}`,
})

describe('缺料判定', () => {
  it('需求严格大于库存才算缺 —— 正好等于不算', () => {
    // 服务端夹取是 min(请求量, 库存)，正好等于时能全额扣完，不该拦
    expect(findShortages(100, [mkBom(1, 1)], [mkPart(1, 100)])).toHaveLength(0)
    expect(findShortages(100, [mkBom(1, 1)], [mkPart(1, 99)])).toHaveLength(1)
  })

  it('零件在零件表里找不到时跳过，不报缺（Fyne batch.go:354-356）', () => {
    expect(findShortages(100, [mkBom(1, 999)], [])).toHaveLength(0)
  })

  it('已标记跳过的零件不参与 —— 完成时不扣它们', () => {
    expect(findShortages(100, [mkBom(1, 999)], [mkPart(1, 1)], [1])).toHaveLength(0)
  })

  it('多个零件逐个列出，只有真缺的那些进结果', () => {
    // 计划 100 台，每台各用 1 个 → 每个都需要 100
    const r = findShortages(100, [mkBom(1), mkBom(2), mkBom(3)], [
      mkPart(1, 50), // 缺
      mkPart(2, 100), // 刚好够
      mkPart(3, 500), // 充裕
    ])
    expect(r).toHaveLength(1)
    expect(r[0].partId).toBe(1)
    expect(r[0].needed).toBe(100)
    expect(r[0].have).toBe(50)
  })
})

const mkBatch = (status: number): Batch => ({
  id: 1,
  batch_no: 'B1',
  product_id: 1,
  plan_qty: 100,
  produced_qty: 0,
  status,
  version: 1,
  consumption_recorded: 0,
})

describe('状态机守卫', () => {
  it('待生产可以改成生产中或已完成', () => {
    expect(canChangeStatus(mkBatch(BATCH_STATUS.pending), BATCH_STATUS.running).ok).toBe(true)
    expect(canChangeStatus(mkBatch(BATCH_STATUS.pending), BATCH_STATUS.done).ok).toBe(true)
  })

  it('已完成与已撤销一律不可再改', () => {
    expect(canChangeStatus(mkBatch(BATCH_STATUS.done), BATCH_STATUS.running).ok).toBe(false)
    expect(canChangeStatus(mkBatch(BATCH_STATUS.revoked), BATCH_STATUS.running).ok).toBe(false)
  })

  it('相同状态不重复提交 —— API 不拦，界面必须拦', () => {
    // 否则后端会成功并写一条状态没变的审计记录
    const r = canChangeStatus(mkBatch(BATCH_STATUS.running), BATCH_STATUS.running)
    expect(r.ok).toBe(false)
    if (!r.ok) expect(r.msg).toContain('生产中')
  })

  it('已完成与已撤销的文案要分清', () => {
    // Fyne 两种情况都用「已完成的批次不能更改状态」，对已撤销批次是错的
    const done = canChangeStatus(mkBatch(BATCH_STATUS.done), 1)
    const revoked = canChangeStatus(mkBatch(BATCH_STATUS.revoked), 1)
    if (!done.ok && !revoked.ok) {
      expect(done.msg).toContain('已完成')
      expect(revoked.msg).toContain('撤销')
      expect(done.msg).not.toBe(revoked.msg)
    }
  })

  it('已完成/已撤销不能改零件（跳过/临时），但可撤销', () => {
    expect(canManageParts(mkBatch(BATCH_STATUS.done)).ok).toBe(false)
    expect(canManageParts(mkBatch(BATCH_STATUS.revoked)).ok).toBe(false)
    expect(canManageParts(mkBatch(BATCH_STATUS.running)).ok).toBe(true)
  })
})

describe('状态文案', () => {
  it('五个状态齐全，未知值有兜底', () => {
    expect(statusText(0)).toBe('待生产')
    expect(statusText(1)).toBe('生产中')
    expect(statusText(2)).toBe('已完成')
    expect(statusText(3)).toBe('已暂停')
    expect(statusText(4)).toBe('已撤销')
    expect(statusText(99)).toBe('未知')
  })
})

describe('产品级联', () => {
  const ps = [
    { id: 1, code: 'P-1', name: '电机', spec: 'A型', unit: '台', status: 1, version: 1 },
    { id: 2, code: 'P-1', name: '电机', spec: 'B型', unit: '台', status: 1, version: 1 },
    { id: 3, code: 'P-2', name: '水泵', unit: '台', status: 1, version: 1 },
  ]

  it('选名称后收窄编码，编码下只有一个时不再要求规格', () => {
    const a = cascadeFor(ps, '电机', '')
    expect(a.codes).toEqual(['P-1'])
    expect(a.specs).toEqual([])

    const b = cascadeFor(ps, '水泵', 'P-2')
    expect(b.specs).toEqual([])
  })

  it('同名同编码有多个规格时，规格必选且不能默认取第一个', () => {
    // 漏选规格就会建到 A型 上，而用户以为选的是 B型
    expect(resolveProduct(ps, '电机', 'P-1', '')).toBeNull()
    expect(resolveProduct(ps, '电机', 'P-1', 'B型')?.id).toBe(2)
  })

  it('无规格歧义时直接取唯一项', () => {
    expect(resolveProduct(ps, '水泵', 'P-2', '')?.id).toBe(3)
  })

  it('名称或编码未选时不解析', () => {
    expect(resolveProduct(ps, '', '', '')).toBeNull()
    expect(resolveProduct(ps, '电机', '', '')).toBeNull()
  })
})

describe('默认批次号', () => {
  it('BATCH + 14 位时间戳', () => {
    const s = defaultBatchNo(new Date(2026, 9, 12, 14, 30, 45))
    expect(s).toBe('BATCH20261012143045')
  })
})