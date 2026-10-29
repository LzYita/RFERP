// 投料批次分配的纯逻辑：按时间排、按顺序摊、手改数量与合计校验。
import { describe, expect, it } from 'vitest'
import {
  allocatedTotal,
  candidatesFor,
  checkPart,
  clampQty,
  fillAllocations,
  initialAllocations,
  moveRow,
  remainingAfter,
  setRowQty,
  type AllocCandidate,
} from '../src/lib/traceAlloc'
import type { PartInbound } from '../src/lib/catalog'

const cand = (inboundId: number, batchNo: string, available: number, createdAt = '2026-10-01T00:00:00Z'): AllocCandidate => ({
  inboundId,
  batchNo,
  available,
  createdAt,
})

describe('fillAllocations', () => {
  it('按顺序摊：前一批扣满才轮到下一批', () => {
    const rows = fillAllocations(10, [cand(1, 'RK-A', 6), cand(2, 'RK-B', 10)])
    expect(rows.map((r) => r.qty)).toEqual([6, 4])
    expect(remainingAfter(rows[0])).toBe(0)
    expect(remainingAfter(rows[1])).toBe(6)
    expect(allocatedTotal(rows)).toBe(10)
  })

  it('不够摊时余量不分配（完成时按先进先出兜底）', () => {
    const rows = fillAllocations(20, [cand(1, 'RK-A', 6), cand(2, 'RK-B', 3)])
    expect(rows.map((r) => r.qty)).toEqual([6, 3])
    expect(allocatedTotal(rows)).toBe(9)
  })
})

describe('clampQty / setRowQty', () => {
  it('负数与非数字按 0，超过可用量封顶', () => {
    expect(clampQty('-3', 10)).toBe(0)
    expect(clampQty('abc', 10)).toBe(0)
    expect(clampQty('99', 4)).toBe(4)
    expect(clampQty('2.5', 4)).toBe(2.5)
  })

  it('只改指定行，且不越过该行可用量', () => {
    const rows = fillAllocations(10, [cand(1, 'RK-A', 6), cand(2, 'RK-B', 10)])
    const next = setRowQty(rows, 0, '99')
    expect(next[0].qty).toBe(6)
    expect(next[1].qty).toBe(4)
  })
})

describe('checkPart', () => {
  const rows = fillAllocations(10, [cand(1, 'RK-A', 6), cand(2, 'RK-B', 10)])
  const avail = [{ ...rows[0] }, { ...rows[1] }]

  it('合计等于用量 → ok', () => {
    expect(checkPart(10, rows).level).toBe('ok')
  })

  it('合计不等于用量但可用量还没分完 → mismatch（拦住保存）', () => {
    const edited = setRowQty(rows, 1, '1')
    const res = checkPart(10, edited)
    expect(res.level).toBe('mismatch')
    expect(res.msg).toContain('还差')
  })

  it('用量超过全部可用量且已分完 → short（允许保存，先说明兜底）', () => {
    const short = fillAllocations(20, [cand(1, 'RK-A', 6), cand(2, 'RK-B', 3)])
    const res = checkPart(20, short)
    expect(res.level).toBe('short')
    expect(res.msg).toContain('先进先出')
  })

  it('未改动的完整分配不会误报', () => {
    expect(checkPart(10, avail).level).toBe('ok')
  })
})

describe('moveRow', () => {
  it('换位后顺序生效', () => {
    const rows = fillAllocations(10, [cand(1, 'RK-A', 6), cand(2, 'RK-B', 10)])
    const moved = moveRow(rows, 1, 0)
    expect(moved[0].batchNo).toBe('RK-B')
  })

  it('越界或原地不动时原样返回', () => {
    const rows = [cand(1, 'RK-A', 1), cand(2, 'RK-B', 2)]
    expect(moveRow(rows, 0, 0)).toBe(rows)
    expect(moveRow(rows, 0, 5)).toBe(rows)
    expect(moveRow(rows, -1, 1)).toBe(rows)
  })
})

