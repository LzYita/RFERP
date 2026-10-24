// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { UsersScreen } from '../src/screens/UsersScreen'

vi.mock('../src/lib/api', async (original) => ({
  ...await original<typeof import('../src/lib/api')>(),
  usePermissions: () => ({ modules: { users: 'write' } }),
}))
afterEach(() => { cleanup(); vi.unstubAllGlobals() })

it.each([
  { name: 'name only', displayName: 'new name', role: 'viewer', status: 1, cancel: false },
  { name: 'role only', displayName: 'old name', role: 'warehouse', status: 1, cancel: false },
  { name: 'name role and status', displayName: 'new name', role: 'warehouse', status: 0, cancel: false },
  { name: 'cancel role confirmation', displayName: 'new name', role: 'warehouse', status: 1, cancel: true },
])('$name submits the confirmed draft', async ({ displayName, role, status, cancel }) => {
  const writes: unknown[] = []
  vi.stubGlobal('fetch', vi.fn(async (_url, init) => {
    if (init?.method === 'PUT') { writes.push(JSON.parse(init.body)); return new Response('{"status":"ok"}') }
    return new Response(JSON.stringify([{ id: 7, username: 'target', display_name: 'old name', role: 'viewer', status: 1 }]))
  }))
  render(<UsersScreen meId={1} />)
  const u = userEvent.setup()
  await u.click(await screen.findByLabelText('选择 target'))
  await u.click(screen.getByRole('button', { name: '编辑', exact: true }))
  const form = within(screen.getByRole('dialog', { name: '编辑用户' }))
  await u.clear(form.getByPlaceholderText('显示名'))
  await u.type(form.getByPlaceholderText('显示名'), '  ' + displayName + '  ')
  const selects = form.getAllByRole('combobox')
  await u.selectOptions(selects[0], role)
  await u.selectOptions(selects[1], String(status))
  await u.click(form.getByRole('button', { name: '确定', exact: true }))
  if (role !== 'viewer') {
    const confirm = within(await screen.findByRole('dialog', { name: '确认变更角色' }))
    expect(writes).toEqual([])
    await u.click(confirm.getByRole('button', { name: cancel ? '取消' : '确定', exact: true }))
  }
  expect(writes).toEqual(cancel ? [] : [{ id: 7, displayName, role, status }])
})
