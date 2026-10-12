import { describe, it, expect, vi, afterEach } from 'vitest'
import { users, passwordTooShort, roleLabel } from '../src/lib/users'

/**
 * 这些断言的价值全在一个点上：**字段名与必填性**。
 *
 * 四个用户端点直接反序列化进没有 json tag 的 usecase 结构体，字段名错了
 * 不会 400，只会静默忽略；PUT 又是三字段全量替换，漏一个就静默清空显示名
 * 或把账号停用。接口层测不出来（要打真实服务），所以在这里钉住形状。
 */

const calls: { url: string; method: string; body: any }[] = []

function stubFetch(status = 200, payload: any = {}) {
  calls.length = 0
  vi.stubGlobal('fetch', async (url: string, init: any) => {
    calls.push({ url, method: init?.method ?? 'GET', body: init?.body ? JSON.parse(init.body) : undefined })
    return new Response(JSON.stringify(payload), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })
  })
}

afterEach(() => vi.unstubAllGlobals())

describe('请求体的字段名', () => {
  it('创建用 displayName 驼峰，不是 display_name', async () => {
    stubFetch(200, { id: 1 })
    await users.create({
      username: 'wh1',
      password: 'pass1234',
      displayName: '仓管一',
      role: 'warehouse',
    })
    expect(calls[0].body).toEqual({
      username: 'wh1',
      password: 'pass1234',
      displayName: '仓管一',
      role: 'warehouse',
    })
    // 写成 display_name 会被后端静默丢弃，所以这个断言反过来说明它必须是驼峰
    expect(calls[0].body.display_name).toBeUndefined()
  })

  it('重置口令是 password，不是 newPassword', async () => {
    stubFetch(200, { status: 'ok' })
    await users.resetPassword(7, 'newpass1234')
    expect(calls[0].body).toEqual({ id: 7, password: 'newpass1234' })
    expect(calls[0].body.new_password).toBeUndefined()
  })

  it('改自己口令是 oldPass / newPass，不是 old_password', async () => {
    stubFetch(200, { status: 'ok' })
    await users.changePassword(3, 'old1234', 'new1234')
    expect(calls[0].body).toEqual({ id: 3, oldPass: 'old1234', newPass: 'new1234' })
  })

  it('删除走查询参数，不带 body', async () => {
    stubFetch(200, { status: 'ok' })
    await users.remove(9)
    expect(calls[0].url).toBe('/api/users?id=9')
    expect(calls[0].method).toBe('DELETE')
    expect(calls[0].body).toBeUndefined()
  })
})

describe('更新是全量替换，四个字段一个都不能少', () => {
  it('发出的 body 必须包含 id / displayName / role / status', async () => {
    stubFetch(200, { status: 'ok' })
    await users.update({ id: 5, displayName: '新名', role: 'viewer', status: 1 })
    const b = calls[0].body
    // 少发任何一个，后端都会写成零值：displayName→NULL、status→0（停用）、role→''
    for (const k of ['id', 'displayName', 'role', 'status']) {
      expect(b, `PUT 缺少 ${k} 会造成静默数据损坏`).toHaveProperty(k)
    }
    expect(b).toEqual({ id: 5, displayName: '新名', role: 'viewer', status: 1 })
  })
})

describe('口令长度按 rune 计，与后端 auth.ValidatePassword 同规则', () => {
  it('不足 4 位被拦', () => {
    expect(passwordTooShort('')).toBe(true)
    expect(passwordTooShort('abc')).toBe(true)
    expect(passwordTooShort('abcd')).toBe(false)
  })

  it('中文按字符数算，不是字节数', () => {
    // 后端用 []rune，前端若用 .length 就是字节数，两者结论相反：
    //   「密码」  = 2 rune / 6 字节 → rune 判为太短，字节判为够
    //   「密码a」 = 3 rune / 7 字节 → 两种算法都不够
    //   「密码ab」= 4 rune / 8 字节 → 两种算法都够
    expect(passwordTooShort('密码')).toBe(true)
    expect(passwordTooShort('密码a')).toBe(true)
    expect(passwordTooShort('密码ab')).toBe(false)
  })
})

describe('角色标签', () => {
  it('四个角色都能翻译，未知角色原样返回而不是静默变成只读', () => {
    // Fyne 的 roleKeyFromLabel 遇到未知标签会静默落到 viewer（ui/users.go:136-143）。
    // 显示侧不做这件事：一个没见过的角色名应该原样显示，让人知道出事了。
    expect(roleLabel('admin')).toBe('管理员')
    expect(roleLabel('warehouse')).toBe('仓管')
    expect(roleLabel('production')).toBe('生产')
    expect(roleLabel('viewer')).toBe('只读')
    expect(roleLabel('superuser')).toBe('superuser')
  })
})