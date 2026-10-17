// 库存档位（工作台与零件页共用同一份）。
//
// 为什么值得单独测：Round 2 的 Wails 版在工作台内联写了
// `stock_qty <= p.warn_qty`，零件页却修了。两处判定分叉的直接后果是
// 「工作台报 15 条、零件页勾选后一条都查不出来」。
//
// 判定被提到 lib/lowStock.ts 之后，这里要守的不只是函数本身，还有两条
// 最容易被后人「顺手简化」掉的规则：
//   1. `warn_qty = 0` 的含义是「未设预警线」（表单 hint 就写着「0=不预警」），
//      少了 `warn_qty > 0`，判定会退化成 `stock_qty <= 0`，于是所有库存归零的
//      说明书、包装袋、配件全被算成断货；
//   2. 零库存优先于跌破预警线 —— 两者都要提醒，但红色比橙色更硬，
//      用户的处置动作也不同（先补货 vs 先下单）。
import { describe, it, expect } from 'vitest'
import { isStockAlert, stockLevel, STOCK_LEVEL_TEXT } from '../src/lib/lowStock'

describe('stockLevel', () => {
  it('库存充足', () => {
    expect(stockLevel({ stock_qty: 100, warn_qty: 10 })).toBe('normal')
  })

  it('跌破预警线', () => {
    expect(stockLevel({ stock_qty: 2, warn_qty: 10 })).toBe('low')
    expect(stockLevel({ stock_qty: 9.99, warn_qty: 10 })).toBe('low')
  })

  // 边界：正好等于预警线是「已到线」，不是「已跌破」。
  it('库存正好等于预警线不算低库存（严格小于）', () => {
    expect(stockLevel({ stock_qty: 8000, warn_qty: 8000 })).toBe('normal')
    expect(stockLevel({ stock_qty: 10.01, warn_qty: 10 })).toBe('normal')
  })

  // 关键用例：`0 <= 0` 在数学上成立，但这不是「跌破预警线」。
  it('warn_qty=0 表示未设预警线：有货不算低', () => {
    expect(stockLevel({ stock_qty: 12, warn_qty: 0 })).toBe('normal')
  })

  // 零库存独立成档：没货与快没了是两种颜色、两种处置动作。
  it('库存为 0 归入零库存档，无论配没配预警线', () => {
    expect(stockLevel({ stock_qty: 0, warn_qty: 0 })).toBe('out')
    expect(stockLevel({ stock_qty: 0, warn_qty: 10 })).toBe('out')
  })

  it('负库存（异常数据）也归入零库存档', () => {
    expect(stockLevel({ stock_qty: -5, warn_qty: 10 })).toBe('out')
  })

  // 生产库里 210 条零件中有 14 条是 warn_qty=0 + stock_qty=0 的配件/说明书，
  // 它们曾被工作台误报为「低库存」（阈值退化）而零件页查不出来。
  it('未设预警线的一批零件：有货不报，没货报零库存', () => {
    const noWarnLine = [
      { stock_qty: 12, warn_qty: 0 },
      { stock_qty: 0, warn_qty: 0 },
      { stock_qty: 0, warn_qty: 0 },
    ]
    expect(noWarnLine.map(stockLevel)).toEqual(['normal', 'out', 'out'])
  })
})

describe('isStockAlert', () => {
  // 预警口径包含零库存 —— 这一条决定了工作台报的数字和零件页筛出的条数一致。
  it('零库存与跌破预警线都算预警', () => {
    expect(isStockAlert({ stock_qty: 0, warn_qty: 0 })).toBe(true)
    expect(isStockAlert({ stock_qty: 0, warn_qty: 10 })).toBe(true)
    expect(isStockAlert({ stock_qty: 2, warn_qty: 10 })).toBe(true)
  })

  it('库存正常不算预警，无论有没有配预警线', () => {
    expect(isStockAlert({ stock_qty: 100, warn_qty: 10 })).toBe(false)
    expect(isStockAlert({ stock_qty: 12, warn_qty: 0 })).toBe(false)
    expect(isStockAlert({ stock_qty: 8000, warn_qty: 8000 })).toBe(false)
  })

  // 曾经工作台按 stock<=warn 报 15 条、零件页报 0 条；同口径下必须相等。
  it('工作台与零件页对同一批数据给出相同的预警集合', () => {
    const rows = [
      { stock_qty: 0, warn_qty: 0 },
      { stock_qty: 0, warn_qty: 10 },
      { stock_qty: 2, warn_qty: 10 },
      { stock_qty: 8000, warn_qty: 8000 },
      { stock_qty: 100, warn_qty: 10 },
      { stock_qty: 12, warn_qty: 0 },
    ]
    // 「工作台口径」与「零件页口径」现在是同一个函数，断言两遍是为了让
    // 上面那组形状的回归立刻可见。
    const dashboardSide = rows.filter(isStockAlert)
    const partsPageSide = rows.filter(isStockAlert)
    expect(dashboardSide).toHaveLength(3)
    expect(partsPageSide).toEqual(dashboardSide)
  })
})

describe('STOCK_LEVEL_TEXT', () => {
  it('三档各有自己的文案', () => {
    expect(STOCK_LEVEL_TEXT.out).toBe('无库存')
    expect(STOCK_LEVEL_TEXT.low).toBe('低库存')
    expect(STOCK_LEVEL_TEXT.normal).toBe('正常')
  })
})