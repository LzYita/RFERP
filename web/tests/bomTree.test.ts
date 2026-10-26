import { describe, expect, it } from 'vitest'
import { buildBomGroups, UNTYPED, type BomGroup } from '../src/lib/bomTree'
import type { BOMItem, Part } from '../src/lib/catalog'

const item = (id: number, part_id: number, code = `M-${part_id}`): BOMItem =>
  ({ id, part_id, product_id: 1, quantity: 2, loss_rate: 0, replaceable: 0, use_mode: 0, version: 1, part_code: code }) as BOMItem

const part = (id: number, part_type?: string): Part =>
  ({ id, code: `M-${id}`, name: `零件${id}`, part_type, stock_qty: 0, warn_qty: 0, status: 1, version: 1 }) as Part

function names(groups: readonly BomGroup[]): string[] {
  return groups.map((g) => g.name)
}

describe('buildBomGroups', () => {
  it('groups BOM items by the part master data category', () => {
    const groups = buildBomGroups(
      [item(1, 7), item(2, 8), item(3, 7)],
      [part(7, '电子'), part(8, '五金')],
    )
    expect(names(groups)).toEqual(['电子', '五金'])
    expect(groups[0].leaves.map((l) => l.item.id)).toEqual([1, 3])
    expect(groups[0].leaves[0].part?.id).toBe(7)
  })

  it('sorts categories by Chinese locale, not by code point', () => {
    // 码点序会把「包装辅料」排在「电子」后面但「五金」前面参差错落；
    // zh 排序是 PartsScreen 分类筛选的既定约定，这里必须一致
    const groups = buildBomGroups(
      [item(1, 1), item(2, 2), item(3, 3)],
      [part(1, '电子'), part(2, '包装辅料'), part(3, '五金')],
    )
    expect(names(groups)).toEqual(['包装辅料', '电子', '五金'])
  })

  it('puts untyped parts into a trailing catch-all group', () => {
    const groups = buildBomGroups(
      [item(1, 1), item(2, 2)],
      [part(1, '电子'), part(2, undefined)],
    )
    expect(names(groups)).toEqual(['电子', UNTYPED])
    expect(groups[1].leaves).toHaveLength(1)
  })

  it('treats blank category the same as a missing one', () => {
    const groups = buildBomGroups([item(1, 1)], [part(1, '   ')])
    expect(names(groups)).toEqual([UNTYPED])
  })

  it('keeps rendering an item whose part master data is gone, under the catch-all', () => {
    // part_id 查不到主数据（并发删除等）：明细不能消失，编码回落 BOMItem 冗余值
    const groups = buildBomGroups([item(1, 999, 'M-GONE')], [part(7, '电子')])
    expect(names(groups)).toEqual([UNTYPED])
    expect(groups[0].leaves[0].part).toBeNull()
    expect(groups[0].leaves[0].item.part_code).toBe('M-GONE')
  })

  it('keeps server order inside a group and returns no groups for an empty BOM', () => {
    const groups = buildBomGroups([item(9, 7), item(8, 7)], [part(7, '电子')])
    expect(groups[0].leaves.map((l) => l.item.id)).toEqual([9, 8])
    expect(buildBomGroups([], [part(7, '电子')])).toEqual([])
  })
})
