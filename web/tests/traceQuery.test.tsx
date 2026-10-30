// 双向追溯查询：四种模式各有输入口径，结果按生产批次分组。
// 匹配统一走部分匹配：输片段即可，输整串时自然只命中那一条。
// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TraceQuery } from '../src/screens/traceQuery'
import type { Batch, BatchTrace } from '../src/lib/batch'
import type { Part } from '../src/lib/catalog'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const batches: Batch[] = [
  { id: 1, batch_no: '成品批次', product_id: 10, product_name: '电水壶', product_code: 'DSH', status: 1 } as Batch,
  { id: 2, batch_no: '第二批次', product_id: 10, product_name: '电水壶', product_code: 'DSH', status: 1 } as Batch,
]
const parts: Part[] = [
  { id: 2, code: '零件A', name: '加热管' } as Part,
  { id: 3, code: '零件B', name: '温控器' } as Part,
]

const row = (over: Partial<BatchTrace>): BatchTrace =>
  ({
    id: 3,
    batch_id: 1,
    part_id: 2,
    used_qty: 1.25,
    supplier: '供应商',
    ...over,
  }) as BatchTrace

function jsonRows(rows: BatchTrace[]) {
  return new Response(JSON.stringify(rows))
}

async function pickMode(label: string) {
  const u = userEvent.setup()
  await u.selectOptions(screen.getByRole('combobox', { name: '查询方式' }), label)
  return u
}

it('产品批次号：部分匹配，输片段即带 match=partial 查询', async () => {
  const fetch = vi.fn().mockResolvedValue(jsonRows([row({})]))
  vi.stubGlobal('fetch', fetch)
  render(<TraceQuery batches={batches} parts={parts} onClose={() => {}} />)
  const u = await pickMode('产品批次号')
  // 只有一个下拉：不再提供匹配方式选择。
  expect(screen.getAllByRole('combobox')).toHaveLength(1)
  await u.type(screen.getByPlaceholderText(/可只输片段/), '成品')
  await u.click(screen.getByRole('button', { name: '查询' }))

  // 查询值「成品」被 <mark> 拆开，按 textContent 匹配组头。
  expect(await screen.findByText((_, el) => el?.textContent === '成品批次')).toBeTruthy()
  expect(screen.getByText(/电水壶/)).toBeTruthy()
  expect(screen.getByText('零件A')).toBeTruthy()
  expect(screen.getByText('1.25')).toBeTruthy()
  expect(fetch).toHaveBeenCalledWith(
    '/api/traces/by-product?batch_no=%E6%88%90%E5%93%81&match=partial',
    expect.anything(),
  )
})

it('产品名称：部分匹配命中同产品的多个批次，合并成两组', async () => {
  const fetch = vi.fn(async (url: string) => {
    if (url.startsWith('/api/traces/by-batch?batch_id=1')) return jsonRows([row({ id: 3 })])
    if (url.startsWith('/api/traces/by-batch?batch_id=2')) return jsonRows([row({ id: 4, batch_id: 2, part_id: 3 })])
    return new Response('[]')
  })
  vi.stubGlobal('fetch', fetch)
  render(<TraceQuery batches={batches} parts={parts} onClose={() => {}} />)
  const u = await pickMode('产品名称/编码')
  await u.type(screen.getByPlaceholderText(/可只输片段/), '水壶')
  await u.click(screen.getByRole('button', { name: '查询' }))

  expect(await screen.findByText('成品批次')).toBeTruthy()
  expect(screen.getByText('第二批次')).toBeTruthy()
  expect(screen.getByText('温控器')).toBeTruthy()
})

it('零件编码/名称：编码与名称都由服务端按部分匹配查', async () => {
  const fetch = vi.fn(async (url: string) => {
    if (url.startsWith('/api/traces/by-part?')) return jsonRows([row({ id: 5, part_id: 3 })])
    return new Response('[]')
  })
  vi.stubGlobal('fetch', fetch)
  render(<TraceQuery batches={batches} parts={parts} onClose={() => {}} />)
  const u = await pickMode('零件编码/名称')
  await u.type(screen.getByPlaceholderText(/可只输片段/), '温控')
  await u.click(screen.getByRole('button', { name: '查询' }))

  const cell = await screen.findByText((_, el) => el?.textContent === '零件B温控器')
  expect(cell).toBeTruthy()
  // 名称不再由前端逐批次轮询：一条 by-part 就够。
  expect(fetch).toHaveBeenCalledWith(
    '/api/traces/by-part?part_code=%E6%B8%A9%E6%8E%A7&match=partial',
    expect.anything(),
  )
  expect(fetch.mock.calls.some(([url]) => String(url).includes('by-batch'))).toBe(false)
})

