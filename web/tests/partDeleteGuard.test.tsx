// 删除零件被 BOM 引用时：直接列出「被哪些产品用了」，并拒绝删除。
//
// 为什么不是只断言「弹了错误」：这条需求的重点就是**说清是谁在用**。
// 后端回 409 + products，界面必须把这个数组渲染出来；只显示一句失败文案
// 等于让用户自己去 BOM 表里一个个翻。
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

const part = {
  id: 7,
  code: 'M-001',
  name: '共用零件',
  spec: '10x10',
  unit: '个',
  part_type: '电子',
  stock_qty: 10,
  warn_qty: 1,
  status: 1,
  version: 1,
}

function stubFetch(deleteResponse: () => Response) {
  const fetch = vi.fn(async (_url: string, init?: RequestInit) => {
    if (init?.method === 'DELETE') return deleteResponse()
    return new Response(JSON.stringify([part]))
  })
  vi.stubGlobal('fetch', fetch)
  return fetch
}

it('lists the products that reference the part and keeps the part', async () => {
  stubFetch(
    () =>
      new Response(
        JSON.stringify({
          error: '零件 M-001 已被 2 个产品的 BOM 引用，无法删除',
          products: [
            { id: 3, code: 'P-001', name: '智能电水壶' },
            { id: 4, code: 'P-003', name: '空气炸锅' },
          ],
        }),
        { status: 409 },
      ),
  )
  const u = userEvent.setup()
  render(<PartsScreen />)

  await u.click(await screen.findByRole('button', { name: '删除' }))
  const confirm = within(await screen.findByRole('dialog', { name: '确认删除' }))
  await u.click(confirm.getByRole('button', { name: '确定' }))

  const dialog = within(await screen.findByRole('dialog', { name: '无法删除零件' }))
  const list = dialog.getByRole('list', { name: '引用该零件的产品' })
  expect(list.textContent).toContain('P-001')
  expect(list.textContent).toContain('智能电水壶')
  expect(list.textContent).toContain('P-003')
  expect(list.textContent).toContain('空气炸锅')
  expect(dialog.getByText(/M-001/)).toBeTruthy()
  // 拒绝删除：零件还在列表里（没有乐观移除）
  expect(screen.getByText('共用零件')).toBeTruthy()
})

it('does not treat an ordinary failure as a BOM reference', async () => {
  stubFetch(() => new Response(JSON.stringify({ error: 'part not found' }), { status: 400 }))
  const u = userEvent.setup()
  render(<PartsScreen />)

  await u.click(await screen.findByRole('button', { name: '删除' }))
  const confirm = within(await screen.findByRole('dialog', { name: '确认删除' }))
  await u.click(confirm.getByRole('button', { name: '确定' }))

  expect(await screen.findByText('part not found')).toBeTruthy()
  expect(screen.queryByRole('dialog', { name: '无法删除零件' })).toBeNull()
})

it('stops a batch delete at the first BOM-referenced part', async () => {
  const other = { ...part, id: 8, code: 'M-002', name: '另一个零件' }
  const fetch = vi.fn(async (_url: string, init?: RequestInit) => {
    if (init?.method === 'DELETE') {
      return new Response(
        JSON.stringify({
          error: '零件 M-001 已被产品 P-001（智能电水壶）的 BOM 引用，无法删除',
          products: [{ id: 3, code: 'P-001', name: '智能电水壶' }],
        }),
        { status: 409 },
      )
    }
    return new Response(JSON.stringify([part, other]))
  })
  vi.stubGlobal('fetch', fetch)
  const u = userEvent.setup()
  render(<PartsScreen />)

  await u.click(await screen.findByLabelText('选择 M-001'))
  await u.click(screen.getByLabelText('选择 M-002'))
  await u.click(screen.getByRole('button', { name: '批量删除' }))
  const confirm = within(await screen.findByRole('dialog', { name: '确认批量删除' }))
  await u.click(confirm.getByRole('button', { name: '确定' }))

  const dialog = within(await screen.findByRole('dialog', { name: '无法删除零件' }))
  expect(dialog.getByRole('list', { name: '引用该零件的产品' }).textContent).toContain('P-001')
  // 第一个被拒后不再继续删第二个：批量删除没有事务，继续删只会让「已删的不会回滚」更难收拾
  const deletes = fetch.mock.calls.filter(([, init]) => (init as RequestInit | undefined)?.method === 'DELETE')
  expect(deletes).toHaveLength(1)
  expect(String(deletes[0][0])).toContain('id=7')
})
