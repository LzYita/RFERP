// 工作台四宫格：左列上"现状预警"、下"未来预警"；右列上"生产计划"、下"操作记录"。
//
// 顺序是用户明确要求的（2026-10-26）：现状在上、未来在下。它容易被后续
// 重构无意改回去，所以除了断言内容，还要断言面板顺序与两个预警的徽章形态
// （实心 vs 虚线）——颜色表达严重程度，线型表达"已发生 / 预计"。
// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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
  // 现在低库存（40 < 100），且在产后也会缺：两栏都会出现，但文案必须不同
  { id: 4, code: 'M-004', name: '装饰圈', stock_qty: 40, warn_qty: 100, unit: '个', status: 1 },
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
    {
      part_id: 2, stock: 100, warn: 20, committed: 120, after: -20,
      holders: [
        { batch_id: 11, batch_no: 'B-1', qty: 60 },
        { batch_id: 12, batch_no: 'B-2', qty: 60 },
      ],
    },
    {
      part_id: 4, stock: 40, warn: 100, committed: 70, after: -30,
      holders: [
        { batch_id: 11, batch_no: 'B-1', qty: 40 },
        { batch_id: 12, batch_no: 'B-2', qty: 30 },
      ],
    },
  ],
  batches: [
    { batch_id: 11, level: 'short', parts: [{ part_id: 2, code: 'M-002', name: '外壳', after: -20, warn: 20 }] },
    { batch_id: 12, level: 'low' },
  ],
  summary: { willWarn: 0, willShort: 2 },
}

function stubFetch(opts: { projectionFails?: boolean } = {}) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url.startsWith('/api/parts')) return new Response(JSON.stringify(parts))
      if (url.startsWith('/api/products')) return new Response(JSON.stringify(products))
      if (url.startsWith('/api/batches')) return new Response(JSON.stringify(batches))
      if (url.startsWith('/api/audit/recent')) return new Response(JSON.stringify(logs))
      if (url.startsWith('/api/stock/projection')) {
        return opts.projectionFails
          ? new Response('{"error":"boom"}', { status: 500 })
          : new Response(JSON.stringify(projectionRes))
      }
      return new Response('{}', { status: 404 })
    }),
  )
}

it('lays the four panels out as requested: current/plan on top, future/logs below', async () => {
  stubFetch()
  render(<DashboardScreen />)
  await screen.findByText('现状预警')

  const panels = Array.from(document.querySelectorAll('[data-panel]')).map((el) =>
    el.getAttribute('data-panel'),
  )
  // DOM 顺序 = 视觉顺序（grid 按行列排布）。左列上、右列上、左列下、右列下。
  expect(panels).toEqual(['current-alerts', 'production-plan', 'future-alerts', 'recent-logs'])
})

it('reports only the present tense in 现状预警', async () => {
  stubFetch()
  render(<DashboardScreen />)

  const current = within(
    await screen.findByText('现状预警').then((el) => el.closest('[data-panel]') as HTMLElement),
  )
  expect(current.getByText('无库存')).toBeTruthy()
  expect(current.getByText('无库存').getAttribute('data-badge-variant')).toBe('solid')
  // M-004 现在 40 < 预警线 100：在"现状"栏里必须是低库存，
  // 不能因为"做完会缺"就显示成未来的"将缺料"
  expect(current.getByText('低库存')).toBeTruthy()
  // 未来时态的词不该出现在现状栏
  expect(current.queryByText('将缺料')).toBeNull()
  expect(current.queryByText('将预警')).toBeNull()
})

it('keeps 未来预警 strictly forward-looking and dashed', async () => {
  stubFetch()
  render(<DashboardScreen />)

  const future = within(
    await screen.findByText('未来预警').then((el) => el.closest('[data-panel]') as HTMLElement),
  )
  // 当前正常的 M-002 只在产后缺料
  expect(future.getByText('外壳')).toBeTruthy()
  for (const badge of future.getAllByText('将缺料')) {
    expect(badge.getAttribute('data-badge-variant')).toBe('dashed')
  }
  // M-004 现在低库存、在产后缺料：未来栏里也只能出现"将缺料"
  expect(future.getByText('装饰圈')).toBeTruthy()
  expect(future.queryByText('低库存')).toBeNull()
})

it('opens the in-progress batches consuming a projected part', async () => {
  stubFetch()
  const u = userEvent.setup()
  render(<DashboardScreen />)

  // 同一零件会同时出现在现状与未来两栏，这里要点的是"未来预警"里那一行
  const future = within(
    await screen.findByText('未来预警').then((el) => el.closest('[data-panel]') as HTMLElement),
  )
  await u.click(future.getByText('装饰圈'))

  const dialog = within(await screen.findByRole('dialog'))
  expect(dialog.getByText(/B-1/)).toBeTruthy()
  expect(dialog.getByText(/B-2/)).toBeTruthy()
  // 每条占用都要能对上批次号、计划数与本零件占用
  expect(dialog.getByText('批次号')).toBeTruthy()
  expect(dialog.getByText('计划数')).toBeTruthy()
  expect(dialog.getByText('本零件占用')).toBeTruthy()
})

it('says the projection failed instead of pretending there are no future alerts', async () => {
  stubFetch({ projectionFails: true })
  render(<DashboardScreen />)
  const future = within(
    await screen.findByText('未来预警').then((el) => el.closest('[data-panel]') as HTMLElement),
  )
  expect(future.getByText(/在产占用数据加载失败/)).toBeTruthy()
  // 不能把"算不出来"画成"没有预警"
  expect(future.queryByText('没有正在生产、完成后会跌破预警线的零件')).toBeNull()
  // 现状预警不依赖投影，仍然正常
  const current = within(screen.getByText('现状预警').closest('[data-panel]') as HTMLElement)
  expect(current.getByText('无库存')).toBeTruthy()
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
