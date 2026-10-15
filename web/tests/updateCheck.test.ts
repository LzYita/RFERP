// @vitest-environment jsdom
//
// 登录后的自动更新检查（2026-10-15）。
//
// 背景：1.3.0 的更新检查写在 launchMain 里，也就是**登录之后**
// （cmd/desktop/main.go:291-309）；Wails 版一度放在启动后 3 秒（登录之前），
// 于是用户刚坐下、可能正在录入数据时弹框，点「是」就立刻重启。时机改回登录后，
// 触发点挂在"登录态成立"上，手输密码与「记住登录」自动恢复两条路都覆盖。
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useUpdateCheckOnLogin } from '../src/lib/useUpdateCheck'

afterEach(() => {
  vi.unstubAllGlobals()
})

/** 记录对 /api/update/prompt 的调用次数；其它请求一律 200 空对象。 */
function stubFetch(promptStatus = 202) {
  const calls: string[] = []
  vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
    const u = String(url)
    if (u.includes('/api/update/prompt')) {
      calls.push(u)
      return new Response(JSON.stringify({ status: 'prompted' }), { status: promptStatus })
    }
    void init
    return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })
  })
  return calls
}

describe('登录后的自动更新检查', () => {
  it('未登录时不触发', async () => {
    const calls = stubFetch()
    renderHook(() => useUpdateCheckOnLogin(null))
    await new Promise((r) => setTimeout(r, 10))
    expect(calls.filter((u) => u.includes('/api/update/prompt'))).toHaveLength(0)
  })

  it('登录态成立后恰好触发一次', async () => {
    const calls = stubFetch()
    renderHook(() => useUpdateCheckOnLogin({ id: 1 }))
    await waitFor(() => expect(calls.filter((u) => u.includes('/api/update/prompt'))).toHaveLength(1))
  })

  it('me 对象换了引用也不会重复触发（同一次挂载只查一次）', async () => {
    const calls = stubFetch()
    const { rerender } = renderHook(({ me }: { me: { id: number } | null }) =>
      useUpdateCheckOnLogin(me),
      { initialProps: { me: { id: 1 } as { id: number } | null } },
    )
    await waitFor(() => expect(calls).toHaveLength(1))
    rerender({ me: { id: 1 } })
    rerender({ me: { id: 2 } })
    await new Promise((r) => setTimeout(r, 20))
    expect(calls).toHaveLength(1)
  })

  it('服务端报错也必须被吞掉：查不到更新绝不能让登录流程出问题', async () => {
    const calls = stubFetch(409)
    renderHook(() => useUpdateCheckOnLogin({ id: 1 }))
    // 没有 unhandled rejection，且请求确实发出去了
    await waitFor(() => expect(calls).toHaveLength(1))
    await new Promise((r) => setTimeout(r, 20))
  })

  it('App 确实把这个 hook 接上了（防止有人把这一行删掉而没人发现）', () => {
    const src = readFileSync(resolve(__dirname, '../src/App.tsx'), 'utf-8')
    expect(src).toMatch(/useUpdateCheckOnLogin\(me\)/)
  })
})