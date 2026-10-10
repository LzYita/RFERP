import { useCallback, useEffect, useState } from 'react'
import type { ModulePermissions } from './permissions'

const TOKEN_KEY = 'rferp.token'

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(t: string | null): void {
  if (t === null) localStorage.removeItem(TOKEN_KEY)
  else localStorage.setItem(TOKEN_KEY, t)
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
}

/** 登出：作废服务端会话条目，再清本地 token。只清本地是不够的。 */
export async function logout(): Promise<void> {
  try {
    await api.del('/api/session')
  } catch {
    /* 会话可能已失效，本地照样清 */
  } finally {
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
