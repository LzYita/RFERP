import { describe, expect, it } from 'vitest'
import { buildPreviewRows, type StockProjectionResult } from '../src/lib/projection'

const parts = [
  { id: 1, code: 'M-001', name: '加热管', unit: '个' },
  { id: 2, code: 'M-002', name: '外壳', unit: '个' },
  { id: 3, code: 'M-003', name: '彩盒', unit: '个' },
]

function result(over: Partial<StockProjectionResult> = {}): StockProjectionResult {
  return {
    parts: [
      {
        part_id: 1, stock: 100, warn: 20, committed: 130, after: -30,
        holders: [
          { batch_no: '计划中', qty: 10, planned: true },
          { batch_no: 'B-1', qty: 60 },
          { batch_no: 'B-2', qty: 60 },
        ],
      },
      {
        part_id: 3, stock: 500, warn: 100, committed: 110, after: 390,
        holders: [{ batch_no: '计划中', qty: 10, planned: true }, { batch_no: 'B-1', qty: 100 }],
      },
      {
        part_id: 2, stock: 300, warn: 50, committed: 80, after: 220,
        holders: [
          { batch_no: 'B-2', qty: 80 },
          { batch_no: '计划中', qty: 20, planned: true },
        ],
      },
    ],
    batches: [],
    summary: { willWarn: 0, willShort: 1 },
    ...over,
  }
}

describe('buildPreviewRows', () => {
  it('keeps only parts this batch consumes and separates its own share from others', () => {
    const rows = buildPreviewRows(result(), parts)
    expect(rows.map((r) => r.code)).toEqual(['M-001', 'M-002', 'M-003'])
    const heating = rows.find((r) => r.code === 'M-001')!
    expect(heating.planned).toBe(10)
    expect(heating.others).toBe(120) // 130 总占用 − 本批 10
    expect(heating.after).toBe(-30)
    expect(heating.level).toBe('short')
  })

  it('orders short before low before ok, then by resulting stock', () => {
    const rows = buildPreviewRows(result(), parts)
    expect(rows[0].level).toBe('short')
  })

  it('classifies "still positive but under the line" as low, not short', () => {
    const rows = buildPreviewRows(
      result({
        parts: [
          {
            part_id: 2, stock: 100, warn: 90, committed: 20, after: 80,
            holders: [{ batch_no: '计划中', qty: 20, planned: true }],
          },
        ],
      }),
      parts,
    )
    expect(rows[0].level).toBe('low')
  })

  it('falls back to a placeholder code when the part list lacks the row', () => {
    const rows = buildPreviewRows(result(), [])
    expect(rows[0].code).toBe('#1')
    expect(rows[0].name).toBe('')
  })
})
