// 零件管理弹窗（跳过/临时两个分页）与记录投料的联动。
//
// 关键行为：
//   1. 「零件管理」一个入口、两个分页；分页切换不改已勾选的跳过集；
//   2. 临时零件逐条立即生效，后端拒绝（库存不足）要在弹窗里显示原文；
//   3. 记录投料：跳过的零件行禁用且不提交，临时零件作为新增行出现。
// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PartsManagerModal, RecordTraceModal } from '../src/screens/batchForms'
import { BatchScreen } from '../src/screens/BatchScreen'
import type { BOMItem, Part } from '../src/lib/catalog'
import type { TempPart } from '../src/lib/batch'

vi.mock('../src/lib/api', async (original) => ({
  ...(await original<typeof import('../src/lib/api')>()),
  usePermissions: () => ({ modules: { batch: 'write' } }),
}))

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
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

const part = (over: Partial<Part> = {}): Part =>
  ({
    id: 1,
    code: 'M-001',
    name: '加热管',
    unit: '个',
    stock_qty: 50,
    warn_qty: 10,
    status: 1,
    version: 1,
    ...over,
  }) as Part

const tempPart = (over: Partial<TempPart> = {}): TempPart => ({
  batch_id: 1,
  part_id: 2,
  qty: 5,
  code: 'M-002',
  name: '备用螺丝',
  unit: '个',
  ...over,
})

function renderManager(over: {
  items?: BOMItem[]
  temps?: TempPart[]
  skipped?: Set<number>
  onSaveSkips?: (next: Set<number>) => void
  onAddTemp?: (partId: number, qty: number) => Promise<void>
  onRemoveTemp?: (partId: number) => Promise<void>
} = {}) {
  const props = {
    batchNo: 'B-1',
    items: over.items ?? [bomItem()],
    parts: [part(), part({ id: 2, code: 'M-002', name: '备用螺丝', stock_qty: 8 })],
    skipped: over.skipped ?? new Set<number>(),
    temps: over.temps ?? [],
    busy: false,
    onSaveSkips: over.onSaveSkips ?? vi.fn(),
    onAddTemp: over.onAddTemp ?? vi.fn(async () => {}),
    onRemoveTemp: over.onRemoveTemp ?? vi.fn(async () => {}),
    onClose: vi.fn(),
  }
  render(<PartsManagerModal {...props} />)
  return props
}

it('keeps two tabs and preserves the skip set while switching', async () => {
  const u = userEvent.setup()
  const props = renderManager({ skipped: new Set([1]) })
  const dialog = screen.getByRole('dialog')

  // 默认落在跳过分页，且已跳过的零件带勾选
  expect(within(dialog).getByRole('tab', { name: /跳过零件/ }).getAttribute('aria-selected')).toBe('true')
  const checkbox = within(dialog).getByRole('checkbox') as HTMLInputElement
  expect(checkbox.checked).toBe(true)

  // 切到临时分页再切回来：勾选状态不丢
  await u.click(within(dialog).getByRole('tab', { name: /临时零件/ }))
  expect(within(dialog).getByRole('tab', { name: /临时零件/ }).getAttribute('aria-selected')).toBe('true')
  await u.click(within(dialog).getByRole('tab', { name: /跳过零件/ }))
  expect(((within(dialog).getByRole('checkbox') as HTMLInputElement).checked)).toBe(true)

  // 保存提交的是勾选集本身，父组件负责算差集
  await u.click(within(dialog).getByRole('button', { name: '保存' }))
  expect(props.onSaveSkips).toHaveBeenCalledWith(new Set([1]))
})

it('adds a temp part with its quantity and shows backend refusals verbatim', async () => {
  const u = userEvent.setup()
  const onAddTemp = vi.fn(async () => {})
  renderManager({ onAddTemp })
  const dialog = screen.getByRole('dialog')

  await u.click(within(dialog).getByRole('tab', { name: /临时零件/ }))
  await u.selectOptions(within(dialog).getByLabelText('临时零件'), '2')
  await u.type(within(dialog).getByPlaceholderText('数量'), '3')
  await u.click(within(dialog).getByRole('button', { name: '添加' }))

  await waitFor(() => expect(onAddTemp).toHaveBeenCalledWith(2, 3))
})

