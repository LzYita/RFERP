// Exercise the gate function without importing its top-level server/browser run.
import { readFileSync } from 'node:fs'
import { EventEmitter } from 'node:events'
import { stripVTControlCharacters } from 'node:util'
import path from 'node:path'
import { describe, expect, it, vi } from 'vitest'

const source = readFileSync(new URL('../scripts/browser-gate.mjs', import.meta.url), 'utf8')
const frontend = source.slice(source.indexOf('async function startFrontend('), source.indexOf('async function launchBrowser('))
const url = 'http://127.0.0.1:5187/'

function harness(chunks: string[], exitCode: number | null = null) {
  const child = { stdout: new EventEmitter(), stderr: new EventEmitter(), exitCode: null as number | null }
  const fetch = vi.fn(async () => ({ ok: true }))
  const run = vi.fn(() => child)
  let pollStopped = false
  const until = vi.fn(async (ready: () => Promise<boolean>) => {
    for (const chunk of chunks) child.stdout.emit('data', Buffer.from(chunk))
    child.exitCode = exitCode
    pollStopped = await ready()
    return pollStopped
  })
  const start = new Function('run', 'path', 'WEB', 'API_PORT', 'VITE_PORT', 'VITE_URL', 'until', 'fetch', 'stripVTControlCharacters',
    `${frontend}; return startFrontend`)(run, path, '.', '19198', '5187', url, until, fetch, stripVTControlCharacters)
  return { start, fetch, run, pollStopped: () => pollStopped }
}

describe('browser gate Vite readiness', () => {
  it.each([
    ['plain banner', [`Local: ${url}`]],
    ['coloured port', ['Local: http://127.0.0.1:\x1b[1m5187\x1b[22m/']],
    ['escape split across chunks', ['Local: http://127.0.0.1:\x1b[', '1m5187\x1b[22', 'm/']],
  ])('accepts %s', async (_, chunks) => {
    const h = harness(chunks)
    await expect(h.start(false)).resolves.toBe(url)
    expect(h.fetch).toHaveBeenCalledWith(url)
    expect(h.run.mock.calls[0]?.[1]).toEqual(expect.arrayContaining(['--strictPort', '--host', '127.0.0.1', '--port', '5187']))
  })

  it('rejects an unrelated HTTP 200 without a listening banner', async () => {
    const h = harness([])
    await expect(h.start(false)).rejects.toThrow('vite 未就绪')
    expect(h.fetch).not.toHaveBeenCalled()
  })

  it('stops polling on Vite exit and reports a plain diagnostic', async () => {
    const h = harness(['\x1b[31mError: Vite entry missing\x1b[0m\n'], 1)
    await expect(h.start(false)).rejects.toThrow(`vite 未就绪（${url}）：Error: Vite entry missing`)
    expect(h.pollStopped()).toBe(true)
    expect(h.fetch).not.toHaveBeenCalled()
  })
})
