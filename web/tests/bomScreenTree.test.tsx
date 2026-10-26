// BOM 屏：树状明细表 + 零件主数据的新增/编辑入口。
//
// 树的验收点不是「渲染了行」，而是层级正确：根 = 产品，子层 = 分类
// （取自零件主数据，不是 BOMItem），叶子 = 明细行；分类为空的零件
// 必须落在「未分类」里而不是消失。零件编辑走与零件管理共用的
// PartFormModal，提交后两份冗余（allParts 分组、明细行的编码/名称）都要刷新。
// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { BOMScreen } from '../src/screens/BOMScreen'
import { clearBomSelection, recallBomSelection } from '../src/lib/bomSelection'

vi.mock('../src/lib/api', async (original) => ({
  ...(await original<typeof import('../src/lib/api')>()),
  usePermissions: () => ({ modules: { bom: 'write', parts: 'write' } }),
}))

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  // 选择记忆是模块级的，用例之间必须清掉，否则后一个用例会"自动选中"前一个的产品
  clearBomSelection()
})

const product = { id: 3, code: 'P-001', name: '智能电水壶', unit: '个', status: 1, version: 5 }

const electronic = {
  id: 7, code: 'M-001', name: '加热管', spec: '220V', unit: '个', part_type: '电子',
  stock_qty: 10, warn_qty: 1, status: 1, version: 2,
}
const plastic = {
  id: 8, code: 'M-002', name: '外壳', unit: '个', part_type: '塑料',
  stock_qty: 5, warn_qty: 0, status: 1, version: 1,
}
const untyped = {
  id: 9, code: 'M-003', name: '旧数据零件', unit: '个', part_type: undefined,
  stock_qty: 0, warn_qty: 0, status: 1, version: 1,
}

const bomItems = [
  { id: 11, product_id: 3, part_id: 7, quantity: 1, loss_rate: 0, replaceable: 0, use_mode: 0, version: 1, part_code: 'M-001', part_name: '加热管' },
  { id: 12, product_id: 3, part_id: 9, quantity: 2, loss_rate: 0, replaceable: 0, use_mode: 0, version: 1, part_code: 'M-003', part_name: '旧数据零件' },
]

const product2 = { id: 4, code: 'P-002', name: '空气炸锅', unit: '台', status: 1, version: 1 }
const bomItems2 = [
  { id: 21, product_id: 4, part_id: 8, quantity: 3, loss_rate: 0, replaceable: 0, use_mode: 0, version: 1, part_code: 'M-002', part_name: '外壳' },
]

/** 产品与零件列表按 URL 区分；BOM 明细按 product_id 分流，默认回退到第一个产品的数据。 */
function stubFetch(opts: { parts?: unknown[]; bomByProduct?: Record<string, unknown[]> } = {}) {
  const fetch = vi.fn(async (url: string, init?: RequestInit) => {
    if (init?.method === 'PUT' || init?.method === 'POST') {
      return new Response(JSON.stringify({}), { status: 200 })
    }
    if (url.startsWith('/api/products')) return new Response(JSON.stringify([product, product2]))
    if (url.startsWith('/api/parts')) return new Response(JSON.stringify(opts.parts ?? [electronic, plastic, untyped]))
    if (url.startsWith('/api/bom?')) {
      const id = new URL(url, 'http://x').searchParams.get('product_id') ?? ''
      const body = opts.bomByProduct?.[id] ?? (id === '4' ? bomItems2 : bomItems)
      return new Response(JSON.stringify(body))
    }
    return new Response(JSON.stringify({ error: `unexpected GET ${url}` }), { status: 404 })
  })
  vi.stubGlobal('fetch', fetch)
  return fetch
}

/** 走级联选中产品（名称 → 编码），触发 BOM 明细加载。 */
async function pickProduct(u: ReturnType<typeof userEvent.setup>) {
  await u.selectOptions(await screen.findByLabelText('名称'), '智能电水壶')
  await u.selectOptions(screen.getByLabelText('编码'), 'P-001')
}

