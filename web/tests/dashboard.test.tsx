// 工作台四宫格：左列上"现状预警"、下"使用中预警"；右列上"生产计划"、下"操作记录"。
//
// 顺序是用户明确要求的（2026-10-26）：现状在上、使用中在下。它容易被后续
// 重构无意改回去，所以除了断言内容，还要断言面板顺序与两个预警的徽章形态
// （实心 vs 虚线）——颜色表达严重程度，线型表达"已发生 / 预计"。
// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
import { DashboardScreen } from '../src/screens/DashboardScreen'

vi.mock('../src/lib/api', async (original) => ({
  ...(await original<typeof import('../src/lib/api')>()),
  usePermissions: () => ({ modules: {} }),
}))

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const parts = [
  // 现状告警：现在就没货
  { id: 1, code: 'M-001', name: '加热管', stock_qty: 0, warn_qty: 20, unit: '个', status: 1 },
  // 现状正常、在产后缺料
  { id: 2, code: 'M-002', name: '外壳', stock_qty: 100, warn_qty: 20, unit: '个', status: 1 },
  { id: 3, code: 'M-003', name: '彩盒', stock_qty: 500, warn_qty: 100, unit: '个', status: 1 },
]

const products = [{ id: 1, code: 'P-001', name: '电水壶', status: 1 }]

const batches = [
  { id: 11, batch_no: 'B-1', product_id: 1, product_code: 'P-001', product_name: '电水壶', plan_qty: 60, produced_qty: 0, status: 0, version: 1 },
  { id: 12, batch_no: 'B-2', product_id: 1, product_code: 'P-001', product_name: '电水壶', plan_qty: 40, produced_qty: 0, status: 1, version: 1 },
  { id: 13, batch_no: 'B-DONE', product_id: 1, product_code: 'P-001', product_name: '电水壶', plan_qty: 10, produced_qty: 10, status: 2, version: 1 },
]

const logs = [
  { id: 1, table_name: 'parts', record_id: 1, action: 'INSERT', operator: '管理员', created_at: '2026-10-26T09:00:00+08:00' },
]

const projectionRes = {
  parts: [
    { part_id: 2, stock: 100, warn: 20, committed: 120, after: -20, holders: [{ batch_no: 'B-1', qty: 60 }, { batch_no: 'B-2', qty: 60 }] },
  ],
  batches: [
    { batch_id: 11, level: 'short', parts: [{ part_id: 2, code: 'M-002', name: '外壳', after: -20, warn: 20 }] },
    { batch_id: 12, level: 'low' },
  ],
  summary: { willWarn: 0, willShort: 1 },
}

function stubFetch() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url.startsWith('/api/parts')) return new Response(JSON.stringify(parts))
      if (url.startsWith('/api/products')) return new Response(JSON.stringify(products))
      if (url.startsWith('/api/batches')) return new Response(JSON.stringify(batches))
      if (url.startsWith('/api/audit/recent')) return new Response(JSON.stringify(logs))
      if (url.startsWith('/api/stock/projection')) return new Response(JSON.stringify(projectionRes))
      return new Response('{}', { status: 404 })
    }),
  )
}

it('lays the four panels out as requested: current/plan on top, projected/logs below', async () => {
  stubFetch()
  render(<DashboardScreen />)
  await screen.findByText('现状预警')

  const panels = Array.from(document.querySelectorAll('[data-panel]')).map((el) =>
    el.getAttribute('data-panel'),
  )
  // DOM 顺序 = 视觉顺序（grid 按行列排布）。左列上、右列上、左列下、右列下。
  expect(panels).toEqual(['current-alerts', 'production-plan', 'projected-alerts', 'recent-logs'])
})

it('keeps current and projected alerts visually distinct', async () => {
  stubFetch()
  render(<DashboardScreen />)

  const current = within(await screen.findByText('现状预警').then((el) => el.closest('[data-panel]') as HTMLElement))
  expect(current.getByText('无库存')).toBeTruthy()
  expect(current.getByText('无库存').getAttribute('data-badge-variant')).toBe('solid')

  const projected = within(await screen.findByText('使用中预警').then((el) => el.closest('[data-panel]') as HTMLElement))
  // 该零件当前库存 100（正常），只有在产后才缺 —— 必须是虚线
  expect(projected.getByText('将缺料').getAttribute('data-badge-variant')).toBe('dashed')
  expect(projected.getByText('外壳')).toBeTruthy()
  // 现状面板里不应出现它：当前库存 100 不告警
  expect(current.queryByText('外壳')).toBeNull()
})

it('lists only open batches in the production plan, with their reserve level', async () => {
  stubFetch()
  render(<DashboardScreen />)
  const plan = within(await screen.findByText('生产计划').then((el) => el.closest('[data-panel]') as HTMLElement))

  expect(plan.getByText('B-1')).toBeTruthy()
  expect(plan.getByText('B-2')).toBeTruthy()
  // 已完成批次不进"生产计划"
  expect(plan.queryByText('B-DONE')).toBeNull()
  // 备料角标取自投影的批次结论
  expect(plan.getByText('不足')).toBeTruthy()
  expect(plan.getByText('将预警')).toBeTruthy()
})

it('shows recent activity and stat cards', async () => {
  stubFetch()
  render(<DashboardScreen />)
  expect(await screen.findByText('最近操作记录')).toBeTruthy()
  expect(screen.getByText('管理员')).toBeTruthy()
  expect(screen.getByText('在产批次')).toBeTruthy()
})
