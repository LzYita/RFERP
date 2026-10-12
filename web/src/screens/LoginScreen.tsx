import { useEffect, useState } from 'react'
import { api, ApiError, remember, type LoginResult } from '../lib/api'

/**
 * 登录页。
 *
 * **每次打开应用都停在这一页**——包括勾了「记住登录」的情况。记住的作用是
 * 免去输入密码，不是跳过登录页：用户每次都明确地点一次「登录」才进入应用。
 *
 * 因此磁盘上**不需要存密码**：记住的是一份 DPAPI 加密的凭证，点「登录」时
 * 用它换一个普通会话。若当初把密码存下来（旧版 Fyne 的做法），同机程序拿到
 * 后密码就永久泄露了，而凭证随时可以作废。
 *
 * 布局按宽度分两档，不是简单地把卡片居中：
 *  - 宽屏（≥1024px）：左墨绿品牌区 + 右表单。窄卡片浮在大画布正中间会显得
 *    没做完，左栏给画面一个锚点，也顺便放掉当前无处安放的版本号。
 *  - 窄屏：只留表单，底色换成主色的浅色调，避免变成一张漂在灰底上的小卡片。
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
  const [rememberMe, setRememberMe] = useState(false)
  const [showPwd, setShowPwd] = useState(false)
  const [saved, setSaved] = useState<{ token: string; username: string } | null>(null)
  /** true = 已记住，点登录即可进入；false = 显示密码框 */
  const [useSaved, setUseSaved] = useState(false)
  const [checking, setChecking] = useState(true)
  const [error, setError] = useState<{ kind: 'credentials' | 'server'; text: string } | null>(null)
  const [busy, setBusy] = useState(false)

  // 启动时问一次本机：有没有记住的凭证
  useEffect(() => {
    let alive = true
    remember
      .load()
      .then((r) => {
        if (!alive || !r.token || !r.username) return
        setSaved({ token: r.token, username: r.username })
        setUsername(r.username)
        setUseSaved(true)
      })
      .catch(() => undefined)
      .finally(() => alive && setChecking(false))
    return () => {
      alive = false
    }
  }, [])

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (busy || checking) return
    setBusy(true)
    setError(null)
    try {
      if (useSaved && saved) {
        // 用记住的凭证换会话：不需要密码
        const r = await remember.exchange(saved.token)
        onLoggedIn(r.token, r.user)
        return
      }
      const r = await api.post<LoginResult>('/api/login', {
        username,
        password,
        // 必须显式带上 remember：后端默认 false，不传就没有 remember_token，
        // 勾选框会看起来生效了但磁盘上什么都没写。
        remember: rememberMe,
      })
      // 先建立会话，再存凭证。
      //
      // /api/remember/save 校验「当前会话的用户」与请求里的 user.id 是否一致，
      // 而 Authorization 头取自 api 模块里的会话 token——在 onLoggedIn 之前
      // 那个 token 还是 null，请求会被 401 掉。
      onLoggedIn(r.token, r.user)
      if (rememberMe && r.remember_token) {
        // 存盘失败不该挡住登录：用户照样进得去，只是下次要输密码。
        // 但不能默默吞掉——否则失败和成功长得一模一样，用户无从判断。
        await remember.save(r.remember_token, r.user.id).catch((e: unknown) => {
          console.warn('记住登录保存失败，下次启动需重新输入密码：', e)
        })
      }
      return
    } catch (err) {
      if (useSaved && saved && err instanceof ApiError && err.isUnauthorized) {
        // 凭证过期/被撤销：退回密码登录，并清掉这份没用的凭证。
        await remember.forget().catch(() => undefined)
        setSaved(null)
        setUseSaved(false)
        setError({ kind: 'credentials', text: '记住的登录已失效，请输入密码' })
        return
      }
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
          宽度按比例给，不设上限——限死会让大屏上剩下大片空白，失衡比不分栏还明显。 */}
      <aside className="hidden w-[42%] shrink-0 flex-col justify-between bg-[var(--color-primary)] p-10 text-white lg:flex xl:w-[38%]">
        <div className="flex items-center gap-2.5 text-[15px] font-semibold">
          <span className="grid h-7 w-7 place-items-center rounded-[6px] bg-white/20 text-[13px]">
            仁
          </span>
          RFERP
        </div>

        <div className="max-w-[30ch]">
          <h1 className="mb-3 text-[26px] leading-snug font-bold">仁风仓库管理系统</h1>
          <p className="text-[13px] leading-relaxed text-white/80">
            零件、BOM、批次与库存追溯，全部数据留在你自己的机器上。
          </p>
        </div>

        <div className="text-[12px] text-white/60">
          <p>版本 {version}</p>
          <p className="mt-1">本机运行 · 数据不离开本机</p>
        </div>
      </aside>

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

          {useSaved && saved ? (
            <>
              <div className="mb-4 rounded-[8px] border border-[var(--color-primary-subtle-border)] bg-[var(--color-primary-subtle)] px-3 py-2.5 text-[12px] leading-relaxed text-[var(--color-fg-muted)]">
                已记住 <b className="text-[var(--color-primary)]">{saved.username}</b>{' '}
                的登录信息，点击下方按钮即可进入。
              </div>
              <button
                type="button"
                onClick={() => {
                  setUseSaved(false)
                  setError(null)
                }}
                className="mb-4 text-[11.5px] text-[var(--color-fg-muted)] underline underline-offset-2 hover:text-[var(--color-primary)]"
              >
                改用密码登录
              </button>
            </>
          ) : (
            <>
              <label className="mb-4 block">
                <span className="mb-1.5 block text-[12px] text-[var(--color-fg-muted)]">用户名</span>
                <input
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  autoFocus={!saved}
                  autoComplete="username"
                  className="h-[30px] w-full rounded-[8px] border border-[var(--color-border)] px-2.5 text-[13px] outline-none focus:border-[var(--color-primary)]"
                />
              </label>

              <label className="mb-3 block">
                <span className="mb-1.5 block text-[12px] text-[var(--color-fg-muted)]">密码</span>
                <div className="relative">
                  <input
                    type={showPwd ? 'text' : 'password'}
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    autoFocus={!!saved}
                    autoComplete="current-password"
                    className="h-[30px] w-full rounded-[8px] border border-[var(--color-border)] px-2.5 pr-9 text-[13px] outline-none focus:border-[var(--color-primary)]"
                  />
                  {/* 显示密码按钮：常驻、可反复切换。之前根本没有，
                      密码只能盲打。图标用线性 SVG 而非 👁/🙈 —— emoji
                      是彩色字形，与这套克制的界面不搭，且跨字体渲染差异大。 */}
                  <button
                    type="button"
                    onClick={() => setShowPwd((v) => !v)}
                    aria-label={showPwd ? '隐藏密码' : '显示密码'}
                    aria-pressed={showPwd}
                    title={showPwd ? '隐藏密码' : '显示密码'}
                    className="absolute top-1/2 right-1 flex h-[22px] w-[26px] -translate-y-1/2 items-center justify-center rounded-[4px] text-[var(--color-fg-subtle)] hover:bg-[var(--color-canvas)] hover:text-[var(--color-primary)]"
                  >
                    <EyeIcon hidden={!showPwd} />
                  </button>
                </div>
              </label>

              <label className="mb-5 flex items-center gap-1.5 text-[11.5px] text-[var(--color-fg-muted)]">
                <input
                  type="checkbox"
                  checked={rememberMe}
                  onChange={(e) => setRememberMe(e.target.checked)}
                />
                记住登录（下次打开只需点登录）
              </label>
            </>
          )}

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
            disabled={busy || checking}
            className="h-[32px] w-full rounded-[8px] bg-[var(--color-primary)] text-[13px] font-medium text-white disabled:opacity-60"
          >
            {checking ? '读取中…' : busy ? '登录中…' : '登录'}
          </button>
        </form>
      </main>
    </div>
  )
}
/**
 * 眼睛 / 闭眼图标，16px 线性描边，取 currentColor 跟随 hover 态。
 *
 * **图标表示密码的当前状态**，不是点击后的结果：
 *   密码不可见（显示点）→ 闭眼   hidden = true
 *   密码可见 （显示明文）→ 睁眼   hidden = false
 *
 * 写成 hidden={showPwd} 会把它反过来——曾经这么错过一次：
 * 点「显示密码」后图标变成闭眼，像是"现在被遮住了"，与实际情况相反。
 * 所以调用方传的是 `!showPwd`，不要直接传 showPwd。
 *
 * 无障碍标签仍描述**动作**（点了会发生什么），两者分工不同：
 * 图标给视觉用户一个即时状态，label 给读屏用户一个可预期的操作。
 */
function EyeIcon({ hidden }: { hidden: boolean }) {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {hidden ? (
        <>
          <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94" />
          <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" />
          <path d="M14.12 14.12a3 3 0 1 1-4.24-4.24" />
          <line x1="2" y1="2" x2="22" y2="22" />
        </>
      ) : (
        <>
          <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
          <circle cx="12" cy="12" r="3" />
        </>
      )}
    </svg>
  )
}