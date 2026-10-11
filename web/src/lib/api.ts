import { useCallback, useEffect, useState } from 'react'
import type { ModulePermissions } from './permissions'

/**
 * 会话 token 只存在内存里，**不写 localStorage**。
 *
 * 这是刻意的：应用要求「每次打开都先到登录界面」。token 若落盘，启动时就能
 * 静默恢复会话，登录页会被跳过。放内存则进程一启动凭证即为空，登录页必然出现
 * —— 靠结构保证，而不是靠约定。
 *
 * 跨启动保留的是另一份东西：`remember` 那份经 DPAPI 加密的凭证，且即使有它，
 * 也仍需用户点一次「登录」。
 */
let sessionToken: string | null = null

export function getToken(): string | null {
  return sessionToken
}

export function setToken(t: string | null): void {
  sessionToken = t
}

export class ApiError extends Error {
  readonly status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
  /** 401 表示会话失效（过期、被登出、被强制下线），界面应回到登录页 */
  get isUnauthorized(): boolean {
    return this.status === 401
  }
  get isForbidden(): boolean {
    return this.status === 403
  }
}

/**
 * 统一请求入口。
 *
 * 凭据用 `Authorization: Bearer` 而不是 Cookie：阶段 A 的启动 Cookie 由
 * Wails 代理注入、HttpOnly，页面 JS 读不到也不该读；用户会话才走 Bearer。
 *
 * 401 一律清掉本地 token 并交给上层跳登录——包括管理员改角色触发的强制下线，
 * 否则界面会停在半可用状态，用户看到的是「按钮点了没反应」而不是「请重新登录」。
 */
async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const token = getToken()
  const headers = new Headers(init.headers)
  if (token) headers.set('Authorization', `Bearer ${token}`)
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }

  const resp = await fetch(path, { ...init, headers, credentials: 'same-origin' })
  if (!resp.ok) {
    let msg = resp.statusText
    try {
      const body = await resp.json()
      if (body?.error) msg = body.error
    } catch {
      /* 响应不是 JSON，用状态文本 */
    }
    if (resp.status === 401) setToken(null)
    throw new ApiError(resp.status, msg)
  }
  if (resp.status === 204) return undefined as T
  return (await resp.json()) as T
}

export const api = {
  get: <T>(p: string) => request<T>(p),
  post: <T>(p: string, body?: unknown) =>
    request<T>(p, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) }),
  put: <T>(p: string, body?: unknown) =>
    request<T>(p, { method: 'PUT', body: body === undefined ? undefined : JSON.stringify(body) }),
  del: <T>(p: string) => request<T>(p, { method: 'DELETE' }),
}

export interface Me {
  id: number
  username: string
  display_name?: string
  role: string
}

export interface LoginResult {
  token: string
  user: Me
  /** 仅当勾选「记住登录」时返回；是长期凭证，不是会话 token */
  remember_token?: string
}

interface RememberLoad {
  token: string | null
  user_id?: number
  username?: string
}

/**
 * 记住登录。
 *
 * 磁盘上只存 DPAPI 加密的凭证，不存密码（见 internal/api/remember.go 的设计说明）。
 * 这几个端点不要求用户会话——它们在登录之前就要用；安全边界是本机的
 * 启动 token 网关，同机其它程序拿不到 Cookie。
 */
export const remember = {
  /** 用凭证换一个新的普通会话，顺带清掉磁盘上的副本。 */
  async exchange(token: string): Promise<LoginResult> {
    return api.post<LoginResult>('/api/remember/exchange', { token })
  },
  /** 读出本机保存的凭证。 */
  async load(): Promise<RememberLoad> {
    return api.get<RememberLoad>('/api/remember/load')
  },
  /** 让本机把凭证 DPAPI 加密后落盘。 */
  async save(token: string, userId: number): Promise<void> {
    await api.post('/api/remember/save', { token, user: { id: userId } })
  },
  /** 清除磁盘上的凭证。主动退出登录时调用。 */
  async forget(): Promise<void> {
    await api.post('/api/remember/forget')
  },
}

/** 登出：作废服务端会话条目、清除本机凭证，再清本地 token。只清本地是不够的。 */
export async function logout(): Promise<void> {
  try {
    await api.del('/api/session')
  } catch {
    /* 会话可能已失效，本地照样清 */
  } finally {
    await remember.forget().catch(() => undefined)
    setToken(null)
  }
}

export function usePermissions(): {
  modules: ModulePermissions | null
  loading: boolean
  reload: () => void
} {
  const [modules, setModules] = useState<ModulePermissions | null>(null)
  const [loading, setLoading] = useState(true)
  const [nonce, setNonce] = useState(0)

  const reload = useCallback(() => setNonce((n) => n + 1), [])

  useEffect(() => {
    let alive = true
    setLoading(true)
    api
      .get<{ modules: ModulePermissions }>('/api/me/permissions')
      .then((r) => {
        if (alive) setModules(r.modules)
      })
      .catch(() => {
        if (alive) setModules(null)
      })
      .finally(() => {
        if (alive) setLoading(false)
      })
    return () => {
      alive = false
    }
  }, [nonce])

  return { modules, loading, reload }
}
