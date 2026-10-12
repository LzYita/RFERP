import { describe, it, expect } from 'vitest'
import { validateStock } from '../src/screens/stockForm'

describe('入库', () => {
  it('必须大于 0 —— 与 Fyne part.go:462 一致', () => {
    expect(validateStock('in', '0')).toEqual({ ok: false, msg: '数量必须大于0' })
    expect(validateStock('in', '-1')).toEqual({ ok: false, msg: '数量必须大于0' })
    expect(validateStock('in', '1').ok).toBe(true)
  })

  it('空值有自己的提示，不落到「必须大于0」', () => {
    // 0 和「忘了填」在界面上长得一样，文案不该让用户猜
    expect(validateStock('in', '')).toEqual({ ok: false, msg: '请填写入库数量' })
    expect(validateStock('in', '   ')).toEqual({ ok: false, msg: '请填写入库数量' })
  })

  it('末尾的 0 不算精度', () => {
    expect(validateStock('in', '1.500').ok).toBe(true)
    expect(validateStock('in', '1.234')).toEqual({
      ok: false,
      msg: '数量最多支持两位小数',
    })
  })

  it('非数值被拦', () => {
    expect(validateStock('in', 'abc').ok).toBe(false)
    expect(validateStock('in', 'Infinity').ok).toBe(false)
  })
})

describe('盘点调整', () => {
  it('**可以**设为 0 —— 盘亏到零是真实的盘点结果', () => {
    // 与入库相反：入库拦 0，盘点放行 0。
    // 这两条规则不一致是有原因的，写在一起是为了防止有人"顺手统一"它们。
    expect(validateStock('adjust', '0').ok).toBe(true)
  })

  it('不能为负', () => {
    expect(validateStock('adjust', '-0.5')).toEqual({ ok: false, msg: '数量不能为负' })
  })

  it('绝对值语义：填 5 就是设为 5，不是加 5', () => {
    const r = validateStock('adjust', '5')
    expect(r.ok).toBe(true)
    if (r.ok) expect(r.qty).toBe(5)
  })

  it('小数位限制与入库一致', () => {
    expect(validateStock('adjust', '1.234')).toEqual({
      ok: false,
      msg: '数量最多支持两位小数',
    })
  })
})