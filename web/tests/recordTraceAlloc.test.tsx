// 记录投料：每零件一张卡片，用量只读，批次数量可手改但合计必须等于用量；
// 默认按入库时间由远到近；「按顺序消耗」恢复默认摊法。
// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { RecordTraceModal } from '../src/screens/batchForms'
import type { BOMItem, PartInbound } from '../src/lib/catalog'
import type { BatchTrace } from '../src/lib/batch'

afterEach(() => {
  cleanup()
})

const bomItem = (over: Partial<BOMItem> = {}): BOMItem => ({
  id: 1,
  product_id: 10,
  part_id: 1,
  quantity: 2,
  loss_rate: 0,
  replaceable: 1,
  use_mode: 0,
  version: 1,
  part_code: 'M-001',
  part_name: '加热管',
  ...over,
})

const inbound = (over: Partial<PartInbound> = {}): PartInbound => ({
  id: 1,
  part_id: 1,
  batch_no: 'RK-20261001-0001',
  qty: 100,
  remaining: 100,
  created_at: '2026-10-01T08:00:00Z',
  occupied: 0,
  ...over,
})

function card(dialog: HTMLElement, code = 'M-001'): HTMLElement {
  const el = dialog.querySelector(`[data-part-code="${code}"]`)
  if (!el) throw new Error(`找不到零件卡片 ${code}`)
  return el as HTMLElement
}

function renderModal(opts: {
  planQty?: number
  inbounds?: Map<number, PartInbound[]>
  existing?: Map<number, BatchTrace>
  onSubmit?: (rows: unknown[]) => void
}) {
  const onSubmit = vi.fn(opts.onSubmit)
  render(
    <RecordTraceModal
      batchNo="B-1"
      planQty={opts.planQty ?? 2}
      items={[bomItem()]}
      skipped={new Set()}
      temps={[]}
      inbounds={opts.inbounds ?? new Map()}
      existing={opts.existing ?? new Map()}
      busy={false}
      err=""
      onSubmit={onSubmit}
      onClose={vi.fn()}
    />,
  )
  return { onSubmit, dialog: screen.getByRole('dialog') }
}

it('用量只读，默认按入库时间由远到近摊分', () => {
  const { dialog } = renderModal({
    planQty: 3, // 每台 2 个 → 用量 6
    inbounds: new Map([
      [
        1,
        [
          // id 小的反而是较新的入库：默认顺序要看时间，不能看 id。
          inbound({ id: 1, batch_no: 'RK-NEW', remaining: 10, created_at: '2026-10-20T08:00:00Z' }),
          inbound({ id: 2, batch_no: 'RK-OLD', remaining: 4, created_at: '2026-09-01T08:00:00Z' }),
        ],
      ],
    ]),
  })
  const body = card(dialog)
  expect(body.textContent).toContain('用量 6')
  expect(within(body).queryByPlaceholderText('供应商')).toBeNull() // 供应商取自零件台账

  const qty = (label: string) => (within(body).getByLabelText(label) as HTMLInputElement).value
  expect(qty('RK-OLD 本次消耗')).toBe('4')
  expect(qty('RK-NEW 本次消耗')).toBe('2')
  expect(body.textContent).toContain('合计 6 / 用量 6')
})

it('手改数量后合计不等就拦下保存，点「按顺序消耗」恢复', async () => {
  const user = userEvent.setup()
  const onSubmit = vi.fn()
  const { dialog } = renderModal({
    planQty: 3,
    inbounds: new Map([
      [
        1,
        [
          inbound({ id: 2, batch_no: 'RK-OLD', remaining: 4, created_at: '2026-09-01T08:00:00Z' }),
          inbound({ id: 1, batch_no: 'RK-NEW', remaining: 10, created_at: '2026-10-20T08:00:00Z' }),
        ],
      ],
    ]),
    onSubmit,
  })
  const body = card(dialog)
  const input = within(body).getByLabelText('RK-OLD 本次消耗') as HTMLInputElement
  await user.clear(input)
  await user.type(input, '3')
  expect(body.textContent).toContain('还差 1')

  await user.click(within(dialog).getByRole('button', { name: '确定' }))
  expect(onSubmit).not.toHaveBeenCalled()
  expect(screen.getByRole('alert').textContent).toContain('还差 1')

  await user.click(within(body).getByRole('button', { name: 'M-001 按顺序消耗' }))
  expect((within(body).getByLabelText('RK-OLD 本次消耗') as HTMLInputElement).value).toBe('4')
  await user.click(within(dialog).getByRole('button', { name: '确定' }))
  expect(onSubmit).toHaveBeenCalledTimes(1)
})

it('手改数量后合计仍等于用量，提交带上有序分配', async () => {
  const user = userEvent.setup()
  const onSubmit = vi.fn()
  const { dialog } = renderModal({
    planQty: 3,
    inbounds: new Map([
      [
        1,
        [
          inbound({ id: 2, batch_no: 'RK-OLD', remaining: 4, created_at: '2026-09-01T08:00:00Z' }),
          inbound({ id: 1, batch_no: 'RK-NEW', remaining: 10, created_at: '2026-10-20T08:00:00Z' }),
        ],
      ],
    ]),
    onSubmit,
  })
  const body = card(dialog)
  const old = within(body).getByLabelText('RK-OLD 本次消耗') as HTMLInputElement
  const fresh = within(body).getByLabelText('RK-NEW 本次消耗') as HTMLInputElement
  await user.clear(old)
  await user.type(old, '1')
  await user.clear(fresh)
  await user.type(fresh, '5')

  await user.click(within(dialog).getByRole('button', { name: '确定' }))
  expect(onSubmit.mock.calls[0][0]).toEqual([
    {
      partId: 1,
      usedQty: 6,
      allocations: [
        { inbound_id: 2, qty: 1 },
        { inbound_id: 1, qty: 5 },
      ],
    },
  ])
})