describe('candidatesFor', () => {
  const inbound = (over: Partial<PartInbound>): PartInbound => ({
    id: 1,
    part_id: 1,
    batch_no: 'RK-20261001-0001',
    qty: 100,
    remaining: 100,
    created_at: '2026-10-01T08:00:00Z',
    occupied: 0,
    ...over,
  })

  it('可用 = 剩余 + 本批已有分配 − 在产占用', () => {
    const rows = candidatesFor(
      [
        inbound({ id: 1, batch_no: 'RK-A', remaining: 100, occupied: 70 }),
        inbound({ id: 2, batch_no: 'RK-B', remaining: 40, occupied: 0 }),
      ],
      [{ inbound_id: 1, batch_no: 'RK-A', qty: 30, position: 0 }],
    )
    expect(rows.map((r) => [r.batchNo, r.available])).toEqual([
      ['RK-A', 60],
      ['RK-B', 40],
    ])
  })

  it('默认按入库时间由远到近，与 id 大小无关', () => {
    const rows = candidatesFor([
      inbound({ id: 1, batch_no: 'RK-NEW', created_at: '2026-10-20T08:00:00Z' }),
      inbound({ id: 2, batch_no: 'RK-OLD', created_at: '2026-09-01T08:00:00Z' }),
      inbound({ id: 3, batch_no: 'RK-MID', created_at: '2026-09-20T08:00:00Z' }),
    ])
    expect(rows.map((r) => r.batchNo)).toEqual(['RK-OLD', 'RK-MID', 'RK-NEW'])
  })

  it('已有分配保留原顺序，其余按时间', () => {
    const rows = candidatesFor(
      [
        inbound({ id: 1, batch_no: 'RK-A', created_at: '2026-09-01T08:00:00Z' }),
        inbound({ id: 2, batch_no: 'RK-B', created_at: '2026-09-10T08:00:00Z' }),
        inbound({ id: 3, batch_no: 'RK-C', created_at: '2026-09-20T08:00:00Z' }),
      ],
      [{ inbound_id: 3, batch_no: 'RK-C', qty: 5, position: 0 }],
    )
    expect(rows.map((r) => r.batchNo)).toEqual(['RK-C', 'RK-A', 'RK-B'])
  })

  it('没有可用量的批次不列出来', () => {
    const rows = candidatesFor([inbound({ id: 1, batch_no: 'RK-A', remaining: 0, occupied: 0 })])
    expect(rows).toEqual([])
  })
})

describe('initialAllocations', () => {
  const candidates = [
    cand(1, 'RK-OLD', 4, '2026-09-01T08:00:00Z'),
    cand(2, 'RK-NEW', 10, '2026-10-20T08:00:00Z'),
  ]

  it('没有保存过 → 按顺序摊', () => {
    expect(initialAllocations(6, candidates).map((r) => r.qty)).toEqual([4, 2])
  })

  it('保存过且用量一致 → 沿用保存的数量', () => {
    const rows = initialAllocations(6, candidates, [
      { inbound_id: 2, batch_no: 'RK-NEW', qty: 6, position: 0 },
      { inbound_id: 1, batch_no: 'RK-OLD', qty: 0, position: 1 },
    ])
    expect(rows.map((r) => r.qty)).toEqual([0, 6])
  })

  it('保存过但用量变了 → 重新按顺序摊', () => {
    const rows = initialAllocations(3, candidates, [
      { inbound_id: 2, batch_no: 'RK-NEW', qty: 6, position: 0 },
    ])
    expect(rows.map((r) => r.qty)).toEqual([3, 0])
  })

  it('保存的数量超过现在可用量 → 按可用量封顶', () => {
    const rows = initialAllocations(10, candidates, [
      { inbound_id: 1, batch_no: 'RK-OLD', qty: 8, position: 0 },
      { inbound_id: 2, batch_no: 'RK-NEW', qty: 2, position: 1 },
    ])
    expect(rows[0].qty).toBe(4)
  })
})
