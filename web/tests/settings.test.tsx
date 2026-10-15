// @vitest-environment jsdom
//
// 设置屏（Round 7）。三个页签的内容与服务端 /api/help 一次取回；
// 版本号用服务端自报值，不许前端写死。
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SettingsScreen } from '../src/screens/SettingsScreen'
import { renderScreen } from '../src/screens/registry'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const HELP = {
  // 与前端常量（App.tsx 的 VERSION）刻意不同：能测出「到底显示的是哪一个」
  currentVersion: '2.0.0',
  latestVersion: '1.3.0',
  guide: '# RFERP 使用指南\n\n本指南随程序内置，可离线阅读。\n\n- **正在运行** —— 直接进入登录界面\n',
  faq: '# 常见问题\n\n**Q：忘记密码？**\n请联系管理员重置。\n',
  versions: [
    {
      version: '1.3.0',
      publishedAt: '2026-09-30T18:01:27Z',
      sections: [{ title: '一、新功能', body: '- 侧栏左下角新增「设置」入口\n' }],
    },
    {
      version: '1.2.2',
      publishedAt: '2026-09-25T05:53:17Z',
      sections: [{ title: '一、问题修复', body: '- 打开「操作记录」不再闪退\n' }],
    },
  ],
}

/** 按 URL 分派响应；未匹配到的一律 500，避免"没想清楚"的请求静默通过。 */
function stubApi(update: unknown = { enabled: true, hasUpdate: false }) {
  const calls: string[] = []
  vi.stubGlobal('fetch', async (url: string) => {
    const u = String(url)
    calls.push(u)
    if (u.includes('/api/update/download')) {
      return new Response(JSON.stringify({ jobId: 'job-1', version: '2.0.1', total: 1024 }), {
        status: 202,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    if (u.includes('/api/update/progress')) {
      // 一次就报完成，测试不必等轮询
      return new Response(JSON.stringify({ jobId: 'job-1', done: 1024, total: 1024, state: 'done' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    if (u.includes('/api/update/apply')) {
      return new Response(JSON.stringify({ status: 'restarting' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    const payload = u.includes('/api/update/check') ? update : HELP
    return new Response(JSON.stringify(payload), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  })
  return calls
}

async function renderSettings() {
  render(<SettingsScreen />)
  // 内容来自数组请求，等它落地再断言
  await screen.findByText(/当前版本/)
}

describe('设置屏', () => {
  it('默认展开使用指南，并显示服务端自报的版本与内置最新版本', async () => {
    stubApi()
    await renderSettings()

    expect(screen.getByRole('tab', { name: '使用指南' })).toHaveProperty('ariaSelected', 'true')
    expect(screen.getByText('当前版本 v2.0.0')).toBeTruthy()
    expect(screen.getByText('内置记录最新 v1.3.0')).toBeTruthy()
    // 指南正文确实渲染了（不是只显示了一句版本号）
    expect(screen.getByText('正在运行')).toBeTruthy()
  })

  it('切到常见问题显示 FAQ，指南正文随之消失', async () => {
    stubApi()
    await renderSettings()

    await userEvent.click(screen.getByRole('tab', { name: '常见问题' }))
    expect(screen.getByText('Q：忘记密码？')).toBeTruthy()
    expect(screen.queryByText('正在运行')).toBeNull()
  })

  it('更新说明列出全部版本，默认展开最新的那条', async () => {
    stubApi()
    await renderSettings()

    await userEvent.click(screen.getByRole('tab', { name: '更新说明' }))
    const list = screen.getByRole('list', { name: '版本列表' })
    expect(list.textContent).toContain('v1.3.0')
    expect(list.textContent).toContain('v1.2.2')
    expect(screen.getByText('侧栏左下角新增「设置」入口')).toBeTruthy()
    expect(screen.getByText('发布于 2026-09-30')).toBeTruthy()
  })

  it('点另一个版本切换到它的说明', async () => {
    stubApi()
    await renderSettings()

    await userEvent.click(screen.getByRole('tab', { name: '更新说明' }))
    await userEvent.click(screen.getByRole('button', { name: 'v1.2.2' }))
    expect(screen.getByText('打开「操作记录」不再闪退')).toBeTruthy()
    expect(screen.queryByText('侧栏左下角新增「设置」入口')).toBeNull()
  })

  describe('查询最新更新', () => {
    it('已是最新时说明当前版本已是最新', async () => {
      const calls = stubApi({ enabled: true, hasUpdate: false })
      await renderSettings()

      await userEvent.click(screen.getByRole('button', { name: '查询最新更新' }))
      await waitFor(() => expect(screen.getByRole('status').textContent).toBe('当前 v2.0.0 已是最新版本。'))
      // 没有新版就不该去弹框
      expect(calls.some((u) => u.includes('/api/update/prompt'))).toBe(false)
    })

    it('有源验签被拒时不能显示成"已是最新版本"（先决项⑤）', async () => {
      const calls = stubApi({
        enabled: true,
        hasUpdate: false,
        warning: 'http://configured.invalid/releases.json：更新清单签名校验失败',
      })
      await renderSettings()

      await userEvent.click(screen.getByRole('button', { name: '查询最新更新' }))
      await waitFor(() => expect(screen.getByRole('status').textContent).toContain('未通过校验'))
      // 关键：绝不能显示"已是最新版本"——那会把一次发版事故伪装成一切正常
      expect(screen.getByRole('status').textContent).not.toContain('已是最新版本')
      expect(screen.getByRole('status').textContent).toContain('签名校验失败')
      // 源有问题时不该出现安装入口
      expect(screen.queryByRole('button', { name: '下载并安装' })).toBeNull()
      expect(calls.some((u) => u.includes('/api/update/download'))).toBe(false)
    })

    it('查到新版本就给出「下载并安装」入口，并说清新版本号', async () => {
      stubApi({ enabled: true, hasUpdate: true, version: '2.0.1' })
      await renderSettings()

      await userEvent.click(screen.getByRole('button', { name: '查询最新更新' }))
      await waitFor(() => expect(screen.getByRole('status').textContent).toContain('v2.0.1'))
      // 查到新版后才出现安装入口（没有新版时不出现）
      expect(screen.getByRole('button', { name: '下载并安装' })).toBeTruthy()
    })

    it('没有新版本时不出现「下载并安装」入口', async () => {
      stubApi({ enabled: true, hasUpdate: false })
      await renderSettings()

      await userEvent.click(screen.getByRole('button', { name: '查询最新更新' }))
      await waitFor(() =>
        expect(screen.getByRole('status').textContent).toBe('当前 v2.0.0 已是最新版本。'),
      )
      expect(screen.queryByRole('button', { name: '下载并安装' })).toBeNull()
    })

    it('点「下载并安装」先弹确认框，确认后才下载并应用', async () => {
      const calls = stubApi({ enabled: true, hasUpdate: true, version: '2.0.1' })
      await renderSettings()

      await userEvent.click(screen.getByRole('button', { name: '查询最新更新' }))
      await waitFor(() => expect(screen.getByRole('button', { name: '下载并安装' })).toBeTruthy())

      // 确认框没确认之前，绝不该开始下载
      await userEvent.click(screen.getByRole('button', { name: '下载并安装' }))
      expect(screen.getByText(/安装完成后程序会/)).toBeTruthy()
      expect(calls.some((u) => u.includes('/api/update/download'))).toBe(false)

      await userEvent.click(screen.getByRole('button', { name: '下载 v2.0.1' }))
      await waitFor(() =>
        expect(calls.some((u) => u.includes('/api/update/apply'))).toBe(true),
      )
      // 走的是 HTTP 那条链：download → progress → apply
      expect(calls.some((u) => u.includes('/api/update/download?async=1'))).toBe(true)
      expect(calls.some((u) => u.includes('/api/update/progress'))).toBe(true)
    })

    it('取消确认框就什么都不做', async () => {
      const calls = stubApi({ enabled: true, hasUpdate: true, version: '2.0.1' })
      await renderSettings()

      await userEvent.click(screen.getByRole('button', { name: '查询最新更新' }))
      await waitFor(() => expect(screen.getByRole('button', { name: '下载并安装' })).toBeTruthy())
      await userEvent.click(screen.getByRole('button', { name: '下载并安装' }))

      await userEvent.click(screen.getByRole('button', { name: '取消' }))
      expect(calls.some((u) => u.includes('/api/update/download'))).toBe(false)
      expect(calls.some((u) => u.includes('/api/update/apply'))).toBe(false)
    })

    it('下载失败时把原因带出来，且不去应用', async () => {
      const calls = stubApi({ enabled: true, hasUpdate: true, version: '2.0.1' })
      await renderSettings()
      vi.stubGlobal('fetch', async (url: string) => {
        const u = String(url)
        calls.push(u)
        if (u.includes('/api/update/download')) {
          return new Response(JSON.stringify({ jobId: 'job-1', version: '2.0.1', total: 1024 }), {
            status: 202,
          })
        }
        if (u.includes('/api/update/progress')) {
          return new Response(
            JSON.stringify({ jobId: 'job-1', done: 0, total: 1024, state: 'error', error: '网络中断' }),
            { status: 200 },
          )
        }
        if (u.includes('/api/update/check')) {
          return new Response(JSON.stringify({ enabled: true, hasUpdate: true, version: '2.0.1' }))
        }
        return new Response(JSON.stringify(HELP), { status: 200 })
      })

      await userEvent.click(screen.getByRole('button', { name: '查询最新更新' }))
      await waitFor(() => expect(screen.getByRole('button', { name: '下载并安装' })).toBeTruthy())
      await userEvent.click(screen.getByRole('button', { name: '下载并安装' }))
      await userEvent.click(screen.getByRole('button', { name: '下载 v2.0.1' }))

      await waitFor(() =>
        expect(screen.getByRole('status').textContent).toContain('更新失败：网络中断'),
      )
      // 下载失败绝不能去 apply —— 那会把一个不完整的包推给替换程序
      expect(calls.some((u) => u.includes('/api/update/apply'))).toBe(false)
    })

    it('未配置更新地址时说清楚是没配置，而不是报成故障', async () => {
      const calls = stubApi({ enabled: false })
      await renderSettings()

      await userEvent.click(screen.getByRole('button', { name: '查询最新更新' }))
      await waitFor(() =>
        expect(screen.getByRole('status').textContent).toBe('未配置更新地址，无法在线查询。'),
      )
      expect(calls.some((u) => u.includes('/api/update/prompt'))).toBe(false)
    })

    it('查询失败时把原因带出来（不静默）', async () => {
      stubApi()
      await renderSettings()
      // 第二次请求开始返回 502
      vi.stubGlobal('fetch', async (url: string) => {
        if (String(url).includes('/api/update/check')) {
          return new Response(JSON.stringify({ error: '无法获取更新信息' }), { status: 502 })
        }
        return new Response(JSON.stringify(HELP), { status: 200 })
      })

      await userEvent.click(screen.getByRole('button', { name: '查询最新更新' }))
      await waitFor(() =>
        expect(screen.getByRole('status').textContent).toContain('查询失败：无法获取更新信息'),
      )
    })
  })
})

describe('导航接线', () => {
  it('renderScreen("settings") 渲染设置屏，而不是占位屏', async () => {
    stubApi()
    render(<>{renderScreen('settings', { meId: 1 })}</>)
    expect(await screen.findByRole('tab', { name: '使用指南' })).toBeTruthy()
    expect(screen.getByRole('tablist', { name: '设置' })).toBeTruthy()
  })

  it('未知的键走兜底，并把键名显示出来（便于发现注册表漏项）', () => {
    render(<>{renderScreen('nope', { meId: 1 })}</>)
    expect(screen.getByText('nope')).toBeTruthy()
    expect(screen.getByText(/没有这个屏幕/)).toBeTruthy()
  })
})