it('小数用量保留两位，不被取整', async () => {
  const onSubmit = vi.fn()
  render(
    <RecordTraceModal
      batchNo="B-1"
      planQty={3}
      items={[bomItem({ quantity: 1.5 })]} // 3 台 × 1.5 = 4.5
      skipped={new Set()}
      temps={[]}
      inbounds={new Map([[1, [inbound({ id: 1, batch_no: 'RK-ONE', remaining: 10 })]]])}
      existing={new Map()}
      busy={false}
      err=""
      onSubmit={onSubmit}
      onClose={vi.fn()}
    />,
  )
  const dialog = screen.getByRole('dialog')
  const body = card(dialog)
  expect(body.textContent).toContain('用量 4.5')
  expect((within(body).getByLabelText('RK-ONE 本次消耗') as HTMLInputElement).value).toBe('4.5')
  expect(body.textContent).toContain('合计 4.5 / 用量 4.5')
  userEvent.click(within(dialog).getByRole('button', { name: '确定' }))
  return waitFor(() =>
    expect(onSubmit).toHaveBeenCalledWith([
      { partId: 1, usedQty: 4.5, allocations: [{ inbound_id: 1, qty: 4.5 }] },
    ]),
  )
})

it('单批零件输入超过可用量会被封顶，不会出现负数或超量', async () => {
  const user = userEvent.setup()
  const { dialog } = renderModal({
    planQty: 1, // 用量 2
    inbounds: new Map([[1, [inbound({ id: 1, batch_no: 'RK-ONE', remaining: 2 })]]]),
  })
  const body = card(dialog)
  const input = within(body).getByLabelText('RK-ONE 本次消耗') as HTMLInputElement
  await user.clear(input)
  await user.type(input, '99')
  expect(input.value).toBe('2')
  await user.clear(input)
  await user.type(input, '3')
  expect(input.value).toBe('2')
})

it('打开时回显已保存的顺序与数量', () => {
  const { dialog } = renderModal({
    planQty: 2, // 用量 4
    inbounds: new Map([
      [
        1,
        [
          inbound({ id: 2, batch_no: 'RK-OLD', remaining: 10, created_at: '2026-09-01T08:00:00Z' }),
          inbound({ id: 1, batch_no: 'RK-NEW', remaining: 10, created_at: '2026-10-20T08:00:00Z' }),
        ],
      ],
    ]),
    existing: new Map([
      [
        1,
        {
          id: 9,
          batch_id: 1,
          part_id: 1,
          used_qty: 4,
          allocations: [
            { inbound_id: 1, batch_no: 'RK-NEW', qty: 3, position: 0 },
            { inbound_id: 2, batch_no: 'RK-OLD', qty: 1, position: 1 },
          ],
        },
      ],
    ]),
  })
  const body = card(dialog)
  expect((within(body).getByLabelText('RK-NEW 本次消耗') as HTMLInputElement).value).toBe('3')
  expect((within(body).getByLabelText('RK-OLD 本次消耗') as HTMLInputElement).value).toBe('1')
  // 行序也沿用保存时的顺序：RK-NEW 在 RK-OLD 之前。
  const text = body.textContent ?? ''
  expect(text.indexOf('RK-NEW')).toBeLessThan(text.indexOf('RK-OLD'))
})

it('可用量不够时直接拦住保存，并提示先入库', async () => {
  const user = userEvent.setup()
  const onSubmit = vi.fn()
  const { dialog } = renderModal({
    planQty: 10, // 用量 20，但只有 5
    inbounds: new Map([[1, [inbound({ id: 1, batch_no: 'RK-ONE', remaining: 5 })]]]),
    onSubmit,
  })
  const body = card(dialog)
  expect(body.textContent).toContain('库存不足')
  await user.click(within(dialog).getByRole('button', { name: '确定' }))
  expect(onSubmit).not.toHaveBeenCalled()
  expect(screen.getByRole('alert').textContent).toContain('库存不足')
})

it('拖过顺序后点「按顺序消耗」按当前顺序重算，而不是回到入库时间序', async () => {
  const user = userEvent.setup()
  const { dialog } = renderModal({
    planQty: 1, // 用量 2
    inbounds: new Map([
      [
        1,
        [
          inbound({ id: 1, batch_no: 'RK-OLD', remaining: 10, created_at: '2026-09-01T08:00:00Z' }),
          inbound({ id: 2, batch_no: 'RK-NEW', remaining: 10, created_at: '2026-10-20T08:00:00Z' }),
        ],
      ],
    ]),
  })
  const body = card(dialog)
  const oldQty = () => (within(body).getByLabelText('RK-OLD 本次消耗') as HTMLInputElement).value
  const newQty = () => (within(body).getByLabelText('RK-NEW 本次消耗') as HTMLInputElement).value
  expect(oldQty()).toBe('2')
  expect(newQty()).toBe('0')

  // 把 RK-NEW 上移：数量不动，仍是 2 / 0。
  await user.click(within(body).getByRole('button', { name: '上移批次 RK-NEW' }))
  expect(oldQty()).toBe('2')
  expect(newQty()).toBe('0')

  // 按当前顺序重算：RK-NEW 在前，应吃掉全部 2。
  await user.click(within(body).getByRole('button', { name: 'M-001 按顺序消耗' }))
  expect(newQty()).toBe('2')
  expect(oldQty()).toBe('0')
})