it('shows the stock-shortage refusal from the server instead of closing silently', async () => {
  const u = userEvent.setup()
  const onAddTemp = vi.fn(async () => {
    throw new Error('备用螺丝 [M-002] 临时需要 99.00，全部在产批次完成后将缺 91.00')
  })
  renderManager({ onAddTemp })
  const dialog = screen.getByRole('dialog')

  await u.click(within(dialog).getByRole('tab', { name: /临时零件/ }))
  await u.selectOptions(within(dialog).getByLabelText('临时零件'), '2')
  await u.type(within(dialog).getByPlaceholderText('数量'), '99')
  await u.click(within(dialog).getByRole('button', { name: '添加' }))

  expect(await within(dialog).findByText(/将缺 91.00/)).toBeTruthy()
})

it('removes a temp part immediately', async () => {
  const u = userEvent.setup()
  const onRemoveTemp = vi.fn(async () => {})
  renderManager({ temps: [tempPart()], onRemoveTemp })
  const dialog = screen.getByRole('dialog')

  await u.click(within(dialog).getByRole('tab', { name: /临时零件/ }))
  expect(within(dialog).getByText('M-002')).toBeTruthy()
  await u.click(within(dialog).getByRole('button', { name: '移除' }))
  await waitFor(() => expect(onRemoveTemp).toHaveBeenCalledWith(2))
})

it('lists skipped BOM parts as disabled rows and temp parts as new rows in 记录投料', async () => {
  const onSubmit = vi.fn()
  render(
    <RecordTraceModal
      batchNo="B-1"
      planQty={10}
      items={[bomItem(), bomItem({ id: 2, part_id: 3, part_code: 'M-003', part_name: '彩盒', quantity: 1 })]}
      skipped={new Set([1])}
      temps={[tempPart()]}
      busy={false}
      err=""
      onSubmit={onSubmit}
      onClose={vi.fn()}
    />,
  )
  const dialog = screen.getByRole('dialog')

  // 跳过行有标记且输入禁用；临时行带「临时」标记且预填数量
  const skippedInput = within(dialog).getByDisplayValue('20') as HTMLInputElement // 10 × 2
  expect(skippedInput.disabled).toBe(true)
  expect(within(dialog).getByText('已跳过')).toBeTruthy()
  expect(within(dialog).getByDisplayValue('5')).toBeTruthy() // 临时零件数量
  expect(within(dialog).getByText('临时')).toBeTruthy()

  // 提交时跳过行不进入结果；临时行照常提交（把预填 5 改成 50 一起交）
  await userEvent.type(within(dialog).getByDisplayValue('5'), '0')
  await userEvent.click(within(dialog).getByRole('button', { name: '确定' }))
  await waitFor(() =>
    expect(onSubmit).toHaveBeenCalledWith([
      { partId: 3, usedQty: 10, partBatchNo: '', supplier: '' },
      { partId: 2, usedQty: 50, partBatchNo: '', supplier: '' },
    ]),
  )
})

it('orders the batch action buttons as requested', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url.startsWith('/api/batches')) return new Response(JSON.stringify([]))
      if (url.startsWith('/api/products')) return new Response(JSON.stringify([]))
      if (url.startsWith('/api/parts')) return new Response(JSON.stringify([]))
      if (url.startsWith('/api/stock/projection'))
        return new Response(JSON.stringify({ parts: [], batches: [], summary: { willWarn: 0, willShort: 0 } }))
      return new Response('{}', { status: 404 })
    }),
  )
  render(<BatchScreen />)
  await screen.findByText('生产批次')

  const bar = document.querySelector('.ml-auto.flex') as HTMLElement
  const labels = Array.from(bar.querySelectorAll('button')).map((b) => b.textContent)
  expect(labels).toEqual(['新建批次', '零件管理', '记录投料', '开始生产', '完成生产', '撤销'])
})