it('renders the tree: product root, category groups from part master data, part leaves', async () => {
  stubFetch()
  const u = userEvent.setup()
  render(<BOMScreen />)
  await pickProduct(u)

  // 根 = 产品，并给出汇总
  const rootRow = (await screen.findByText('P-001 智能电水壶')).closest('tr')!
  expect(rootRow.getAttribute('data-tree-root')).toBe('true')
  expect(rootRow.textContent).toContain('共 2 个零件')

  // 分类子层：电子（加热管）与 未分类（旧数据零件）；没有 BOM 引用的「塑料」不出现
  const groupRow = screen.getByText('电子').closest('tr')!
  expect(groupRow.getAttribute('data-tree-group')).toBe('true')
  expect(groupRow.textContent).toContain('1 项')
  expect(screen.getByText('未分类').closest('tr')!.textContent).toContain('1 项')
  expect(screen.queryByText('塑料')).toBeNull()

  // 叶子 = 明细行：编码在对应分组下可见
  expect(screen.getByText('M-001')).toBeTruthy()
  expect(screen.getByText('M-003')).toBeTruthy()
})

it('collapses and re-expands a category group', async () => {
  stubFetch()
  const u = userEvent.setup()
  render(<BOMScreen />)
  await pickProduct(u)

  await screen.findByText('M-001')
  await u.click(screen.getByText('电子'))
  expect(screen.queryByText('M-001')).toBeNull()
  // 未分类不受影响
  expect(screen.getByText('M-003')).toBeTruthy()
  await u.click(screen.getByText('电子'))
  expect(screen.getByText('M-001')).toBeTruthy()
})

it('edits quantity, use mode, loss rate and replaceable on a BOM line', async () => {
  const fetch = stubFetch()
  const u = userEvent.setup()
  render(<BOMScreen />)
  await pickProduct(u)

  // 两行明细各有「编辑」，点 M-001 那行的
  const row = (await screen.findByText('M-001')).closest('tr')!
  await u.click(within(row).getByRole('button', { name: '编辑' }))
  const dialog = within(await screen.findByRole('dialog', { name: '编辑 BOM 明细' }))

  // 零件归属只读展示：编辑范围里没有换零件这回事
  expect(dialog.getByText('M-001')).toBeTruthy()
  expect(dialog.queryByPlaceholderText('零件编码')).toBeNull()

  // 改数量、损耗率，并勾上「可替换」→ 提交
  const qty = dialog.getByLabelText(/每台用量/) as HTMLInputElement
  await u.clear(qty)
  await u.type(qty, '8')
  const loss = dialog.getByLabelText('损耗率%') as HTMLInputElement
  await u.clear(loss)
  await u.type(loss, '2.5')
  const replaceable = dialog.getByRole('checkbox', { name: /该零件可被替换/ }) as HTMLInputElement
  expect(replaceable.checked).toBe(false)
  await u.click(replaceable)
  await u.click(dialog.getByRole('button', { name: '保存' }))

  const put = fetch.mock.calls.find(([, init]) => (init as RequestInit)?.method === 'PUT')
  expect(put).toBeTruthy()
  expect(String(put?.[0])).toBe('/api/bom')
  const body = JSON.parse(String((put![1] as RequestInit).body))
  // 请求体只有 id/version + 四个业务字段：服务端 DisallowUnknownFields，
  // 多带 part_id 之类会被 400，所以这里逐键核对，不是只看前两个值。
  expect(Object.keys(body).sort()).toEqual([
    'id',
    'loss_rate',
    'quantity',
    'replaceable',
    'use_mode',
    'version',
  ])
  expect(body.id).toBe(11)
  expect(body.version).toBe(1)
  expect(body.quantity).toBe(8)
  expect(body.loss_rate).toBe(2.5)
  expect(body.use_mode).toBe(0)
  expect(body.replaceable).toBe(1)

  // 保存后重拉明细，树随之更新
  const bomGets = fetch.mock.calls.filter(([url]) => String(url).startsWith('/api/bom?'))
  expect(bomGets.length).toBeGreaterThanOrEqual(2)
})

it('refuses a non-positive quantity before sending', async () => {
  const fetch = stubFetch()
  const u = userEvent.setup()
  render(<BOMScreen />)
  await pickProduct(u)

  const row = (await screen.findByText('M-001')).closest('tr')!
  await u.click(within(row).getByRole('button', { name: '编辑' }))
  const dialog = within(await screen.findByRole('dialog', { name: '编辑 BOM 明细' }))
  const qty = dialog.getByLabelText(/每台用量/) as HTMLInputElement
  await u.clear(qty)
  await u.type(qty, '0')
  await u.click(dialog.getByRole('button', { name: '保存' }))

  expect(await dialog.findByText('BOM用量必须大于0')).toBeTruthy()
  expect(fetch.mock.calls.some(([, init]) => (init as RequestInit)?.method === 'PUT')).toBe(false)
})

