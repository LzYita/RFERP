import { describe, it, expect } from 'vitest'
import { validateStock } from '../src/screens/stockForm'
import { parseQuantity } from '../src/lib/num'

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
      msg: '入库数量最多支持两位小数',
    })
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
})

/**
 * 这一组是回归测试：上一版用 `Number.isFinite` 把关，那问的是「能不能解析」
 * 而不是「该不该接受」，于是下面六种写法全部静默放行，其中两种会直接写出
 * 天文数字的库存。
 */
describe('非法输入：都不能变成库存数字', () => {
  const rejected: [string, string][] = [
    ['指数写法', '1e5'],
    ['指数写法大写', '1E5'],
    ['负指数', '1e-3'],
    ['十六进制', '0x10'],
    ['二进制', '0b101'],
    ['八进制', '0o17'],
    ['前导正号', '+5'],
    ['字母', 'abc'],
    ['单位后缀', '5个'],
    ['千分位', '1,000'],
    ['全角数字', '１'],
    ['内嵌空格', '1 2'],
    ['两个小数点', '1.2.3'],
    ['残缺指数', '1e'],
    ['无穷', 'Infinity'],
    ['非数', 'NaN'],
  ]

  for (const [why, input] of rejected) {
    it(`${why} ${JSON.stringify(input)} 被拒`, () => {
      for (const mode of ['in', 'adjust'] as const) {
        const r = validateStock(mode, input)
        expect(r.ok, `${mode} 模式放行了 ${input}`).toBe(false)
      }
    })
  }

  it('1e5 绝不会被读成 100000', () => {
    // 这条单独写出来：前面几条只断言「被拒」，这条断言「值不存在」
    expect(parseQuantity('1e5', '数量').ok).toBe(false)
    expect(parseQuantity('0x10', '数量').ok).toBe(false)
  })
})

describe('合法输入', () => {
  const accepted: [string, number][] = [
    ['0', 0],
    ['1', 1],
    ['007', 7],
    ['1.', 1],
    ['.5', 0.5],
    ['1.500', 1.5],
    ['0.01', 0.01],
    ['99999999', 99999999],
  ]

  for (const [input, want] of accepted) {
    it(`${JSON.stringify(input)} → ${want}`, () => {
      const r = parseQuantity(input, '数量')
      expect(r.ok).toBe(true)
      if (r.ok) expect(r.value).toBe(want)
    })
  }
})