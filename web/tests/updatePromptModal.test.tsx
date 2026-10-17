// @vitest-environment jsdom
//
// 「发现新版本」弹窗（2026-10-17）。
//
// 它替代的是原生 MessageBox，而原生框有两个绕不开的问题，测试就是钉这两条：
//  1. **按钮不可达**：MessageBox 画不了 markdown、也没有滚动条，只能把窗口整体
//     拉高来塞下整份更新说明。1.3.0 的说明有上百行，于是「下载并安装」被顶到
//     屏幕之外 —— 用户既看不全也点不到（用户报的现象）。
//     → 正文区必须 `overflow-y-auto` 且限高，按钮必须留在**不跟着滚动**的 footer。
//  2. **符号裸露**：`== 中文 ==`、`- ` 以原文显示。
//     → 正文按服务端切好的 sections 交给 <Markdown> 渲染。
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { UpdatePromptModal } from '../src/screens/updatePromptModal'
import type { UpdateCheck } from '../src/lib/help'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

/** 造一份"有新版"的查询结果，正文用真实 changelog 的形状（含中英两段）。 */
function check(over: Partial<UpdateCheck> = {}): UpdateCheck {
  return {
    enabled: true,
    hasUpdate: true,
    version: '1.3.0',
    currentVersion: '1.2.2',
    size: 18 * 1024 * 1024,
    publishedAt: '2026-10-13',
    sections: [
      { title: '一、新功能', body: '- 侧栏左下角新增「设置」入口' },
      { title: '二、问题修复', body: '- 修复列表页点选靠下行时跳回顶部' },
    ],
    ...over,
  }
}

describe('更新提示弹窗', () => {
  it('把版本、当前版本、发布日期与包大小都摆出来', () => {
    render(<UpdatePromptModal check={check()} onClose={() => {}} />)
    expect(screen.getByText('发现新版本 v1.3.0')).toBeTruthy()
    expect(screen.getByText('当前 v1.2.2')).toBeTruthy()
    expect(screen.getByText('发布于 2026-10-13')).toBeTruthy()
    expect(screen.getByText('18.0 MB')).toBeTruthy()
  })

  // 核心断言：正文按小节渲染，而不是把 markdown 原文塞进去。
  it('正文按 sections 渲染成排版，不露出 `==` 与 `-` 原文', () => {
    render(<UpdatePromptModal check={check()} onClose={() => {}} />)
    expect(screen.getByText('一、新功能')).toBeTruthy()
    expect(screen.getByText('二、问题修复')).toBeTruthy()
    // markdown 渲染后 bullet 是列表项，文本里不该再带 "- " 前缀
    expect(screen.getByText('侧栏左下角新增「设置」入口')).toBeTruthy()
    expect(screen.queryByText(/^- 侧栏左下角/)).toBeNull()
    expect(screen.queryByText(/== 中文 ==/)).toBeNull()
  })

  // 按钮必须始终可见 —— 这条正对着原生框那个「点不到」的故障。
  it('正文限高滚动，按钮留在底部不随正文滚动', () => {
    const { container } = render(<UpdatePromptModal check={check()} onClose={() => {}} />)
    const scroller = container.querySelector('.overflow-y-auto') as HTMLElement
    expect(scroller).toBeTruthy()
    // 限高：不给 max-height 的话长 changelog 又会把窗口撑大
    expect(scroller.className).toMatch(/max-h-/)
    // 滚动能力来自 overflow-y-auto
    expect(scroller.className).toMatch(/overflow-y-auto/)

    // 按钮在 scroller 之外，所以正文再长也压不到它
    const btn = screen.getByRole('button', { name: '下载并安装' })
    expect(scroller.contains(btn)).toBe(false)
    expect(screen.getByRole('button', { name: '稍后' })).toBeTruthy()
  })

  it('「稍后」关闭弹窗', async () => {
    const onClose = vi.fn()
    render(<UpdatePromptModal check={check()} onClose={onClose} />)
    await userEvent.click(screen.getByRole('button', { name: '稍后' }))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('点「下载并安装」先进确认框，说明会重启并要签名', async () => {
    render(<UpdatePromptModal check={check()} onClose={() => {}} />)
    await userEvent.click(screen.getByRole('button', { name: '下载并安装' }))
    expect(screen.getByText('安装更新')).toBeTruthy()
    expect(screen.getByText(/自动重启/)).toBeTruthy()
    expect(screen.getByText(/数字签名/)).toBeTruthy()
    // 真正动手的那个按钮上带着版本号，避免用户不知道要装的是哪个
    expect(screen.getByRole('button', { name: '下载 v1.3.0' })).toBeTruthy()
  })

  it('确认框可以取消，取消后不发起任何下载请求', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    render(<UpdatePromptModal check={check()} onClose={() => {}} />)
    await userEvent.click(screen.getByRole('button', { name: '下载并安装' }))
    await userEvent.click(screen.getByRole('button', { name: '取消' }))
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('没有说明时给一句人话，而不是空白', () => {
    render(<UpdatePromptModal check={check({ sections: [] })} onClose={() => {}} />)
    expect(screen.getByText('这个版本没有附带更新说明。')).toBeTruthy()
  })
})