it('restores the last selected product after leaving and re-entering the screen', async () => {
  stubFetch()
  const u = userEvent.setup()
  const first = render(<BOMScreen />)
  await pickProduct(u)
  await screen.findByText('M-001')

  // 导航到别的界面（卸载）再回来（重新挂载）：不需要再点名称→编码→规格
  first.unmount()
  render(<BOMScreen />)

  expect(await screen.findByText('M-001')).toBeTruthy()
  expect((screen.getByLabelText('名称') as HTMLSelectElement).value).toBe('智能电水壶')
  expect((screen.getByLabelText('编码') as HTMLSelectElement).value).toBe('P-001')
  // 级联与搜索框都要恢复，否则界面显示已选、active 却是空的
  const search = screen.getByPlaceholderText('输入编码/名称查询') as HTMLInputElement
  expect(search.value).toContain('P-001')
  expect(search.value).toContain('智能电水壶')
})

it('forgets a remembered product that no longer exists', async () => {
  stubFetch()
  const u = userEvent.setup()
  const first = render(<BOMScreen />)
  await pickProduct(u)
  await screen.findByText('M-001')
  first.unmount()

  // 该产品在产品列表里已不存在（被删 / 被停用后不可见）
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url.startsWith('/api/products')) return new Response(JSON.stringify([]))
      if (url.startsWith('/api/parts')) return new Response(JSON.stringify([electronic]))
      if (url.startsWith('/api/bom?')) return new Response(JSON.stringify(bomItems))
      return new Response('{}', { status: 404 })
    }),
  )
  render(<BOMScreen />)

  await screen.findByText('请先选择产品')
  expect((screen.getByLabelText('名称') as HTMLSelectElement).value).toBe('')
  // 记忆必须被清掉：否则每次进来都会恢复到一个不存在的产品上
  expect(recallBomSelection()).toBeNull()
})

it('blanks the table the moment the cascade changes, not when the next load lands', async () => {
  stubFetch()
  const u = userEvent.setup()
  render(<BOMScreen />)
  await pickProduct(u)
  await screen.findByText('M-001')

  // 改名称：级联立即不完整，旧明细必须当场消失。
  // 慢一拍（等新明细回来才清）的话，中间那几秒查询栏与表格内容对不上。
  await u.selectOptions(screen.getByLabelText('名称'), '空气炸锅')
  expect(screen.queryByText('M-001')).toBeNull()
  expect(screen.queryByText('P-001 智能电水壶')).toBeNull()
  expect(await screen.findByText('请先选择产品')).toBeTruthy()
})

it('after switching, loads only the new product\'s lines', async () => {
  stubFetch()
  const u = userEvent.setup()
  render(<BOMScreen />)
  await pickProduct(u)
  await screen.findByText('M-001')

  await u.selectOptions(screen.getByLabelText('名称'), '空气炸锅')
  await screen.findByText('请先选择产品')
  await u.selectOptions(screen.getByLabelText('编码'), 'P-002')

  // 新产品的明细出现，旧产品的零件一行都不残留，根行也换了产品
  expect(await screen.findByText('M-002')).toBeTruthy()
  expect(screen.queryByText('M-001')).toBeNull()
  expect(screen.getByText('P-002 空气炸锅')).toBeTruthy()
})

it('reloads details when the same product is picked again', async () => {
  const fetch = stubFetch()
  const u = userEvent.setup()
  render(<BOMScreen />)
  await pickProduct(u)
  await screen.findByText('M-001')
  const firstGets = fetch.mock.calls.filter(([url]) => String(url).startsWith('/api/bom?')).length

  // 清空编码再选回同一个产品：id 没变，但明细必须重新拉
  // （这条路径靠 pickerEpoch 驱动，id 相同不会自然触发 effect）
  await u.selectOptions(screen.getByLabelText('编码'), '')
  await screen.findByText('请先选择产品')
  await u.selectOptions(screen.getByLabelText('编码'), 'P-001')
  expect(await screen.findByText('M-001')).toBeTruthy()
  const secondGets = fetch.mock.calls.filter(([url]) => String(url).startsWith('/api/bom?')).length
  expect(secondGets).toBeGreaterThan(firstGets)
})

it('offers no way to create part master data from this screen', async () => {
  stubFetch()
  const u = userEvent.setup()
  render(<BOMScreen />)
  await pickProduct(u)

  await screen.findByText('M-001')
  // 建零件属于「零件管理」屏；这里出现入口会让人以为两个动作是一回事
  expect(screen.queryByRole('button', { name: '+ 新增零件' })).toBeNull()
  expect(screen.getByRole('button', { name: '+ 添加 BOM 零件' })).toBeTruthy()
})
