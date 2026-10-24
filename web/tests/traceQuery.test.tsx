// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TraceQuery } from '../src/screens/traceQuery'
import type { Batch } from '../src/lib/batch'
import type { Part } from '../src/lib/catalog'

afterEach(() => { cleanup(); vi.unstubAllGlobals() })
function show() { render(<TraceQuery batches={[{ id: 1, batch_no: '成品批次' } as Batch]} parts={[{ id: 2, code: '零件A' } as Part]} onClose={() => {}} />) }
it.each(['product', 'part'])('queries by %s with encoded input and readable identities', async (kind) => {
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify([{ id: 3, batch_id: 1, part_id: 2, part_batch_no: '供应批次', used_qty: 1.25, supplier: '供应商' }])))
  vi.stubGlobal('fetch', fetch); show(); const u = userEvent.setup()
  if (kind === 'part') await u.selectOptions(screen.getByRole('combobox'), 'part')
  await u.type(screen.getByPlaceholderText('输入完整批次号或零件编码'), ' A /中 ')
  await u.click(screen.getByRole('button', { name: '查询', exact: true }))
  expect(await screen.findByText('供应批次', { selector: 'td' })).toBeTruthy()
  expect(screen.getByText('成品批次')).toBeTruthy(); expect(screen.getByText('零件A')).toBeTruthy()
  expect(screen.getByText('1.25')).toBeTruthy()
  expect(fetch).toHaveBeenCalledWith(`/api/traces/by-${kind}?${kind === 'part' ? 'part_code' : 'batch_no'}=A%20%2F%E4%B8%AD`, expect.anything())
})
it('handles blank, empty and failed queries without stale results', async () => {
  const fetch = vi.fn().mockResolvedValueOnce(new Response('[]')).mockResolvedValueOnce(new Response('{"error":"查询故障"}', { status: 500 }))
  vi.stubGlobal('fetch', fetch); show(); const u = userEvent.setup(); const button = screen.getByRole('button', { name: '查询', exact: true })
  await u.click(button); expect(fetch).not.toHaveBeenCalled(); expect(screen.getByText('请输入查询值')).toBeTruthy()
  await u.type(screen.getByPlaceholderText('输入完整批次号或零件编码'), 'missing'); await u.click(button)
  expect(await screen.findByRole('status')).toHaveProperty('textContent', '未找到追溯记录')
  await u.click(button); expect(await screen.findByText('查询故障')).toBeTruthy(); expect(screen.queryByRole('status')).toBeNull()
})
