// 零件页：库存状态与「在产占用」合并到一列，靠实心/虚线区分"现在"与"之后"。
//
// 这一屏是用户查库存的地方，所以三件事必须同时成立：
//   1) 状态列能解释"为什么库存 100 却说不够"（title 里有占用与完成后数字）；
//   2) 新增的「在产占用」列只在有占用时显示数字，否则是占位符；
//   3) 「在产后将预警」筛选与既有的「库存预警」互不篡改口径。
// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PartsScreen } from '../src/screens/PartsScreen'

vi.mock('../src/lib/api', async (original) => ({
  ...(await original<typeof import('../src/lib/api')>()),
  usePermissions: () => ({ modules: { parts: 'write' } }),
}))

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const parts = [
  // 当前正常，但在产后缺料
  { id: 2, code: 'M-002', name: '外壳', unit: '个', part_type: '塑料', stock_qty: 100, warn_qty: 20, status: 1, version: 1 },
  // 当前就没货
  { id: 1, code: 'M-001', name: '加热管', unit: '个', part_type: '电子', stock_qty: 0, warn_qty: 20, status: 1, version: 1 },
  // 一切正常
  { id: 3, code: 'M-003', name: '彩盒', unit: '个', part_type: '包装辅料', stock_qty: 500, warn_qty: 100, status: 1, version: 1 },
]

const projectionRes = {
  parts: [
    { part_id: 2, stock: 100, warn: 20, committed: 120, after: -20 },
    { part_id: 3, stock: 500, warn: 100, committed: 30, after: 470 },
  ],
  batches: [],
  summary: { willWarn: 0, willShort: 1 },
}

function stubFetch() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url.startsWith('/api/parts')) return new Response(JSON.stringify(parts))
      if (url.startsWith('/api/stock/projection')) return new Response(JSON.stringify(projectionRes))
      return new Response('{}', { status: 404 })
    }),
  )
}

it('merges current and projected stock into one column with solid vs dashed badges', async () => {
  stubFetch()
  render(<PartsScreen />)

  const row = (await screen.findByText('M-002')).closest('tr')!
  const cells = within(row)
  // 当前库存正常、但在产后缺料 → 虚线「将缺料」，底部 title 给出数字
  const badge = cells.getByText('将缺料')
  expect(badge.getAttribute('data-badge-variant')).toBe('dashed')
  expect(badge.getAttribute('title')).toContain('在产占用 120')

  // 现在就没货 → 实心「无库存」，即使投影没有它
  const outRow = within(screen.getByText('M-001').closest('tr') as HTMLElement)
  expect(outRow.getByText('无库存').getAttribute('data-badge-variant')).toBe('solid')

  // 正常件：实心中性
  const okRow = within(screen.getByText('M-003').closest('tr') as HTMLElement)
  expect(okRow.getByText('正常').getAttribute('data-badge-variant')).toBe('solid')
})

it('shows the committed quantity only where there is one', async () => {
  stubFetch()
  render(<PartsScreen />)

  const withHolding = within((await screen.findByText('M-002')).closest('tr') as HTMLElement)
  expect(withHolding.getByText('120 个')).toBeTruthy()
  // 没有占用的零件显示占位符，而不是一整列 0。
  // 注意该行「库存」本身就是 0 个，所以断言"0 个只出现一次"。
  const noHolding = within(screen.getByText('M-001').closest('tr') as HTMLElement)
  expect(noHolding.getAllByText('0 个')).toHaveLength(1)
})

it('filters by projected alerts without changing the current-alert filter', async () => {
  stubFetch()
  const u = userEvent.setup()
  render(<PartsScreen />)
  await screen.findByText('M-002')

  // 「在产后将预警」只选出投影两档：M-002（将缺料）
  await u.click(screen.getByRole('checkbox', { name: '在产后将预警' }))
  expect(screen.getByText('M-002')).toBeTruthy()
  expect(screen.queryByText('M-001')).toBeNull()
  expect(screen.queryByText('M-003')).toBeNull()

  // 再勾上「库存预警」→ 并集：当前无货的 M-001 也回来
  await u.click(screen.getByRole('checkbox', { name: '库存预警' }))
  expect(screen.getByText('M-001')).toBeTruthy()
  expect(screen.getByText('M-002')).toBeTruthy()
  expect(screen.queryByText('M-003')).toBeNull()

  // 重置后回到全部
  await u.click(screen.getByRole('button', { name: '重置' }))
  expect(screen.getByText('M-003')).toBeTruthy()
})

it('still renders the table when the projection request fails', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url.startsWith('/api/parts')) return new Response(JSON.stringify(parts))
      return new Response('{"error":"boom"}', { status: 500 })
    }),
  )
  render(<PartsScreen />)
  // 投影失败退化为当前三档，列表本身不受影响
  expect(await screen.findByText('M-002')).toBeTruthy()
  expect(within(screen.getByText('M-001').closest('tr') as HTMLElement).getByText('无库存')).toBeTruthy()
  // 但要明说"在产占用算不出来"，不能让整列占位符看起来像真的没有占用
  expect((await screen.findByRole('status')).textContent).toContain('在产占用加载失败')
})
