import { afterEach, expect, it, vi } from 'vitest'
import { bom, type BOMItem, type Part } from '../src/lib/catalog'
import { findShortages } from '../src/lib/batch'

afterEach(() => vi.unstubAllGlobals())
it.each([null, []])('empty BOM response %j can pass completion preflight', async (response) => {
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(response)))
  vi.stubGlobal('fetch', fetch)
  const items = await bom.list(42)
  expect(items).toEqual([])
  expect(findShortages(3, items, [])).toEqual([])
  expect(fetch).toHaveBeenCalledWith('/api/bom?product_id=42', expect.anything())
})
it('populated BOM still reports a real shortage', async () => {
  const item = { id: 1, part_id: 7, product_id: 42, quantity: 2, loss_rate: 0, use_mode: 0 } as BOMItem
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify([item]))))
  const items = await bom.list(42)
  expect(items).toEqual([item])
  expect(findShortages(3, items, [{ id: 7, stock_qty: 5 } as Part])).toEqual([
    { partId: 7, name: '', code: '', needed: 6, have: 5 },
  ])
})
it('failed BOM request is not treated as an empty BOM', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{"error":"unavailable"}', { status: 500 })))
  await expect(bom.list(42)).rejects.toThrow('unavailable')
})
