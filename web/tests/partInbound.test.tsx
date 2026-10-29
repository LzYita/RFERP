// 入库批次的三处界面：入库弹窗（备注 + 自动批次号）、零件详情（只有剩余的
// 批次 + 二级弹窗里的已用完批次）、逐批次盘点弹窗。
//
// 关键行为：
//   1. 一级详情只列 remaining > 0 的批次；已用完的收在二级弹窗里；
//   2. 二级弹窗盖在详情之上，返回只收二级、不连带关掉详情；
//   3. 逐批次盘点每批一行，只提交改动过的批次，差异按各批之和显示。
// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { StockModal, BatchCountModal } from '../src/screens/stockForm'
import { countRowsOf, validateCounts } from '../src/lib/inboundCount'
import { PartDetailModal } from '../src/screens/partDetail'
import type { Part, PartInbound } from '../src/lib/catalog'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const part = (over: Partial<Part> = {}): Part =>
  ({
    id: 1,
    code: 'M-001',
    name: '加热管',
    unit: '个',
    stock_qty: 30,
    warn_qty: 25,
    status: 1,
    version: 1,
    ...over,
  }) as Part

const inbound = (over: Partial<PartInbound> = {}): PartInbound => ({
  id: 1,
  part_id: 1,
  batch_no: 'RK-20261001-0001',
  qty: 100,
  remaining: 100,
  note: '历史库存初始化',
  created_at: '2026-10-01T08:00:00Z',
  occupied: 0,
  ...over,
})

it('入库弹窗：备注选填，提交时一并带回，并提示自动批次号', async () => {
  const user = userEvent.setup()
  const onSubmit = vi.fn()
  render(<StockModal part={part()} mode="in" busy={false} err="" onSubmit={onSubmit} onClose={() => {}} />)

  expect(screen.getByText(/系统自动生成入库批次号/)).toBeTruthy()

  // FormRow 的 label 不与输入框做 id 关联，但可访问名由 label 文案组成；
  // 数量框取「入库数量」，备注靠占位符定位。
  await user.type(screen.getByRole('textbox', { name: /入库数量/ }), '5')
  await user.type(screen.getByPlaceholderText('如：供应商补货'), '供应商补货')
  await user.click(screen.getByRole('button', { name: '确定' }))

  expect(onSubmit).toHaveBeenCalledWith(5, '供应商补货')
})

function stubInbounds(list: unknown, fail = false) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url.startsWith('/api/parts/inbounds')) {
        if (fail) return new Response(JSON.stringify({ error: '数据库不可用' }), { status: 500 })
        return new Response(JSON.stringify(list))
      }
      return new Response('{}', { status: 404 })
    }),
  )
}

it('零件详情：一级只列仍有剩余的批次，并给出剩余合计', async () => {
  stubInbounds([
    inbound({ id: 3, batch_no: 'RK-20261020-0003', qty: 40, remaining: 25, note: '补货' }),
    inbound({ id: 2, batch_no: 'RK-20261010-0002', qty: 60, remaining: 0 }),
    inbound({ id: 1, batch_no: 'RK-20261001-0001', qty: 100, remaining: 0 }),
  ])
  render(<PartDetailModal part={part()} onClose={() => {}} />)

  // 尚有剩余的那批在，已用完的两批不在。
  expect(await screen.findByText('RK-20261020-0003')).toBeTruthy()
  expect(screen.queryByText('RK-20261010-0002')).toBeNull()
  expect(screen.queryByText('RK-20261001-0001')).toBeNull()

  // 摘要：现有（≡各批剩余之和）/ 在产占用 / 可用，三件事分开说。
  const summary = screen.getByTestId('part-detail-summary')
  expect(summary.textContent).toContain('现有')
  expect(summary.textContent).toContain('30') // part.stock_qty
  expect(summary.textContent).toContain('在产占用')
  expect(summary.textContent).toContain('可用')
  expect(summary.textContent).not.toContain('剩余合计') // 与"现有"恒等，不再重复
  expect(screen.getByText('仍有剩余的批次（1）')).toBeTruthy()
})

it('零件详情：二级弹窗列出已用完的批次，返回后回到一级', async () => {
  const user = userEvent.setup()
  stubInbounds([
    inbound({ id: 2, batch_no: 'RK-20261020-0002', qty: 30, remaining: 0, note: '第二批' }),
    inbound({ id: 1, batch_no: 'RK-20261001-0001', qty: 100, remaining: 0 }),
  ])
  render(<PartDetailModal part={part({ stock_qty: 0 })} onClose={() => {}} />)

  await user.click(await screen.findByRole('button', { name: '查看已用完批次（2）' }))
  const nested = await screen.findByRole('dialog', { name: /已用完批次/ })
  const inNested = within(nested)
  expect(inNested.getByText('RK-20261020-0002')).toBeTruthy()
  expect(inNested.getByText('RK-20261001-0001')).toBeTruthy()

  await user.click(inNested.getByRole('button', { name: '返回' }))
  expect(screen.queryByRole('dialog', { name: /已用完批次/ })).toBeNull()
  // 一级还在：返回只收二级，不连带关掉详情。
  expect(screen.getByRole('dialog', { name: /零件详情/ })).toBeTruthy()
})

