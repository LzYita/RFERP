import { useState } from 'react'
import { api, ApiError, type LoginResult } from '../lib/api'

/**
 * 登录页。
 *
 * 布局按宽度分两档，不是简单地把卡片居中：
 *  - 宽屏（≥1024px）：左墨绿品牌区 + 右表单。窄卡片浮在大画布正中间会显得
 *    没做完，左栏给画面一个锚点，也顺便放掉当前无处安放的版本号。
 *  - 窄屏：只留表单，底色换成主色的浅色调，避免变成一张漂在灰底上的小卡片。
 *
 * 错误分两类呈现，因为处理方式完全不同：
 *  - 凭据错误（401）：留在本页，让用户改用户名或密码
 *  - 其余（5xx / 网络）：提示后端不可用，而不是让用户反复试密码
 */
export function LoginScreen({
  onLoggedIn,
  version = '1.3.0',
}: {
  onLoggedIn: (t: string, m: LoginResult['user']) => void
  version?: string
}) {
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
    <div className="flex h-full w-full items-stretch overflow-hidden">
      {/* 品牌区：只在宽屏出现。它同时承担「窗口很宽时不至于空旷」的作用。
          宽度按比例给，不设上限——之前限死 560px，在 1920 宽的屏幕上会
          留下 1360px 的空白区，失衡比不分栏还明显。 */}
      <aside className="hidden w-[42%] shrink-0 flex-col justify-between bg-[var(--color-primary)] p-10 text-white lg:flex xl:w-[38%]">
        <div className="flex items-center gap-2.5 text-[15px] font-semibold">
          <span className="grid h-7 w-7 place-items-center rounded-[6px] bg-white/20 text-[13px]">
            仁
          </span>
          RFERP
        </div>

        <div className="max-w-[30ch]">
          <h1 className="mb-3 text-[26px] leading-snug font-bold">
            仁风仓库管理系统
          </h1>
          <p className="text-[13px] leading-relaxed text-white/80">
            零件、BOM、批次与库存追溯，全部数据留在你自己的机器上。
          </p>
        </div>

        <div className="text-[12px] text-white/60">
          <p>版本 {version}</p>
          <p className="mt-1">本机运行 · 数据不离开本机</p>
        </div>
      </aside>

      {/* 表单区 */}
      <main className="flex min-w-0 flex-1 items-center justify-center bg-[var(--color-canvas)] p-6">
        <form
          onSubmit={submit}
          className="w-full max-w-[340px] rounded-[8px] border border-[var(--color-border)] bg-[var(--color-surface)] p-7 shadow-sm"
        >
          {/* 窄屏没有左栏，标题得在这里出现 */}
          <div className="mb-6 lg:hidden">
            <div className="mb-1 flex items-center gap-2 text-[15px] font-bold">
              <span className="grid h-6 w-6 place-items-center rounded-[6px] bg-[var(--color-primary)] text-[11px] text-white">
                仁
              </span>
              RFERP 仁风仓库
            </div>
            <div className="text-[12px] text-[var(--color-fg-muted)]">
              请登录以继续 · v{version}
            </div>
          </div>

          <div className="mb-4 hidden text-[15px] font-bold lg:block">请登录以继续</div>

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
      </main>
    </div>
  )
}