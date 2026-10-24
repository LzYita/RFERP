// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { RestoreRestart } from '../src/screens/restoreRestart'

afterEach(() => { cleanup(); vi.unstubAllGlobals() })

it('blocks dismissal and exits through the restore endpoint', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response('{"status":"exiting"}'))
  vi.stubGlobal('fetch', fetch)
  render(<RestoreRestart />)
  const u = userEvent.setup()
  await u.keyboard('{Escape}')
  expect(screen.getByRole('dialog', { name: '需要重新启动' })).toBeTruthy()
  expect(screen.queryByRole('button', { name: '关闭' })).toBeNull()
  await u.click(screen.getByRole('button', { name: '退出程序' }))
  expect(fetch).toHaveBeenCalledWith('/api/restore/exit', expect.objectContaining({ method: 'POST' }))
  expect((screen.getByRole('button', { name: '正在退出…' }) as HTMLButtonElement).disabled).toBe(true)
})

it('keeps uncertain restore warning and manual exit instructions on failure', async () => {
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')))
  render(<RestoreRestart uncertain />)
  await userEvent.click(screen.getByRole('button', { name: '退出程序' }))
  expect(screen.getByText(/恢复结果未确认/).textContent).toContain('勿直接重试')
  expect((await screen.findByRole('alert')).textContent).toContain('关闭程序窗口后重新打开')
  expect((screen.getByRole('button', { name: '退出程序' }) as HTMLButtonElement).disabled).toBe(false)
})
