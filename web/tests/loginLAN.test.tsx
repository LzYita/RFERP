// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { LoginScreen } from '../src/screens/LoginScreen'
import { setToken } from '../src/lib/api'

vi.mock('../src/components/BrandMode', () => ({ BrandMode: () => null }))
afterEach(() => { cleanup(); setToken(null); vi.unstubAllGlobals() })

it.each([
  { code: 'device_limit', message: '授权客户端数已满', action: '删除闲置设备' },
  { code: 'license_missing', message: '尚未导入授权', action: '主机上导入授权文件' },
  { code: 'device_revoked', message: '该设备已被撤销', action: '删除该记录后重新登录' },
])('shows the $code login remedy', async ({ code, message, action }) => {
  vi.stubGlobal('fetch', vi.fn(async (url) => {
    if (String(url).includes('/api/remember/load')) return new Response('{"token":null,"enabled":false}')
    return new Response(JSON.stringify({ code, error: message }), { status: 403 })
  }))
  render(<LoginScreen onLoggedIn={vi.fn()} />)
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('用户名'), 'admin')
  await user.type(screen.getByLabelText('密码'), 'password')
  await user.click(screen.getByRole('button', { name: '登录', exact: true }))
  const alert = await screen.findByRole('alert')
  expect(alert.textContent).toContain(message)
  expect(alert.textContent).toContain(action)
  expect(alert.textContent).not.toContain('账号已停用')
  expect(screen.queryByRole('checkbox')).toBeNull()
})

it('keeps remembered login available for standalone mode', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('{"token":null}')))
  render(<LoginScreen onLoggedIn={vi.fn()} />)
  await waitFor(() => expect(screen.getByRole('button', { name: '登录', exact: true }).hasAttribute('disabled')).toBe(false))
  expect(screen.getByRole('checkbox')).not.toBeNull()
})
