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

/**
 * 破坏性操作的三分类结局。
 *
 * 与 internal/api/destructive.go 的 OpState* 一一对应。缺省是 rolled_back
 * （服务端对普通错误的语义就是"已确认回滚"），所以拿不到 state 时按它处理是安全的。
 *
 * 三种情况的正确处置完全相反，所以界面**必须**按它分支，不能只显示文案：
 *   rolled_back → 补料/换文件后可以重试
 *   unknown     → 先核对数据，绝不能重试（§Round 5 要求黄色 + 不渲染重试按钮）
 *   applied     → 数据已经变了，重试等于做两遍
 */
export type OperationState = 'rolled_back' | 'unknown' | 'applied'

export class ApiError extends Error {
  readonly status: number
  /** 仅破坏性端点的错误带这个字段；其它错误为 undefined */
  readonly state?: OperationState
  constructor(status: number, message: string, state?: OperationState) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.state = state
  }
  /** 401 表示会话失效（过期、被登出、被强制下线），界面应回到登录页 */
  get isUnauthorized(): boolean {
    return this.status === 401
  }
  get isForbidden(): boolean {
    return this.status === 403
  }
  /**
   * 结局是否已确定到可以重试。
   *
   * 只有 rolled_back 可以重试。unknown 与 applied 都**不能**：前者可能已经部分
   * 应用，后者已经应用过了。
   */
  get isRetryableOutcome(): boolean {
    return this.state === 'rolled_back'
  }
}

/**
 * 401 广播。
 *
 * 之前只有 `setToken(null)`，而 App 决定「是否回到登录页」看的是 `me`，
 * 于是会话过期后界面停在旧数据上：看起来一切正常，点什么都是 401，
 * 用户既不知道为什么，也不知道该重新登录。
 *
 * `setToken` 只负责清凭据、抛异常；**要不要跳登录页**必须由持有 `me` 的
 * App 来决定，所以这里用订阅把这件事通知出去，而不是在 api 层直接改 UI。
 *
 * 只在「本来带着 token」的情况下广播：登录接口自己返回 401（密码错）
 * 不该触发——那会让登录页自己把用户踢走。
 */
type UnauthorizedListener = () => void
const unauthorizedListeners = new Set<UnauthorizedListener>()

export function onUnauthorized(fn: UnauthorizedListener): () => void {
  unauthorizedListeners.add(fn)
  return () => {
    unauthorizedListeners.delete(fn)
  }
}

function broadcastUnauthorized() {
  for (const fn of unauthorizedListeners) {
    try {
      fn()
    } catch {
      // 一个订阅者出错不该影响其余订阅者，也不该让 401 本身报错
    }
  }
}

/**
 * 出错时把服务端 JSON 里的 error / state 读出来。
 *
 * state 是破坏性操作的三分类，缺了它界面就只能显示一句话、分不出该不该重试
 * （见 OperationState）。
 */
async function throwApiError(resp: Response, token: string | null): Promise<never> {
  let msg = resp.statusText
  let state: OperationState | undefined
  try {
    const body = await resp.json()
    if (body?.error) msg = body.error
    if (body?.state) state = body.state as OperationState
  } catch {
    /* 响应不是 JSON，用状态文本 */
  }
  if (resp.status === 401) {
    setToken(null)
    // 只在携带凭据时广播：登录本身失败不是「会话过期」
    if (token) broadcastUnauthorized()
  }
  throw new ApiError(resp.status, msg, state)
}

/**
 * 统一请求入口。
 *
 * 凭据用 `Authorization: Bearer` 而不是 Cookie：阶段 A 的启动 Cookie 由
 * Wails 代理注入、HttpOnly，页面 JS 读不到也不该读；用户会话才走 Bearer。
 */
async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const token = getToken()
  const headers = new Headers(init.headers)
  if (token) headers.set('Authorization', `Bearer ${token}`)
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }

  const resp = await fetch(path, { ...init, headers, credentials: 'same-origin' })
  if (!resp.ok) await throwApiError(resp, token)
  if (resp.status === 204) return undefined as T
  return (await resp.json()) as T
}

