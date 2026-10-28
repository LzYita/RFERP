// 投料批次分配的纯逻辑：按顺序摊数量、拖动换位、可用量扣掉在产占用。
import { describe, expect, it } from 'vitest'
import {
  allocatedTotal,
  candidatesFor,
  fillAllocations,
  moveRow,
  remainingAfter,
  type AllocCandidate,
} from '../src/lib/traceAlloc'
import type { PartInbound } from '../src/lib/catalog'

const cand = (inboundId: number, batchNo: string, available: number): AllocCandidate => ({
  inboundId,
  batchNo,
  available,
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

  it('用量小于第一批可用时，后面的批次不消耗', () => {
    const rows = fillAllocations(2, [cand(1, 'RK-A', 6), cand(2, 'RK-B', 3)])
    expect(rows.map((r) => r.qty)).toEqual([2, 0])
  })
})

describe('moveRow', () => {
  it('换位后重摊，新的第一行先扣', () => {
    const rows = fillAllocations(10, [cand(1, 'RK-A', 6), cand(2, 'RK-B', 10)])
    const moved = moveRow(rows, 1, 0)
    const refilled = fillAllocations(10, moved)
    expect(refilled[0].batchNo).toBe('RK-B')
    expect(refilled.map((r) => r.qty)).toEqual([10, 0])
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
      ['RK-A', 60], // 100 + 30（自己已分配）− 70（含自己那 30）
      ['RK-B', 40],
    ])
  })

  it('已有分配决定顺序，其余按入库先后', () => {
    const rows = candidatesFor(
      [
        inbound({ id: 1, batch_no: 'RK-A' }),
        inbound({ id: 2, batch_no: 'RK-B' }),
        inbound({ id: 3, batch_no: 'RK-C' }),
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
