import { describe, expect, it } from 'vitest'
import { isProjectedAlert, stockView } from '../src/lib/lowStock'

const part = (over: Partial<{ stock_qty: number; warn_qty: number; status: number }> = {}) => ({
  stock_qty: 100,
  warn_qty: 20,
  status: 1,
  ...over,
})

describe('stockView', () => {
  it('keeps the existing three levels when there is no projection', () => {
    expect(stockView(part({ stock_qty: 0 })).kind).toBe('out')
    expect(stockView(part({ stock_qty: 10 })).kind).toBe('low')
    expect(stockView(part({ stock_qty: 100 })).kind).toBe('normal')
    // 没有投影时 after 等于当前库存，不会凭空出现"将预警"
    expect(stockView(part({ stock_qty: 100 }), null).dashed).toBe(false)
  })

  it('marks projected states as dashed and never as future when projection is absent', () => {
    const v = stockView(part(), { committed: 130, after: -30 })
    expect(v.kind).toBe('will_short')
    expect(v.dashed).toBe(true)
    expect(v.tone).toBe('danger')
    expect(stockView(part()).dashed).toBe(false)
  })

  it('puts current facts before predictions of the same severity', () => {
    // 现在就没货：即使投影也是负数，也显示"无库存"（实心红）
    const out = stockView(part({ stock_qty: 0 }), { committed: 50, after: -50 })
    expect(out.kind).toBe('out')
    expect(out.dashed).toBe(false)
    // 现在已经低于线：优先于"将低于线"
    const low = stockView(part({ stock_qty: 10, warn_qty: 20 }), { committed: 0, after: 10 })
    expect(low.kind).toBe('low')
    expect(low.dashed).toBe(false)
  })

  it('treats warn_qty = 0 as "no warning" but still reports a projected shortfall', () => {
    // 不预警 ≠ 允许欠料
    const noWarn = stockView(part({ stock_qty: 100, warn_qty: 0 }), { committed: 60, after: 40 })
    expect(noWarn.kind).toBe('normal')
    const short = stockView(part({ stock_qty: 100, warn_qty: 0 }), { committed: 120, after: -20 })
    expect(short.kind).toBe('will_short')
  })

  it('does not treat exactly-at-the-line as low', () => {
    expect(stockView(part({ stock_qty: 20, warn_qty: 20 })).kind).toBe('normal')
    expect(stockView(part({ stock_qty: 100, warn_qty: 20 }), { committed: 80, after: 20 }).kind).toBe(
      'normal',
    )
  })

  it('keeps disabled parts disabled regardless of stock', () => {
    const v = stockView(part({ status: 2, stock_qty: 0 }), { committed: 10, after: -10 })
    expect(v.kind).toBe('disabled')
    expect(v.dashed).toBe(false)
    // 数字仍然可查：停用件可能还被在产批次使用
    expect(v.after).toBe(-10)
  })

  it('explains the state with numbers in the hint', () => {
    const v = stockView(part(), { committed: 130, after: -30 })
    expect(v.hint).toContain('在产占用 130')
    expect(v.hint).toContain('-30')
    expect(v.hint).toContain('预警线 20')
  })
})

describe('isProjectedAlert', () => {
  it('covers both dashed states only', () => {
    expect(isProjectedAlert(stockView(part(), { committed: 130, after: -30 }))).toBe(true)
    expect(isProjectedAlert(stockView(part({ stock_qty: 100, warn_qty: 90 }), { committed: 20, after: 80 }))).toBe(true)
    for (const v of [
      stockView(part()),
      stockView(part({ stock_qty: 0 })),
      stockView(part({ stock_qty: 10 })),
      stockView(part({ status: 2 })),
    ]) {
      expect(isProjectedAlert(v)).toBe(false)
    }
  })
})
