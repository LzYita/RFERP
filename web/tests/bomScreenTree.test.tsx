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

vi.mock('../src/lib/api', async (original) => ({
  ...(await original<typeof import('../src/lib/api')>()),
  usePermissions: () => ({ modules: { bom: 'write', parts: 'write' } }),
}))

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
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

/** 产品与零件列表按 URL 区分；BOM 明细只在 product_id=3 时返回。 */
function stubFetch(opts: { parts?: unknown[]; bom?: unknown[]; method?: string; body?: unknown } = {}) {
  const fetch = vi.fn(async (url: string, init?: RequestInit) => {
    if (init?.method === 'PUT' || init?.method === 'POST') {
      return new Response(JSON.stringify(opts.body ?? {}), { status: 200 })
    }
    if (url.startsWith('/api/products')) return new Response(JSON.stringify([product]))
    if (url.startsWith('/api/parts')) return new Response(JSON.stringify(opts.parts ?? [electronic, plastic, untyped]))
    if (url.startsWith('/api/bom?')) return new Response(JSON.stringify(opts.bom ?? bomItems))
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

it('edits only quantity, use mode and loss rate on a BOM line', async () => {
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

  // 改数量与损耗率 → 提交
  const qty = dialog.getByLabelText(/每台用量/) as HTMLInputElement
  await u.clear(qty)
  await u.type(qty, '8')
  const loss = dialog.getByLabelText('损耗率%') as HTMLInputElement
  await u.clear(loss)
  await u.type(loss, '2.5')
  await u.click(dialog.getByRole('button', { name: '保存' }))

  const put = fetch.mock.calls.find(([, init]) => (init as RequestInit)?.method === 'PUT')
  expect(put).toBeTruthy()
  expect(String(put?.[0])).toBe('/api/bom')
  const body = JSON.parse(String((put![1] as RequestInit).body))
  // 请求体只有 id/version + 三个业务字段：服务端 DisallowUnknownFields，
  // 多带 part_id 之类会被 400，所以这里逐键核对，不是只看前两个值。
  expect(Object.keys(body).sort()).toEqual(['id', 'loss_rate', 'quantity', 'use_mode', 'version'])
  expect(body.id).toBe(11)
  expect(body.version).toBe(1)
  expect(body.quantity).toBe(8)
  expect(body.loss_rate).toBe(2.5)
  expect(body.use_mode).toBe(0)

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

it('creates a new part from the BOM screen', async () => {
  const fetch = stubFetch()
  const u = userEvent.setup()
  render(<BOMScreen />)
  await pickProduct(u)

  await u.click(await screen.findByRole('button', { name: '+ 新增零件' }))
  const dialog = within(await screen.findByRole('dialog', { name: '新增零件' }))
  await u.type(dialog.getByPlaceholderText('零件编码'), 'M-100')
  await u.type(dialog.getByPlaceholderText('零件名称'), '新零件')
  await u.click(dialog.getByRole('button', { name: '保存' }))

  const post = fetch.mock.calls.find(([, init]) => (init as RequestInit)?.method === 'POST')
  expect(post).toBeTruthy()
  expect(String(post?.[0])).toBe('/api/parts')
  const init = post![1] as RequestInit
  const body = JSON.parse(String(init.body))
  expect(body.code).toBe('M-100')
  expect(body.name).toBe('新零件')
})
