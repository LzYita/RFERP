// @vitest-environment jsdom
//
// 登录后的自动更新检查。
//
// 背景：1.3.0 的更新检查写在 launchMain 里，也就是**登录之后**
// （cmd/desktop/main.go:291-309）；Wails 版一度放在启动后 3 秒（登录之前），
// 于是用户刚坐下、可能正在录入数据时弹框，点「是」就立刻重启。时机改回登录后，
// 触发点挂在"登录态成立"上，手输密码与「记住登录」自动恢复两条路都覆盖。
//
// 2026-10-17：这条检查原先调 `POST /api/update/prompt` 让**服务端**弹原生框，
// 那个框把整份更新说明塞进 MessageBox，窗口被撑到按钮在屏幕外。现在改成调
// `GET /api/update/check`，由前端渲染网页弹窗 —— 所以这里断言的也从"发了 prompt
// 请求"变成"把查到的清单交回给界面"。
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import { useUpdateCheckOnLogin } from '../src/lib/useUpdateCheck'

afterEach(() => {
  vi.unstubAllGlobals()
})

const CHECK = '/api/update/check'

/**
 * 记录对 check 的调用次数。
 *
 * `body` 决定 check 返回什么：默认是"没配置更新源"，改它就能造出"有新版"。
 * 别的端点一律 200 空对象 —— 本组测试只关心 check 这一步。
 */
function stubFetch(body: unknown = {}, status = 200) {
  const calls: string[] = []
  vi.stubGlobal('fetch', async (url: string) => {
    const u = String(url)
    if (u.includes(CHECK)) {
      calls.push(u)
      return new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })
  })
  return calls
}

const HAS_UPDATE = {
  enabled: true,
  hasUpdate: true,
  version: '1.3.0',
  currentVersion: '1.2.2',
  sections: [{ title: '一、新功能', body: '- 侧栏新增「设置」入口' }],
}

describe('登录后的自动更新检查', () => {
  it('未登录时不触发', async () => {
    const calls = stubFetch(HAS_UPDATE)
    renderHook(() => useUpdateCheckOnLogin(null))
    await new Promise((r) => setTimeout(r, 10))
    expect(calls).toHaveLength(0)
  })

  it('登录态成立后恰好触发一次', async () => {
    const calls = stubFetch(HAS_UPDATE)
    renderHook(() => useUpdateCheckOnLogin({ id: 1 }))
    await waitFor(() => expect(calls).toHaveLength(1))
  })

  it('me 对象换了引用也不会重复触发（同一次挂载只查一次）', async () => {
    const calls = stubFetch(HAS_UPDATE)
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
    const calls = stubFetch({}, 502)
    const { result } = renderHook(() => useUpdateCheckOnLogin({ id: 1 }))
    // 没有 unhandled rejection，且请求确实发出去了，pending 保持为空
    await waitFor(() => expect(calls).toHaveLength(1))
    await new Promise((r) => setTimeout(r, 20))
    expect(result.current.pending).toBeNull()
  })

  it('查到新版本就把清单交回界面（连同已切好的小节）', async () => {
    stubFetch(HAS_UPDATE)
    const { result } = renderHook(() => useUpdateCheckOnLogin({ id: 1 }))
    await waitFor(() => expect(result.current.pending).not.toBeNull())
    expect(result.current.pending?.version).toBe('1.3.0')
    // 正文必须带着 sections 过来：弹窗直接渲染它们，不再自己切 `== 中文 ==`
    expect(result.current.pending?.sections).toHaveLength(1)
  })

  // 没配置更新源 / 已是最新都是常态，弹窗只会变成噪音。
  it.each([
    ['未配置更新源', { enabled: false }],
    ['已是最新', { enabled: true, hasUpdate: false }],
    // 验签被拒要留给设置屏的「查询最新更新」显示：那里有上下文，
    // 也不该由一次后台检查打断用户（先决项⑤）。
    ['验签被拒', { enabled: true, hasUpdate: false, warning: '清单未通过校验' }],
  ])('%s 时不弹窗', async (_name, body) => {
    stubFetch(body)
    const { result } = renderHook(() => useUpdateCheckOnLogin({ id: 1 }))
    await new Promise((r) => setTimeout(r, 30))
    expect(result.current.pending).toBeNull()
  })

  it('用户点「稍后」后能收起，界面回到原样', async () => {
    stubFetch(HAS_UPDATE)
    const { result } = renderHook(() => useUpdateCheckOnLogin({ id: 1 }))
    await waitFor(() => expect(result.current.pending).not.toBeNull())
    act(() => result.current.dismiss())
    expect(result.current.pending).toBeNull()
  })

  it('App 确实把这个 hook 接上了（防止有人把这一行删掉而没人发现）', () => {
    const src = readFileSync(resolve(__dirname, '../src/App.tsx'), 'utf-8')
    expect(src).toMatch(/useUpdateCheckOnLogin\(me\)/)
    // 而且真的渲染了弹窗，而不是拿到 pending 就丢掉
    expect(src).toMatch(/<UpdatePromptModal/)
  })
})
