// @vitest-environment jsdom
//
// 帮助文档渲染器（Round 7 设置屏用的是它）。
//
// 这一层的价值全在「子集边界」上：internal/help 的静态检查保证内置文档只用到
// 标题/分隔线/列表/段落/粗体/行内代码，**没有**表格、引用块、代码块。
// 这里钉住两件事：
//   1. 子集内的写法确实渲染成对应元素（不是原样打出星号）；
//   2. 越界的写法（表格行、引用块、裸 HTML）**按字面量**显示，绝不变成元素
//      —— 帮助内容来自程序内置文件，但渲染器不该因此就假定它一定安全。
import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render } from '@testing-library/react'
import { Markdown } from '../src/components/Markdown'
import { parseBlocks } from '../src/lib/markdown'

afterEach(cleanup)

function renderMarkdown(src: string) {
  const { container } = render(<Markdown source={src} />)
  return container
}

describe('受支持的子集', () => {
  it('标题按层级渲染成 h1..h4', () => {
    const c = renderMarkdown('# 一级\n\n## 二级\n\n### 三级\n\n#### 四级')
    expect(c.querySelector('h1')?.textContent).toBe('一级')
    expect(c.querySelector('h2')?.textContent).toBe('二级')
    expect(c.querySelector('h3')?.textContent).toBe('三级')
    expect(c.querySelector('h4')?.textContent).toBe('四级')
  })

  it('--- 渲染成分隔线，而不是一段文字', () => {
    const c = renderMarkdown('上一段\n\n---\n\n下一段')
    expect(c.querySelectorAll('hr')).toHaveLength(1)
    expect(c.textContent).not.toContain('---')
  })

  it('- 列表渲染成 ul/li', () => {
    const c = renderMarkdown('- **正在运行** —— 直接进入登录界面\n- 未运行 —— 弹出提示')
    expect(c.querySelectorAll('ul li')).toHaveLength(2)
    expect(c.querySelector('ul li strong')?.textContent).toBe('正在运行')
  })

  it('数字编号渲染成 ol/li（指南 5.3 的四道闸门就是这么写的）', () => {
    const c = renderMarkdown('1. **文件校验** —— 文件必须存在\n2. **确认口令** —— 必须手输')
    expect(c.querySelectorAll('ol li')).toHaveLength(2)
    expect(c.querySelector('ol li')?.textContent).toContain('文件校验')
  })

  it('· 也当项目符号（更新说明的二级条目）', () => {
    const c = renderMarkdown('· MySQL：升级前生成一份 .sql 备份')
    expect(c.querySelectorAll('ul li')).toHaveLength(1)
    expect(c.textContent).not.toContain('·')
  })

  it('**粗体** 与 `行内代码` 都渲染成元素', () => {
    const c = renderMarkdown('配置一直保存在 `config.json` 里，**无需重新配置**。')
    expect(c.querySelector('code')?.textContent).toBe('config.json')
    expect(c.querySelector('strong')?.textContent).toBe('无需重新配置')
    expect(c.textContent).not.toContain('**')
    expect(c.textContent).not.toContain('`')
  })
})

describe('软换行', () => {
  it('中文段落换行不插空格（否则句号后会多一道缝）', () => {
    const c = renderMarkdown('也是唯一能进「用户管理」的角色。\n新建账号只能由管理员完成。')
    expect(c.querySelector('p')?.textContent).toBe(
      '也是唯一能进「用户管理」的角色。新建账号只能由管理员完成。',
    )
  })

  it('英文段落换行插空格（否则 thebatch 会黏在一起）', () => {
    const c = renderMarkdown('Restoring a database\nnow takes a backup first')
    expect(c.querySelector('p')?.textContent).toBe('Restoring a database now takes a backup first')
  })
})

describe('越界写法不得变成元素', () => {
  it('markdown 表格行原样显示（Fyne 渲染不出表格，内容约束里就禁掉了）', () => {
    const c = renderMarkdown('| 项 | 决定 |\n| 前端 | React |')
    expect(c.querySelector('table')).toBeNull()
    expect(c.textContent).toContain('| 项 | 决定 |')
  })

  it('引用块原样显示，不渲染成 blockquote', () => {
    const c = renderMarkdown('> 这是一段引用')
    expect(c.querySelector('blockquote')).toBeNull()
    expect(c.textContent).toContain('> 这是一段引用')
  })

  it('裸 HTML 被转义成文字，不会变成元素', () => {
    const c = renderMarkdown('正常文字 <img src=x onerror=alert(1)> 后面还有\n\n<script>alert(1)</script>')
    expect(c.querySelector('img')).toBeNull()
    expect(c.querySelector('script')).toBeNull()
    expect(c.textContent).toContain('<img src=x onerror=alert(1)>')
    expect(c.textContent).toContain('<script>alert(1)</script>')
  })
})

describe('parseBlocks', () => {
  it('空行分隔段落，空文档得到空数组', () => {
    expect(parseBlocks('')).toEqual([])
    expect(parseBlocks(' \n\n  ')).toEqual([])
    expect(parseBlocks('甲\n\n乙').map((b) => b.kind)).toEqual(['p', 'p'])
  })

  it('相邻的列表行合成一个块（不拆成每行一个列表）', () => {
    const blocks = parseBlocks('- 甲\n- 乙\n- 丙')
    expect(blocks).toHaveLength(1)
    expect(blocks[0]).toEqual({ kind: 'ul', items: ['甲', '乙', '丙'] })
  })

  it('未闭合的 ** 按字面量留在文字里，不吞字符', () => {
    const c = renderMarkdown('**没有闭合的粗体')
    expect(c.textContent).toBe('**没有闭合的粗体')
    expect(c.querySelector('strong')).toBeNull()
  })
})
