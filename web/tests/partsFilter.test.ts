// 零件筛选：分类 + 关键字 + 低库存三者叠加。
//
// 为什么值得单独测：分类维度是 Fyne 原版有、迁移时整条丢掉的，
// 恢复它时最容易犯的错是「用表单常量当候选」——那样库里存着的
// 常量之外的分类会筛不出来，用户以为自己那些零件没了。
import { describe, it, expect } from 'vitest'
import { effectiveTypeFilter, filterParts, typeFilterOptions } from '../src/screens/PartsScreen'
import type { Part } from '../src/lib/catalog'

function part(over: Partial<Part>): Part {
  return {
    id: 1,
    code: 'P-0001',
    name: '零件',
    spec: '10x10',
    unit: '个',
    part_type: '电子',
    stock_qty: 100,
    warn_qty: 10,
    status: 1,
    version: 1,
    ...over,
  }
}

const rows: Part[] = [
  part({ id: 1, code: 'P-0001', name: '电阻', spec: '0805', part_type: '电子' }),
  part({ id: 2, code: 'P-0002', name: '螺丝', spec: 'M3', part_type: '五金', supplier: '沪东厂' }),
  part({ id: 3, code: 'P-0003', name: '外壳', spec: 'ABS', part_type: '塑料', stock_qty: 2 }),
  part({ id: 4, code: 'P-0004', name: '未知件', spec: '', part_type: undefined }),
]

describe('typeFilterOptions', () => {
  it('从数据里推候选，去重并排序，首项是「全部」', () => {
    // 拼音序（dianzi/suliao/wujin）。Fyne 用 sort.Strings 按码点排，
    // 中文下是 五金/塑料/电子——那是 Go 默认排序的副产物，此处有意改为拼音。
    expect(typeFilterOptions(rows)).toEqual(['全部', '电子', '塑料', '五金'])
  })

  // 关键用例：候选必须包含库里存着、但不在表单常量里的分类
  it('包含表单常量之外的分类', () => {
    const opts = typeFilterOptions([part({ part_type: '历史遗留分类' })])
    expect(opts).toContain('历史遗留分类')
  })

  it('忽略空值与空白', () => {
    const opts = typeFilterOptions([part({ part_type: undefined }), part({ part_type: '  ' })])
    expect(opts).toEqual(['全部'])
  })
})

describe('effectiveTypeFilter', () => {
  // 删掉最后一个该分类的零件后，选中值就失效了。不回退的话列表会莫名其妙空白。
  it('选中值失效时回退到「全部」', () => {
    expect(effectiveTypeFilter(['全部', '电子'], '塑料')).toBe('全部')
  })

  it('选中值有效时原样保留', () => {
    expect(effectiveTypeFilter(['全部', '电子', '塑料'], '塑料')).toBe('塑料')
  })

  it('有效分类为「全部」时也正常', () => {
    expect(effectiveTypeFilter(['全部'], '全部')).toBe('全部')
  })
})

describe('filterParts', () => {
  it('「全部」不筛分类', () => {
    expect(filterParts(rows, '', '全部', false)).toHaveLength(4)
  })

  it('分类精确匹配，不做模糊', () => {
    expect(filterParts(rows, '', '电子', false).map((p) => p.code)).toEqual(['P-0001'])
    // 「电」不是枚举值，不该捞出「电子」
    expect(filterParts(rows, '', '电', false)).toHaveLength(0)
  })

  it('关键字能搜到编码与名称', () => {
    expect(filterParts(rows, 'P-0002', '全部', false).map((p) => p.code)).toEqual(['P-0002'])
    expect(filterParts(rows, '螺丝', '全部', false).map((p) => p.code)).toEqual(['P-0002'])
  })

  it('关键字能搜到规格与分类', () => {
    expect(filterParts(rows, 'M3', '全部', false).map((p) => p.code)).toEqual(['P-0002'])
    // Fyne 版占位写了「规格」却没把规格纳入全文，这里按并集处理
    expect(filterParts(rows, 'ABS', '全部', false).map((p) => p.code)).toEqual(['P-0003'])
    expect(filterParts(rows, '塑料', '全部', false).map((p) => p.code)).toEqual(['P-0003'])
  })

  // 2026-10-13 用户要求纳入。Fyne 版没把供应商放进全文，搜厂名会一无所获。
  it('关键字能搜到供应商', () => {
    expect(filterParts(rows, '沪东厂', '全部', false).map((p) => p.code)).toEqual(['P-0002'])
    expect(filterParts(rows, '沪东', '全部', false).map((p) => p.code)).toEqual(['P-0002'])
  })

  // 供应商为空（null / 空串）时不能因为它而报错或误命中
  it('供应商为空时不影响筛选', () => {
    expect(filterParts(rows, '', '全部', false)).toHaveLength(4)
    expect(filterParts(rows, '不存在', '全部', false)).toHaveLength(0)
  })

  it('关键字大小写不敏感，且忽略首尾空格', () => {
    expect(filterParts(rows, '  abs  ', '全部', false).map((p) => p.code)).toEqual(['P-0003'])
  })

  it('分类与关键字是「与」的关系', () => {
    expect(filterParts(rows, '螺丝', '电子', false)).toHaveLength(0)
    expect(filterParts(rows, '电阻', '电子', false).map((p) => p.code)).toEqual(['P-0001'])
  })

  it('低库存叠加在分类与关键字之上', () => {
    expect(filterParts(rows, '', '全部', true).map((p) => p.code)).toEqual(['P-0003'])
    expect(filterParts(rows, '', '电子', true)).toHaveLength(0)
  })

  it('不修改入参', () => {
    const before = rows.map((p) => p.id)
    filterParts(rows, '电阻', '电子', true)
    expect(rows.map((p) => p.id)).toEqual(before)
  })
})

// 勾选框现在列的是「库存预警」：零库存 + 跌破预警线两档。
// 这批数据取自生产库的真实形状 —— 说明书、包装袋这类 warn_qty=0 的配件，
// 它们库存归零时曾被工作台按 stock<=warn 报成「低库存」，而零件页一条都查不出。
describe('filterParts 的库存预警口径包含零库存', () => {
  const stock: Part[] = [
    part({ id: 11, code: 'P-0011', name: '说明书', part_type: '包装', stock_qty: 0, warn_qty: 0 }),
    part({ id: 12, code: 'P-0012', name: '外壳', part_type: '塑料', stock_qty: 2, warn_qty: 10 }),
    part({ id: 13, code: 'P-0013', name: '电阻', part_type: '电子', stock_qty: 0, warn_qty: 10 }),
    part({ id: 14, code: 'P-0014', name: '螺丝', part_type: '五金', stock_qty: 100, warn_qty: 10 }),
    part({ id: 15, code: 'P-0015', name: '袋子', part_type: '包装', stock_qty: 12, warn_qty: 0 }),
  ]

  it('零库存与跌破预警线都被列出', () => {
    expect(filterParts(stock, '', '全部', true).map((p) => p.code)).toEqual([
      'P-0011', // 零库存且未设预警线
      'P-0012', // 跌破预警线
      'P-0013', // 零库存且配了预警线
    ])
  })

  it('不勾选时不过滤', () => {
    expect(filterParts(stock, '', '全部', false)).toHaveLength(5)
  })

  it('零库存同样叠加在分类与关键字之上', () => {
    expect(filterParts(stock, '', '包装', true).map((p) => p.code)).toEqual(['P-0011'])
    expect(filterParts(stock, '袋', '包装', true)).toHaveLength(0)
  })
})
