// BOM 屏按钮的权限分档：动 BOM（添加/删除明细）看 bom 档位，
// 动零件主数据（新增/编辑零件）看 parts 档位。两档可以只占其一，
// 所以按钮要分别藏，不能一把梭。vi.mock 按文件提升，每个权限组合一个文件
// 里的一个用例（另一组合见 bomScreenTree.test.tsx 的 write/write）。
// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.resetModules()
})

const product = { id: 3, code: 'P-001', name: '智能电水壶', unit: '个', status: 1, version: 5 }
const part = {
  id: 7, code: 'M-001', name: '加热管', unit: '个', part_type: '电子',
  stock_qty: 10, warn_qty: 1, status: 1, version: 2,
}
const bomItems = [
  { id: 11, product_id: 3, part_id: 7, quantity: 1, loss_rate: 0, replaceable: 0, use_mode: 0, version: 1, part_code: 'M-001', part_name: '加热管' },
]

function stubFetch() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url.startsWith('/api/products')) return new Response(JSON.stringify([product]))
      if (url.startsWith('/api/parts')) return new Response(JSON.stringify([part]))
      if (url.startsWith('/api/bom?')) return new Response(JSON.stringify(bomItems))
      return new Response(JSON.stringify({ error: `unexpected ${url}` }), { status: 404 })
    }),
  )
}

async function pickProduct(u: ReturnType<typeof userEvent.setup>) {
  await u.selectOptions(await screen.findByLabelText('名称'), '智能电水壶')
  await u.selectOptions(screen.getByLabelText('编码'), 'P-001')
}

it('bom-only write: BOM buttons stay, part-master buttons and the action column hide', async () => {
  vi.doMock('../src/lib/api', async (original) => ({
    ...(await original<typeof import('../src/lib/api')>()),
    usePermissions: () => ({ modules: { bom: 'write' } }),
  }))
  const { BOMScreen } = await import('../src/screens/BOMScreen')
  stubFetch()
  const u = userEvent.setup()
  render(<BOMScreen />)
  await pickProduct(u)

  await screen.findByText('M-001')
  expect(screen.getByRole('button', { name: '+ 添加 BOM 零件' })).toBeTruthy()
  expect(screen.getByRole('button', { name: '删除' })).toBeTruthy()
  expect(screen.queryByRole('button', { name: '+ 新增零件' })).toBeNull()
  expect(screen.queryByRole('button', { name: '编辑' })).toBeNull()
  expect(screen.queryByText('操作')).toBeNull()
})

it('parts-only write: part-master buttons stay, BOM buttons hide', async () => {
  vi.doMock('../src/lib/api', async (original) => ({
    ...(await original<typeof import('../src/lib/api')>()),
    usePermissions: () => ({ modules: { parts: 'write' } }),
  }))
  const { BOMScreen } = await import('../src/screens/BOMScreen')
  stubFetch()
  const u = userEvent.setup()
  render(<BOMScreen />)
  await pickProduct(u)

  await screen.findByText('M-001')
  expect(screen.getByRole('button', { name: '+ 新增零件' })).toBeTruthy()
  expect(screen.getByRole('button', { name: '编辑' })).toBeTruthy()
  expect(screen.getByText('操作')).toBeTruthy()
  expect(screen.queryByRole('button', { name: '+ 添加 BOM 零件' })).toBeNull()
  expect(screen.queryByRole('button', { name: '删除' })).toBeNull()
})