it('零件详情：入口常驻，没有已用完批次时点开是空态', async () => {
  const user = userEvent.setup()
  stubInbounds([inbound({ id: 1, remaining: 10 })])
  render(<PartDetailModal part={part()} onClose={() => {}} />)
  expect(await screen.findByText('RK-20261001-0001')).toBeTruthy()

  // 只在有已用完批次时才出现的入口，会让"新批次全是剩余"的用户
  // 以为这个界面根本不存在。
  const entry = screen.getByRole('button', { name: '查看已用完批次（0）' })
  await user.click(entry)
  const nested = await screen.findByRole('dialog', { name: /已用完批次/ })
  expect(within(nested).getByText('还没有已用完的批次')).toBeTruthy()
})

it('零件详情不放盘点入口 —— 盘点只在零件列表', async () => {
  stubInbounds([inbound({ id: 1, remaining: 10 })])
  render(<PartDetailModal part={part()} onClose={() => {}} />)
  await screen.findByText('RK-20261001-0001')
  expect(screen.queryByRole('button', { name: '盘点' })).toBeNull()
})

it('零件详情：暂无入库记录', async () => {
  stubInbounds([])
  render(<PartDetailModal part={part({ stock_qty: 0 })} onClose={() => {}} />)

  expect(await screen.findByText('没有仍有剩余的入库批次')).toBeTruthy()
})

it('零件详情：加载失败把原因摆出来，不装作没有记录', async () => {
  stubInbounds(null, true)
  render(<PartDetailModal part={part()} onClose={() => {}} />)

  expect(await screen.findByText('数据库不可用')).toBeTruthy()
})

describe('逐批次盘点', () => {
  const rows = () =>
    countRowsOf([
      inbound({ id: 2, batch_no: 'RK-20261020-0002', qty: 30, remaining: 10 }),
      inbound({ id: 1, batch_no: 'RK-20261001-0001', qty: 100, remaining: 20 }),
    ])

  it('按批次顺序排列（旧批在前），并预填账面剩余', () => {
    const r = rows()
    expect(r.map((x) => x.inbound.id)).toEqual([1, 2])
    expect(r.map((x) => x.raw)).toEqual(['20', '10'])
  })

  it('只提交改动过的批次，差异取各批之和', () => {
    const r = rows()
    r[0].raw = '18'
    const v = validateCounts(r)
    expect(v.ok).toBe(true)
    if (v.ok) {
      expect(v.items).toEqual([{ inbound_id: 1, remaining: 18 }])
      expect(v.delta).toBe(-2)
    }
  })

  it('没改动时明确拒绝提交，而不是发一个空请求', () => {
    expect(validateCounts(rows())).toEqual({ ok: false, msg: '没有改动，无需提交' })
  })

  it('负数被拒', () => {
    const r = rows()
    r[1].raw = '-1'
    expect(validateCounts(r).ok).toBe(false)
  })

  it('盘到 0 是合法结果（这批确实用完了）', () => {
    const r = rows()
    r[0].raw = '0'
    const v = validateCounts(r)
    expect(v.ok).toBe(true)
    if (v.ok) expect(v.items).toEqual([{ inbound_id: 1, remaining: 0 }])
  })

  it('弹窗逐行显示批次，并实时汇总差异', async () => {
    const user = userEvent.setup()
    const list = rows()
    const onChange = vi.fn()
    const { rerender } = render(
      <BatchCountModal
        part={part()}
        rows={list}
        busy={false}
        err=""
        onChange={onChange}
        onSubmit={() => {}}
        onClose={() => {}}
      />,
    )

    expect(screen.getByText('RK-20261001-0001')).toBeTruthy()
    expect(screen.getByText('RK-20261020-0002')).toBeTruthy()
    // 账面合计 30、实盘合计 30、差异 0。
    expect(screen.getByText(/账面合计/).closest('div')!.textContent).toContain('30')

    const input = screen.getByLabelText('批次 RK-20261001-0001 的实盘数量')
    await user.clear(input)
    await user.type(input, '25')
    expect(onChange).toHaveBeenCalled()
    // 受控组件：把 onChange 产生的结果渲染回去——账面 20 → 实盘 25，差异 +5。
    const next = list.map((r) => (r.inbound.id === 1 ? { ...r, raw: '25' } : r))
    rerender(
      <BatchCountModal
        part={part()}
        rows={next}
        busy={false}
        err=""
        onChange={onChange}
        onSubmit={() => {}}
        onClose={() => {}}
      />,
    )
    const after = screen.getByText(/账面合计/).closest('div')!.textContent!
    expect(after).toContain('实盘合计')
    expect(after).toContain('+5')
  })

  it('没有批次时说明要先入库', () => {
    render(
      <BatchCountModal
        part={part({ stock_qty: 0 })}
        rows={[]}
        busy={false}
        err=""
        onChange={() => {}}
        onSubmit={() => {}}
        onClose={() => {}}
      />,
    )
    expect(screen.getByText(/还没有入库批次，无法盘点/)).toBeTruthy()
  })
})