/** 从 Content-Disposition 里取文件名。取不到就退回默认名。 */
function filenameFromDisposition(header: string | null, fallback: string): string {
  if (!header) return fallback
  // 优先 RFC 5987 的 filename*=UTF-8''…（中文文件名必须这样编码，
  // 否则浏览器会按 ISO-8859-1 解出乱码），再退回普通 filename=
  const star = /filename\*=UTF-8''([^;]+)/i.exec(header)
  if (star) {
    try {
      return decodeURIComponent(star[1].trim())
    } catch {
      /* 编码坏了就用下面的 fallback */
    }
  }
  const plain = /filename="?([^";]+)"?/i.exec(header)
  return plain ? plain[1] : fallback
}

/**
 * 下载二进制响应（备份 / 导出）。
 *
 * 不能复用 request：它结尾固定 `resp.json()`，而备份是 .sql 文件流。这里把内容
 * 与文件名一起交给调用方，由界面触发浏览器下载——服务端只管把字节送出来。
 */
async function requestDownload(
  path: string,
  init: RequestInit = {},
  fallbackName = 'download.bin',
): Promise<{ blob: Blob; filename: string }> {
  const token = getToken()
  const headers = new Headers(init.headers)
  if (token) headers.set('Authorization', `Bearer ${token}`)
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  const resp = await fetch(path, { ...init, headers, credentials: 'same-origin' })
  if (!resp.ok) await throwApiError(resp, token)
  return {
    blob: await resp.blob(),
    filename: filenameFromDisposition(resp.headers.get('Content-Disposition'), fallbackName),
  }
}

/** 把 Blob 交给浏览器下载。用完必须 revoke，否则 blob 会一直占着内存。 */
export function saveBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  // 不立刻 revoke：部分 WebView 在 click 后仍异步读取该 URL
  setTimeout(() => URL.revokeObjectURL(url), 30_000)
}

export const api = {
  get: <T>(p: string) => request<T>(p),
  post: <T>(p: string, body?: unknown) =>
    request<T>(p, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) }),
  put: <T>(p: string, body?: unknown) =>
    request<T>(p, { method: 'PUT', body: body === undefined ? undefined : JSON.stringify(body) }),
  del: <T>(p: string) => request<T>(p, { method: 'DELETE' }),
  download: (p: string, body?: unknown, fallbackName?: string) =>
    requestDownload(
      p,
      { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) },
      fallbackName,
    ),
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
  /**
   * 是否本机形态（决定破坏性操作是否可用）。
   *
   * 缺省 false 而不是"未知"：拿不到就当作不可用，方向是安全的——
   * 顶多让按钮置灰，不会让人在远程服务器上一键清库。
   */
  hostOperations: boolean
  loading: boolean
  reload: () => void
} {
  const [modules, setModules] = useState<ModulePermissions | null>(null)
  const [hostOperations, setHostOperations] = useState(false)
  const [loading, setLoading] = useState(true)
  const [nonce, setNonce] = useState(0)

  const reload = useCallback(() => setNonce((n) => n + 1), [])

  useEffect(() => {
    let alive = true
    setLoading(true)
    api
      .get<{ modules: ModulePermissions; hostOperations?: boolean }>('/api/me/permissions')
      .then((r) => {
        if (!alive) return
        setModules(r.modules)
        setHostOperations(r.hostOperations === true)
      })
      .catch(() => {
        if (!alive) return
        setModules(null)
        setHostOperations(false)
      })
      .finally(() => {
        if (alive) setLoading(false)
      })
    return () => {
      alive = false
    }
  }, [nonce])

  return { modules, hostOperations, loading, reload }
}

/**
 * 首启向导用到的接口。
 *
 * 单独成组，因为它们与登录流程无关：只有「一个用户都没有」的空库才会被调用，
 * 且用户数与建管理员都是**未登录**端点——此刻还没有会话可带。
 */
export const setup = {
/**
   * 本机用户数。0 表示从未初始化，登录页应改显示建管理员向导。
   *
   * 响应是 `{count: N}` 而不是裸数字——注意解包，别把整个对象当数字用。
   */
  async userCount(): Promise<number> {
    const r = await request<{ count: number }>('/api/users/count')
    return r.count
  },
  /**
   * 创建第一个管理员。
   *
   * 仅在用户数为 0 时可用，服务端会再查一次并对重复调用返回 409——
   * 前端的检查只是省一次往返，真正的闸门在服务端。
   */
  async createInitialAdmin(
    username: string,
    password: string,
    displayName: string,
  ): Promise<Me> {
    return request<Me>('/api/bootstrap-admin', {
      method: 'POST',
      body: JSON.stringify({
        username,
        password,
        display_name: displayName,
      }),
    })
  },
}