it('零件批次号：按入库批次反查，结果列出批次分配', async () => {
  const fetch = vi.fn().mockResolvedValue(
    jsonRows([
      row({
        allocations: [
          { inbound_id: 11, batch_no: 'RK-20260630-0064', qty: 4, position: 0 },
          { inbound_id: 12, batch_no: 'RK-20260829-0065', qty: 8, position: 1 },
        ],
      }),
    ]),
  )
  vi.stubGlobal('fetch', fetch)
  render(<TraceQuery batches={batches} parts={parts} onClose={() => {}} />)
  const u = await pickMode('零件批次号')
  await u.type(screen.getByPlaceholderText(/可只输片段/), '20260630')
  await u.click(screen.getByRole('button', { name: '查询' }))

  expect(await screen.findByText('成品批次')).toBeTruthy()
  const alloc = await screen.findByText((_, el) => el?.textContent === 'RK-20260630-0064 × 4')
  expect(alloc).toBeTruthy()
  expect(screen.getByText((_, el) => el?.textContent === 'RK-20260829-0065 × 8')).toBeTruthy()
  expect(fetch).toHaveBeenCalledWith(
    '/api/traces/by-part-lot?batch_no=20260630&match=partial',
    expect.anything(),
  )
})

it('片段也没有命中时给出"未找到"并提示核对查询值', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response('[]'))
  vi.stubGlobal('fetch', fetch)
  render(<TraceQuery batches={batches} parts={parts} onClose={() => {}} />)
  const u = await pickMode('产品批次号')
  await u.type(screen.getByPlaceholderText(/可只输片段/), '不存在')
  await u.click(screen.getByRole('button', { name: '查询' }))

  expect(await screen.findByRole('status')).toHaveProperty(
    'textContent',
    '未找到追溯记录：请核对查询值与查询方式是否匹配',
  )
})

it('命中批次但没有投料记录时，提示去记录投料而不是"未找到"', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('[]')))
  render(<TraceQuery batches={batches} parts={parts} onClose={() => {}} />)
  const u = await pickMode('产品名称/编码')
  await u.type(screen.getByPlaceholderText(/可只输片段/), '水壶') // 命中 2 个批次
  await u.click(screen.getByRole('button', { name: '查询' }))
  expect(await screen.findByRole('status')).toHaveProperty(
    'textContent',
    '命中的批次还没有投料记录（在「记录投料」保存后即可查到）',
  )
})

it('查询值在结果里高亮（批次号、产品名、零件编码与名称）', async () => {
  // Response 的 body 只能读一次，两个批次并发查询必须各自拿到新的 Response；
  // 记录 id 也要按批次区分，否则同一 id 会落在同一组里触发重复 key。
  const fetch = vi.fn(async (url: string) => {
    const id = url.includes('batch_id=1') ? 3 : 4
    return jsonRows([row({ id, batch_id: id === 3 ? 1 : 2 })])
  })
  vi.stubGlobal('fetch', fetch)
  render(<TraceQuery batches={batches} parts={parts} onClose={() => {}} />)
  const u = await pickMode('产品名称/编码')
  await u.type(screen.getByPlaceholderText(/可只输片段/), '水壶')
  await u.click(screen.getByRole('button', { name: '查询' }))

  // 组头的产品名「电水壶」里的"水壶"应该被 <mark> 包住。
  await waitFor(() => expect(document.querySelector('mark')).toBeTruthy())
  const texts = [...document.querySelectorAll('mark')].map((m) => m.textContent)
  expect(texts).toContain('水壶')
})

it('空输入与查询失败不复用旧结果', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response('{"error":"查询故障"}', { status: 500 }))
  vi.stubGlobal('fetch', fetch)
  render(<TraceQuery batches={batches} parts={parts} onClose={() => {}} />)
  const u = userEvent.setup()
  const button = screen.getByRole('button', { name: '查询' })
  await u.click(button)
  expect(fetch).not.toHaveBeenCalled()
  expect(screen.getByText('请输入查询值')).toBeTruthy()

  await u.type(screen.getByPlaceholderText(/可只输片段/), 'missing')
  await u.click(button)
  await waitFor(() => expect(screen.getByText('查询故障')).toBeTruthy())
  expect(screen.queryByRole('status')).toBeNull()

  // 换模式清掉旧结果与错误。
  await pickMode('零件编码/名称')
  expect(screen.queryByText('查询故障')).toBeNull()
})
