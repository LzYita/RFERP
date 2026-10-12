import { describe, it, expect } from 'vitest'
import {
  emptyPartDraft,
  partToDraft,
  validatePartDraft,
  draftToWrite,
  tooManyDecimals,
} from '../src/screens/partForm'

describe('零件表单：小数位', () => {
  it('超过两位被拒', () => {
    expect(tooManyDecimals('1.234')).toBe(true)
  })

  it('末尾的 0 不算精度 —— 1.500 与 1.5 是同一个数', () => {
    // 后端 representableQuantity（service.go:95）也是这个判法，
    // 前端若更严就会挡住后端本可以接受的输入。
    expect(tooManyDecimals('1.500')).toBe(false)
    expect(tooManyDecimals('1.50')).toBe(false)
    expect(tooManyDecimals('1.5')).toBe(false)
    expect(tooManyDecimals('1')).toBe(false)
  })
})

describe('零件表单：校验', () => {
  const base = () => ({ ...emptyPartDraft(), code: 'P-1', name: '电阻' })

  it('新增与编辑共用必填项', () => {
    expect(validatePartDraft({ ...base(), code: '' }, false).ok).toBe(false)
    expect(validatePartDraft({ ...base(), code: '   ' }, true).ok).toBe(false)
    expect(validatePartDraft({ ...base(), name: '' }, false).ok).toBe(false)
  })

  it('新增时库存必须非负有效', () => {
    expect(validatePartDraft({ ...base(), stock: '-1' }, false)).toEqual({
      ok: false,
      msg: '库存和预警库存不能为负',
    })
    expect(validatePartDraft({ ...base(), stock: 'abc' }, false).ok).toBe(false)
    expect(validatePartDraft({ ...base(), warn: '-1' }, false).ok).toBe(false)
  })

  it('编辑时不校验库存 —— 库存不在编辑表单里', () => {
    // 库存只能走入库/盘点。编辑表单里没有这个输入框，
    // 因此无论原库存多少，编辑都不该因为库存报错。
    const r = validatePartDraft({ ...base(), stock: '-999', warn: '3' }, true)
    expect(r.ok).toBe(true)
  })

  it('编辑时仍校验预警值', () => {
    expect(validatePartDraft({ ...base(), warn: '-1' }, true)).toEqual({
      ok: false,
      msg: '预警库存必须是非负有效数值',
    })
  })

  it('空编码要当场拦，而不是等后端撞唯一约束', () => {
    // 后端 CreatePart 只验数量（service.go），空编码会直接写进库，
    // 第二个空编码才会撞 parts.code 唯一索引，报「零件编码已存在」——
    // 对第一个空编码的用户来说这句话毫无意义。
    expect(validatePartDraft({ ...base(), code: '' }, false).ok).toBe(false)
  })
})

describe('零件表单：转成请求体', () => {
  it('新增：空的可选字段落 undefined 而不是空串', () => {
    // 空串会让库里出现 ''，于是「有规格」和「规格是空的」再也分不开，
    // 列表里的空值判断也会失效。
    const b = draftToWrite({ ...emptyPartDraft(), code: 'P-1', name: '电阻' }, false)
    expect(b.spec).toBeUndefined()
    expect(b.supplier).toBeUndefined()
    expect(b.part_type).toBe('电子')
    expect(b.stock_qty).toBe(0)
    expect(b.status).toBe(1)
  })

  it('编辑：库存原样回传，不能当成 0', () => {
    const orig = {
      id: 7,
      code: 'P-1',
      name: '电阻',
      unit: '个',
      stock_qty: 1284,
      warn_qty: 100,
      status: 1,
      version: 3,
    }
    const b = draftToWrite(
      { ...partToDraft(orig), name: '电阻 改' },
      true,
      orig,
    )
    // 后端 PUT 是全量更新：漏掉 stock_qty 就会把 1284 清成 0。
    expect(b.stock_qty).toBe(1284)
    expect(b.id).toBe(7)
    // version 必须带，且必须是从 GET 读回来的那个，否则乐观锁直接失败
    expect(b.version).toBe(3)
  })

  it('编辑：停用状态被保留', () => {
    // Fyne 的产品/零件编辑框里那个「启用」复选框被创建后直接丢掉，
    // 于是编辑一个已停用的记录会把它悄悄启用。
    const orig = {
      id: 7,
      code: 'P-1',
      name: '电阻',
      unit: '个',
      stock_qty: 10,
      warn_qty: 0,
      status: 2,
      version: 1,
    }
    expect(draftToWrite(partToDraft(orig), true, orig).status).toBe(2)
  })
})

describe('零件表单：草稿回填', () => {
  it('库存与预警值的显示格式：整数不拖 .00', () => {
    const d = partToDraft({
      id: 1,
      code: 'P-1',
      name: '电阻',
      unit: '个',
      stock_qty: 1284,
      warn_qty: 100,
      status: 1,
      version: 1,
    })
    expect(d.stock).toBe('1284')
    expect(d.warn).toBe('100')
  })

  it('小数保留两位，与 Fyne 的 %.2f 一致', () => {
    const d = partToDraft({
      id: 1,
      code: 'P-1',
      name: '电阻',
      unit: '个',
      stock_qty: 1.5,
      warn_qty: 0.25,
      status: 1,
      version: 1,
    })
    expect(d.warn).toBe('0.25')
  })

  it('缺省的分类回填成空串而不是 undefined', () => {
    const d = partToDraft({
      id: 1,
      code: 'P-1',
      name: '电阻',
      unit: '个',
      stock_qty: 1,
      warn_qty: 0,
      status: 1,
      version: 1,
    })
    expect(d.partType).toBe('')
    expect(d.supplier).toBe('')
  })
})