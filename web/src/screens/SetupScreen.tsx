import { useState } from 'react'
import { ApiError, api, setup, type LoginResult, type Me } from '../lib/api'
import { EyeIcon } from '../components/form'

/**
 * 首启向导：创建一个管理员账号。
 *
 * 只在「本机一个用户都没有」时出现。空库直接落到登录页是没用的——用户既没有
 * 账号可输，也不知道该先做什么，只会以为软件坏了。正确流程是：
 *
 *   启动 → GET /api/users/count
 *          ├─ 0   → 本屏（创建第一个管理员）
 *          └─ > 0 → 登录页
 *
 * 字段与校验对齐 Fyne 版 internal/ui/login.go 的 showCreateAdmin：用户名、
 * 显示名称（选填）、密码、确认密码。密码强度由服务端 auth.ValidatePassword
 * 把关（至少 4 位），前端只做**即时提示**——不重复实现强度规则，
 * 免得两边规则漂移。
 */
export function SetupScreen({
  version = '',
  onDone,
}: {
  version?: string
  /** 建好管理员并登录后回调（token 必须一并交上来，见下方 submit 里的说明）。 */
  onDone: (token: string, user: Me) => void
}) {
  const [username, setUsername] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [showPwd, setShowPwd] = useState(false)
  // 确认密码独立一个开关：两个框共用同一个状态的话，点确认框的眼睛会把
  // 上面那个也一起显出来，看起来像是"密码被显示了两次"。
  const [showPwd2, setShowPwd2] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  // 确认密码不一致是最常见的低级失误，就地提示，不要等服务端拒绝。
  const mismatch = confirm.length > 0 && confirm !== password

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (busy) return
    setError(null)

    if (!username.trim()) {
      setError('请填写用户名')
      return
    }
    if (!password) {
      setError('请填写密码')
      return
    }
    if (mismatch) {
      setError('两次输入的密码不一致')
      return
    }

    setBusy(true)
    try {
      await setup.createInitialAdmin(username.trim(), password, displayName.trim())

      // **必须再登录一次**才能拿到会话。
      //
      // /api/bootstrap-admin 只建号、不签发 token，而后面每一个接口都要 Bearer。
      // 少了这一步，界面会渲染出外壳、侧栏空白、所有请求 401——看起来像成功，
      // 实际上一步也走不了。
      //
      // 这里不选「让 bootstrap-admin 直接返回 token」：那会改动既有端点契约，
      // 而 Fyne 版也在用它。为一次性首启多发一个请求更划算。
      const r = await api.post<LoginResult>('/api/login', {
        username: username.trim(),
        password,
      })
      onDone(r.token, r.user)
    } catch (err) {
      setBusy(false)
      if (err instanceof ApiError && err.status === 409) {
        // 已被别的窗口抢先建好了。这不是错误，是并发下的正常结果。
        setError('该数据库已初始化，请返回登录页登录')
      } else {
        setError(err instanceof Error ? err.message : '创建失败，请稍后重试')
      }
    }
  }

  return (
    <div className="flex min-h-full flex-col bg-[var(--color-primary-subtle)]">
      <div className="flex flex-1 items-center justify-center p-5">
        <div className="w-full max-w-[420px] rounded-[12px] border border-[var(--color-primary-subtle-border)] bg-[var(--color-surface)] p-6 shadow-sm">
          <div className="mb-1 text-[17px] font-bold">创建第一个管理员</div>
          <div className="mb-5 text-[12px] leading-relaxed text-[var(--color-fg-muted)]">
            这台机器上还没有任何账号。创建完成后，用这个账号登录。
            <br />
            密码请至少 4 位，并妥善保管——它是这个数据库的唯一入口。
          </div>

          <form onSubmit={submit} className="flex flex-col gap-3">
            <Field label="用户名" required>
              <input
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoFocus
                autoComplete="username"
                className="h-[30px] w-full rounded-[8px] border border-[var(--color-border)] px-2.5 text-[13px] outline-none focus:border-[var(--color-primary)]"
              />
            </Field>

            <Field label="显示名称（选填）">
              <input
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                autoComplete="name"
                placeholder={username.trim() || '例如：仓库主管'}
                className="h-[30px] w-full rounded-[8px] border border-[var(--color-border)] px-2.5 text-[13px] outline-none focus:border-[var(--color-primary)]"
              />
            </Field>

            <Field label="密码" required>
              <div className="relative">
                <input
                  type={showPwd ? 'text' : 'password'}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="new-password"
                  className="h-[30px] w-full rounded-[8px] border border-[var(--color-border)] px-2.5 pr-9 text-[13px] outline-none focus:border-[var(--color-primary)]"
                />
                <button
                  type="button"
                  onClick={() => setShowPwd((v) => !v)}
                  aria-label={showPwd ? '隐藏密码' : '显示密码'}
                  title={showPwd ? '隐藏密码' : '显示密码'}
                  className="absolute top-1/2 right-1 flex h-[22px] w-[26px] -translate-y-1/2 items-center justify-center rounded-[4px] text-[var(--color-fg-subtle)] hover:bg-[var(--color-canvas)] hover:text-[var(--color-primary)]"
                >
                  <EyeIcon hidden={!showPwd} />
                </button>
              </div>
              {password.length > 0 && password.length < 4 && (
                <span className="mt-1 block text-[11px] text-[var(--color-warn)]">密码至少 4 位</span>
              )}
            </Field>

            <Field label="确认密码" required>
              <div className="relative">
                <input
                  type={showPwd2 ? 'text' : 'password'}
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                  autoComplete="new-password"
                  className={[
                    'h-[30px] w-full rounded-[8px] border px-2.5 pr-9 text-[13px] outline-none focus:border-[var(--color-primary)]',
                    mismatch ? 'border-[var(--color-danger)]' : 'border-[var(--color-border)]',
                  ].join(' ')}
                />
                <button
                  type="button"
                  onClick={() => setShowPwd2((v) => !v)}
                  aria-label={showPwd2 ? '隐藏确认密码' : '显示确认密码'}
                  title={showPwd2 ? '隐藏确认密码' : '显示确认密码'}
                  className="absolute top-1/2 right-1 flex h-[22px] w-[26px] -translate-y-1/2 items-center justify-center rounded-[4px] text-[var(--color-fg-subtle)] hover:bg-[var(--color-canvas)] hover:text-[var(--color-primary)]"
                >
                  <EyeIcon hidden={!showPwd2} />
                </button>
              </div>
              {mismatch && <span className="mt-1 block text-[11px] text-[var(--color-danger)]">两次输入的密码不一致</span>}
            </Field>

            {error && (
              <div
                role="alert"
                className="rounded-[6px] border border-[var(--color-danger)]/30 bg-[var(--color-danger-bg)] px-2.5 py-2 text-[12px] text-[var(--color-danger)]"
              >
                {error}
              </div>
            )}

            <button
              type="submit"
              disabled={busy}
              className="mt-1 inline-flex h-[32px] items-center justify-center rounded-[8px] border border-[var(--color-primary)] bg-[var(--color-primary)] text-[13px] font-semibold text-white hover:bg-[var(--color-primary-hover)] disabled:opacity-50"
            >
              {busy ? '正在创建…' : '创建管理员并进入'}
            </button>
          </form>
        </div>
      </div>
      <div className="pb-3 text-center text-[11px] text-[var(--color-fg-subtle)]">
        仁风仓库管理系统 · v{version} · 数据全部保存在本机
      </div>
    </div>
  )
}

function Field({
  label,
  required,
  children,
}: {
  label: string
  required?: boolean
  children: React.ReactNode
}) {
  return (
    <label className="flex flex-col gap-[3px]">
      <span className="text-[12px] text-[var(--color-fg-muted)]">
        {label}
        {required && <span className="text-[var(--color-danger)]"> *</span>}
      </span>
      {children}
    </label>
  )
}
