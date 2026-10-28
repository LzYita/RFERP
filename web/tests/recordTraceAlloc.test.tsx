// 记录投料：每零件可排多个入库批次，顺序可调，右侧显示消耗后剩余；
// 打开时回显上次保存的记录；提交带分配（服务端按顺序扣）。
// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
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

it('按入库顺序摊用量，并显示每批的消耗与消耗后剩余', async () => {
  const user = userEvent.setup()
  const { dialog } = renderModal({
    planQty: 3, // BOM: 每台 2 个 → 默认用量 6
    inbounds: new Map([
      [
        1,
        [
          inbound({ id: 2, batch_no: 'RK-B', remaining: 10 }),
          inbound({ id: 1, batch_no: 'RK-A', remaining: 4 }),
        ],
      ],
    ]),
  })

  const rowText = (batchNo: string) =>
    within(dialog)
      .getByText(batchNo)
      .closest('tr')!.textContent!.replace(/\s+/g, ' ')

  // 默认用量 = 3 台 × 2 = 6；先扣先入库的 RK-A（4），再扣 RK-B（2）。
  const usage = within(dialog).getAllByRole('textbox')[0] as HTMLInputElement
  expect(usage.value).toBe('6')
  expect(rowText('RK-A')).toContain('消耗 4')
  expect(rowText('RK-A')).toContain('剩余 0')
  expect(rowText('RK-B')).toContain('消耗 2')
  expect(rowText('RK-B')).toContain('剩余 8')

  // 上移 RK-B：现在它先扣，6 个全从它出。
  await user.click(within(dialog).getByRole('button', { name: '上移批次 RK-B' }))
  expect(rowText('RK-B')).toContain('消耗 6')
  expect(rowText('RK-B')).toContain('剩余 4')
  expect(rowText('RK-A')).toContain('消耗 0')
})

it('提交时带上有序的批次分配', async () => {
  const user = userEvent.setup()
  const onSubmit = vi.fn()
  const { dialog } = renderModal({
    planQty: 3,
    inbounds: new Map([
      [
        1,
        [
          inbound({ id: 2, batch_no: 'RK-B', remaining: 10 }),
          inbound({ id: 1, batch_no: 'RK-A', remaining: 4 }),
        ],
      ],
    ]),
    onSubmit,
  })

  await user.click(within(dialog).getByRole('button', { name: '确定' }))
  expect(onSubmit).toHaveBeenCalledTimes(1)
  expect(onSubmit.mock.calls[0][0]).toEqual([
    {
      partId: 1,
      usedQty: 6,
      supplier: '',
      allocations: [
        { inbound_id: 1, qty: 4 },
        { inbound_id: 2, qty: 2 },
      ],
    },
  ])
})

it('打开时回显已保存的用量与批次顺序', () => {
  const { dialog } = renderModal({
    inbounds: new Map([
      [
        1,
        [
          inbound({ id: 1, batch_no: 'RK-A', remaining: 10 }),
          inbound({ id: 2, batch_no: 'RK-B', remaining: 10 }),
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
          used_qty: 5,
          supplier: '老供应商',
          allocations: [
            { inbound_id: 2, batch_no: 'RK-B', qty: 3, position: 0 },
            { inbound_id: 1, batch_no: 'RK-A', qty: 2, position: 1 },
          ],
        },
      ],
    ]),
  })

  expect((within(dialog).getAllByRole('textbox')[0] as HTMLInputElement).value).toBe('5')
  expect(within(dialog).getByDisplayValue('老供应商')).toBeTruthy()
  // 保存过的顺序优先：RK-B 排第一，5 个用量全部从它出。
  const rows = within(dialog).getAllByText(/^RK-/)
  expect(rows.map((r) => r.textContent)).toEqual(['RK-B', 'RK-A'])
  const first = rows[0].closest('tr')!.textContent!.replace(/\s+/g, ' ')
  expect(first).toContain('消耗 5')
})

it('用量超过可用批次时提示差额走先进先出', () => {
  const { dialog } = renderModal({
    planQty: 10,
    inbounds: new Map([[1, [inbound({ id: 1, batch_no: 'RK-A', remaining: 5 })]]]),
  })
  expect(within(dialog).getByText(/还差 15 将按先进先出扣/)).toBeTruthy()
})
