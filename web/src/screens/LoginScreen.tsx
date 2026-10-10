import { useState } from 'react'
import { api, ApiError, type LoginResult } from '../lib/api'

/**
 * 登录页。
 *
 * 错误分两类呈现，因为处理方式完全不同：
 *  - 凭据错误（401）：留在本页，让用户改用户名或密码
 *  - 其余（5xx / 网络）：提示后端不可用，而不是让用户反复试密码
 */
export function LoginScreen({ onLoggedIn }: { onLoggedIn: (t: string, m: LoginResult['user']) => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<{ kind: 'credentials' | 'server'; text: string } | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      const r = await api.post<LoginResult>('/api/login', { username, password })
      onLoggedIn(r.token, r.user)
    } catch (err) {
      if (err instanceof ApiError && err.isUnauthorized) {
        setError({ kind: 'credentials', text: '用户名或密码不正确' })
      } else if (err instanceof ApiError && err.isForbidden) {
        setError({ kind: 'credentials', text: '账号已停用，请联系管理员' })
      } else {
        setError({ kind: 'server', text: `无法连接服务：${(err as Error).message}` })
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="grid h-full place-items-center bg-[var(--color-canvas)]">
      <form
        onSubmit={submit}
        className="w-[340px] rounded-[8px] border border-[var(--color-border)] bg-[var(--color-surface)] p-7"
      >
        <div className="mb-1 flex items-center gap-2 text-[15px] font-bold">
          <span className="h-6 w-6 rounded-[6px] bg-[var(--color-primary)]" />
          RFERP 仁风仓库管理系统
        </div>
        <div className="mb-6 text-[12px] text-[var(--color-fg-muted)]">请登录以继续</div>

        <label className="mb-4 block">
          <span className="mb-1.5 block text-[12px] text-[var(--color-fg-muted)]">用户名</span>
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoFocus
            autoComplete="username"
            className="h-[30px] w-full rounded-[8px] border border-[var(--color-border)] px-2.5 text-[13px] outline-none focus:border-[var(--color-primary)]"
          />
        </label>

        <label className="mb-5 block">
          <span className="mb-1.5 block text-[12px] text-[var(--color-fg-muted)]">密码</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            className="h-[30px] w-full rounded-[8px] border border-[var(--color-border)] px-2.5 text-[13px] outline-none focus:border-[var(--color-primary)]"
          />
        </label>

        {error && (
          <div
            role="alert"
            className={[
              'mb-4 rounded-[8px] border px-3 py-2 text-[12px]',
              error.kind === 'credentials'
                ? 'border-[var(--color-danger)]/30 bg-[var(--color-danger-bg)] text-[var(--color-danger)]'
                : 'border-[var(--color-warn)]/30 bg-[var(--color-warn-bg)] text-[var(--color-warn)]',
            ].join(' ')}
          >
            {error.text}
          </div>
        )}

        <button
          type="submit"
          disabled={busy}
          className="h-[32px] w-full rounded-[8px] bg-[var(--color-primary)] text-[13px] font-medium text-white disabled:opacity-60"
        >
          {busy ? '登录中…' : '登录'}
        </button>
      </form>
    </div>
  )
}
