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
import type { PartProjection } from '../src/lib/projection'

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

type AddDerivation = { useMode: number; quantity: number; lossRate: number }

/** 走完"文字查询 → 点选零件"的前置步骤。按编码定位，避免撞上 ComboInput
 * 把输入原文预置成的那条候选项。 */
async function pickPart(u: ReturnType<typeof userEvent.setup>, scope: HTMLElement, code: string) {
  const input = within(scope).getByRole('combobox', { name: '临时零件' })
  await u.click(input)
  await u.type(input, code)
  // ComboInput 会把输入原文也预置成一条候选项（aria-selected），真正要点的
  // 是带名称与库存的那条完整标签。
  await u.click(within(scope).getByRole('option', { name: new RegExp(`${code} .*现有`) }))
}

function renderManager(over: {
  items?: BOMItem[]
  temps?: TempPart[]
  skipped?: Set<number>
  planQty?: number
  projection?: ReadonlyMap<number, PartProjection>
  projectionFailed?: boolean
  onSaveSkips?: (next: Set<number>) => void
  onAddTemp?: (partId: number, qty: number, derivation?: AddDerivation) => Promise<void>
  onRemoveTemp?: (partId: number) => Promise<void>
} = {}) {
  const props = {
    batchNo: 'B-1',
    planQty: over.planQty ?? 60,
    items: over.items ?? [bomItem()],
    parts: [part(), part({ id: 2, code: 'M-002', name: '备用螺丝', stock_qty: 8 })],
    skipped: over.skipped ?? new Set<number>(),
    temps: over.temps ?? [],
    projection: over.projection,
    projectionFailed: over.projectionFailed ?? false,
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

it('adds a temp part via text search with a manually overridden total', async () => {
  const u = userEvent.setup()
  const onAddTemp = vi.fn(async () => {})
  renderManager({ onAddTemp })
  const dialog = screen.getByRole('dialog')

  await u.click(within(dialog).getByRole('tab', { name: /临时零件/ }))
  await pickPart(u, dialog, 'M-002')
  // 手填总量 3：不再提交推导依据（后端存 NULL，列表显示"手填总量"）
  const total = within(dialog).getByPlaceholderText('自动') as HTMLInputElement
  await u.type(total, '3')
  // 手填前先清掉自动值：全选再输入
  await u.clear(total)
  await u.type(total, '3')
  await u.click(within(dialog).getByRole('button', { name: '添加' }))

  await waitFor(() => expect(onAddTemp).toHaveBeenCalledWith(2, 3, undefined))
})

it('computes the total from mode, per-unit quantity and loss rate automatically', async () => {
  const u = userEvent.setup()
  const onAddTemp = vi.fn(async () => {})
  renderManager({ onAddTemp, planQty: 60 })
  const dialog = screen.getByRole('dialog')

  await u.click(within(dialog).getByRole('tab', { name: /临时零件/ }))
  await pickPart(u, dialog, 'M-002')
  // 默认 每台 1 个、无损耗 × 计划 60 → 总量自动 60
  const total = within(dialog).getByPlaceholderText('自动') as HTMLInputElement
  expect(total.value).toBe('60')
  await u.click(within(dialog).getByRole('button', { name: '添加' }))

  await waitFor(() =>
    expect(onAddTemp).toHaveBeenCalledWith(2, 60, { useMode: 0, quantity: 1, lossRate: 0 }),
  )
})

it('recalculates with the BOM formula when mode/quantity/loss change', async () => {
  const u = userEvent.setup()
  const onAddTemp = vi.fn(async () => {})
  renderManager({ onAddTemp, planQty: 25 })
  const dialog = screen.getByRole('dialog')

  await u.click(within(dialog).getByRole('tab', { name: /临时零件/ }))
  await pickPart(u, dialog, 'M-002')
  // 每 4 台用 1 个、损耗 10%、计划 25 → ceil(25/4)=7 → ×1.1 → ceil = 8
  await u.selectOptions(within(dialog).getByLabelText('使用方式'), '1')
  const per = within(dialog).getByPlaceholderText('台数')
  await u.clear(per)
  await u.type(per, '4')
  const loss = within(dialog).getByPlaceholderText('0')
  await u.clear(loss)
  await u.type(loss, '10')
  const total = within(dialog).getByPlaceholderText('自动') as HTMLInputElement
  expect(total.value).toBe('8')
  await u.click(within(dialog).getByRole('button', { name: '添加' }))

  await waitFor(() =>
    expect(onAddTemp).toHaveBeenCalledWith(2, 8, { useMode: 1, quantity: 4, lossRate: 10 }),
  )
})

it('rejects an out-of-range loss rate before it reaches the server', async () => {
  const u = userEvent.setup()
  const onAddTemp = vi.fn(async () => {})
  renderManager({ onAddTemp })
  const dialog = screen.getByRole('dialog')

  await u.click(within(dialog).getByRole('tab', { name: /临时零件/ }))
  await pickPart(u, dialog, 'M-002')
  const loss = within(dialog).getByPlaceholderText('0')
  await u.clear(loss)
  await u.type(loss, '150')
  await u.click(within(dialog).getByRole('button', { name: '添加' }))

  expect(within(dialog).getByText('损耗率必须在 0 到 100 之间')).toBeTruthy()
  expect(onAddTemp).not.toHaveBeenCalled()
})

it('shows the stock-shortage refusal from the server instead of closing silently', async () => {
  const u = userEvent.setup()
  const onAddTemp = vi.fn(async () => {
    throw new Error('备用螺丝 [M-002] 临时需要 99.00，全部在产批次完成后将缺 91.00')
  })
  renderManager({ onAddTemp, planQty: 60 })
  const dialog = screen.getByRole('dialog')

  await u.click(within(dialog).getByRole('tab', { name: /临时零件/ }))
  await pickPart(u, dialog, 'M-002')
  // 手填 99（清掉自动的 60）
  const total = within(dialog).getByPlaceholderText('自动') as HTMLInputElement
  await u.clear(total)
  await u.type(total, '99')
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

it('explains how each temp quantity was derived in the list', async () => {
  renderManager({
    temps: [
      tempPart({ use_mode: 0, quantity: 2, loss_rate: 5 }),
      tempPart({ part_id: 3, code: 'M-003', name: '垫片', qty: 10 }),
    ],
  })
  const dialog = screen.getByRole('dialog')
  await userEvent.click(within(dialog).getByRole('tab', { name: /临时零件/ }))

  expect(within(dialog).getByText('每台 2 个，损耗 5%')).toBeTruthy()
  expect(within(dialog).getByText('手填总量')).toBeTruthy()
})

it('shows existing and available stock together when picking a temp part', async () => {
  const u = userEvent.setup()
  const projection = new Map<number, PartProjection>([
    [2, { part_id: 2, stock: 8, warn: 10, committed: 3, after: 5 }],
  ])
  renderManager({ projection })
  const dialog = screen.getByRole('dialog')
  await u.click(within(dialog).getByRole('tab', { name: /临时零件/ }))
  const input = within(dialog).getByRole('combobox', { name: '临时零件' })
  await u.click(input)
  await u.type(input, 'M-002')

  // 现有来自零件表，可用来自投影（现有 − 全部在产占用），两个都要给。
  expect(
    within(dialog).getByRole('option', { name: /M-002 备用螺丝（现有 8，可用 5 个）/ }),
  ).toBeTruthy()
})

it('marks available stock unknown instead of showing existing stock when projection fails', async () => {
  const u = userEvent.setup()
  renderManager({ projectionFailed: true })
  const dialog = screen.getByRole('dialog')
  await u.click(within(dialog).getByRole('tab', { name: /临时零件/ }))
  const input = within(dialog).getByRole('combobox', { name: '临时零件' })
  await u.click(input)
  await u.type(input, 'M-002')

  expect(within(dialog).getByRole('option', { name: /可用 —/ })).toBeTruthy()
  expect(within(dialog).getByText(/在产占用加载失败，可用库存暂不可用/)).toBeTruthy()
})

it('lets the user dismiss the batch warning banner', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url.startsWith('/api/batches')) {
        return new Response(
          JSON.stringify([
            { id: 1, batch_no: 'B-REV', product_id: 1, plan_qty: 10, produced_qty: 0, status: 4, version: 1 },
          ]),
        )
      }
      if (url.startsWith('/api/products')) return new Response(JSON.stringify([]))
      if (url.startsWith('/api/parts')) return new Response(JSON.stringify([]))
      if (url.startsWith('/api/stock/projection')) {
        return new Response(JSON.stringify({ parts: [], batches: [], summary: { willWarn: 0, willShort: 0 } }))
      }
      return new Response('{}', { status: 404 })
    }),
  )
  const u = userEvent.setup()
  render(<BatchScreen />)
  await screen.findByText('B-REV')

  await u.click(screen.getByRole('checkbox', { name: '选择 B-REV' }))
  await u.click(screen.getByRole('button', { name: '开始生产' }))
  await u.click(await screen.findByRole('button', { name: '确定' }))

  expect(await screen.findByText('已撤销的批次不能更改状态')).toBeTruthy()
  await u.click(screen.getByRole('button', { name: '关闭提示' }))
  expect(screen.queryByText('已撤销的批次不能更改状态')).toBeNull()
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
