import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

/**
 * 密码可见性图标曾经出错两次，两次都是「图标显示的东西和实际状态相反」：
 *
 *  - 第一轮：LoginScreen 传 `hidden={showPwd}`，点「显示密码」后变成闭眼，
 *    看着像「现在被遮住了」。
 *  - 第二轮：Round 6 新写 PasswordInput 时用了 emoji 🙈/👁，与既有实现
 *    不一致，而且 🙈 本身就是「藏起来」的意思，正好把语义搞反。
 *
 * 这条测试钉两件事：
 *  1. EyeIcon 只有一处定义，三处共用（不同步就会各画各的）。
 *  2. 任何调用点传的都是 `!shown`，不是 `shown`。
 */

const SRC = 'src'

function walk(d: string, out: string[] = []): string[] {
  for (const e of readdirSync(d)) {
    const f = join(d, e)
    if (statSync(f).isDirectory()) walk(f, out)
    else if (/\.tsx?$/.test(f)) out.push(f)
  }
  return out
}

const files = walk(SRC)

/** join() 在 Windows 上产生反斜杠，比较前统一成 /。 */
const norm = (f: string) => f.replace(/\\/g, '/')

describe('EyeIcon 只有一处定义', () => {
  it('components/form.tsx 是唯一定义处', () => {
    const definers = files
      .filter((f) => /function EyeIcon\s*\(/.test(readFileSync(f, 'utf8')))
      .map(norm)
    expect(definers).toEqual(['src/components/form.tsx'])
  })

  it('所有用到 <EyeIcon 的文件都从 form.tsx 导入', () => {
    // form.tsx 自己既是定义处也是使用处，不能要求它「从自己导入」
    const users = files.filter(
      (f) =>
        norm(f) !== 'src/components/form.tsx' &&
        readFileSync(f, 'utf8').includes('<EyeIcon'),
    )
    expect(users.length, '至少应有两个屏在用 EyeIcon').toBeGreaterThan(1)
    for (const f of users) {
      const t = readFileSync(f, 'utf8')
      const imported = /import\s*\{[^}]*EyeIcon[^}]*\}\s*from\s*'[^']*components\/form'/.test(t)
      expect(imported, `${norm(f)} 用了 <EyeIcon 却没从 components/form 导入`).toBe(true)
    }
  })
})

describe('图标语义不反转', () => {
  it('所有调用点传的都是取反后的值', () => {
    // 含 form.tsx 自己的调用点：PasswordInput 里的那一处恰恰是最容易再次
    // 写反的地方（它是「新建用户/重置密码」两个弹窗实际用的那一个）。
    const users = files.filter((f) => readFileSync(f, 'utf8').includes('<EyeIcon'))
    let total = 0
    for (const f of users) {
      const t = readFileSync(f, 'utf8')
      const calls = [...t.matchAll(/<EyeIcon\s+hidden=\{([^}]+)\}/g)].map((m) => m[1].trim())
      expect(calls.length, `${norm(f)} 里的 <EyeIcon 调用`).toBeGreaterThan(0)
      total += calls.length
      for (const c of calls) {
        expect(c.startsWith('!'), `${norm(f)}: <EyeIcon hidden={${c}}> 应写成 hidden={!${c}}`).toBe(true)
      }
    }
    // 登录页、注册页、PasswordInput —— 至少 4 处
    expect(total).toBeGreaterThanOrEqual(4)
  })

  it('hidden=true 画闭眼，false 画睁眼', () => {
    const t = readFileSync('src/components/form.tsx', 'utf8')
    const m = /export function EyeIcon[\s\S]*?\n}/.exec(t)
    expect(m, '未找到 EyeIcon 定义').toBeTruthy()
    const body = m![0]
    // hidden 分支里有那条对角线（划掉的标志），睁眼分支里有 circle（瞳孔）
    const hiddenBranch = body.slice(body.indexOf('{hidden ?'), body.indexOf(') : ('))
    expect(hiddenBranch).toContain('M17.94 17.94')
    expect(hiddenBranch).toContain('<line x1="2" y1="2" x2="22" y2="22"')
  })
})

describe('没有 emoji 图标残留', () => {
  it('form.tsx 里不含 🙈 / 👁', () => {
    const t = readFileSync('src/components/form.tsx', 'utf8')
    expect(t).not.toContain('\u{1F648}')
    expect(t).not.toContain('\u{1F441}')
  })
})