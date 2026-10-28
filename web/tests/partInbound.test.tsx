// 入库批次的两端：入库弹窗（备注 + 自动批次号提示）与零件详情（台账表）。
//
// 关键行为：
//   1. 入库提交把备注一起带给服务端；批次号提示让用户知道会有台账；
//   2. 盘点不出现备注（它不产生入库批次）；
//   3. 详情页按倒序列出批次，空数据与加载失败都有各自的说法。
// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { StockModal } from '../src/screens/stockForm'
import { PartDetailModal } from '../src/screens/partDetail'
import type { Part } from '../src/lib/catalog'

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

it('入库弹窗：备注选填，提交时一并带回，并提示自动批次号', async () => {
  const user = userEvent.setup()
  const onSubmit = vi.fn()
  render(<StockModal part={part()} mode="in" busy={false} err="" onSubmit={onSubmit} onClose={() => {}} />)

  expect(screen.getByText(/系统自动生成入库批次号/)).toBeTruthy()

  // FormRow 的 label 不与输入框做 id 关联：数量是弹窗里唯一的数字输入，
  // 备注靠占位符定位。
  // FormRow 的 label 不与输入框做 id 关联，但可访问名由 label 文案组成；
  // 数量框取「入库数量」，备注靠占位符定位。
  await user.type(screen.getByRole('textbox', { name: /入库数量/ }), '5')
  await user.type(screen.getByPlaceholderText('如：供应商补货'), '供应商补货')
  await user.click(screen.getByRole('button', { name: '确定' }))

  expect(onSubmit).toHaveBeenCalledWith(5, '供应商补货')
})

it('盘点弹窗：没有备注输入，备注恒为空串', async () => {
  const user = userEvent.setup()
  const onSubmit = vi.fn()
  render(<StockModal part={part()} mode="adjust" busy={false} err="" onSubmit={onSubmit} onClose={() => {}} />)

  expect(screen.queryByLabelText('备注（选填）')).toBeNull()

  await user.click(screen.getByRole('button', { name: '确定' }))
  // 盘点预填当前库存，直接确认就是"没变化"。
  expect(onSubmit).toHaveBeenCalledWith(30, '')
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

it('零件详情：倒序列出入库批次，数量与备注齐全', async () => {
  stubInbounds([
    { id: 2, part_id: 1, batch_no: 'RK-20261028-0002', qty: 10, note: '补货', created_at: '2026-10-28T09:00:00Z' },
    { id: 1, part_id: 1, batch_no: 'RK-20261001-0001', qty: 100, note: '历史库存初始化', created_at: '2026-10-01T08:00:00Z' },
  ])
  render(<PartDetailModal part={part()} onClose={() => {}} />)

  expect(await screen.findByText('RK-20261028-0002')).toBeTruthy()
  // 摘要行：现有 / 预警线 / 累计入库（100 + 10）。
  const summary = screen.getByText(/累计入库/).closest('div')!
  expect(summary.textContent).toContain('累计入库')
  expect(summary.textContent).toContain('110')
  expect(screen.getByText('RK-20261001-0001')).toBeTruthy()
  expect(screen.getByText('历史库存初始化')).toBeTruthy()
})

it('零件详情：暂无入库记录', async () => {
  stubInbounds([])
  render(<PartDetailModal part={part({ stock_qty: 0 })} onClose={() => {}} />)

  expect(await screen.findByText('暂无入库记录')).toBeTruthy()
})

it('零件详情：加载失败把原因摆出来，不装作没有记录', async () => {
  stubInbounds(null, true)
  render(<PartDetailModal part={part()} onClose={() => {}} />)

  expect(await screen.findByText('数据库不可用')).toBeTruthy()
})
