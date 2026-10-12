import { useCallback, useEffect, useState } from 'react'
import { AppShell } from './components/AppShell'
import { LoginScreen } from './screens/LoginScreen'
import { SetupScreen } from './screens/SetupScreen'
import { renderScreen } from './screens/registry'
import { api, getToken, logout, setToken, setup, usePermissions, type Me } from './lib/api'

const VERSION = '1.3.0'

/**
 * 启动态有三态而不是两态。
 *
 * `init` 是必要的：空库时不该先闪一下登录页再切到向导——用户会以为输错密码。
 * 要么先查清「有没有账号」，要么什么都不显示。
 */
type Screen =
  | { kind: 'login' }
  | { kind: 'setup' }
  | { kind: 'shell'; key: string }

export default function App() {
  const [me, setMe] = useState<Me | null>(null)
  const [ready, setReady] = useState(false)
  const [needsSetup, setNeedsSetup] = useState(false)
  const [screen, setScreen] = useState<Screen>({ kind: 'login' })
  const { modules, reload } = usePermissions()

  // 会话 token 只在内存里，进程启动时必然为空 —— 登录页一定会出现。
  // 这里只处理「页面在已登录状态下被重载」的情况（例如 WebView 自身刷新）。
  useEffect(() => {
    if (getToken()) {
      api
        .get<Me>('/api/me')
        .then((u) => {
          setMe(u)
          setScreen({ kind: 'shell', key: 'dashboard' })
        })
        // 会话失效（含被管理员强制下线）时回登录页；顺手查一次是否需要向导，
        // 因为「被删号」和「空库」都可能发生。
        .catch(() => {
          setToken(null)
          return setup.userCount().then((n) => {
            if (n === 0) setNeedsSetup(true)
          }).catch(() => undefined)
        })
        .finally(() => setReady(true))
      return
    }

    // 未登录：先问本机还有没有账号。没有就直接进向导。
    setup
      .userCount()
      .then((n) => {
        if (n === 0) setNeedsSetup(true)
      })
      // 查不到用户数不代表空库——可能是服务没起来。让用户去登录页，
      // 那里的报错比「误报成未初始化」更有帮助。
      .catch(() => undefined)
      .finally(() => setReady(true))
  }, [])

  const onLoggedIn = useCallback((token: string, user: Me) => {
    setToken(token)
    setMe(user)
    setNeedsSetup(false)
    setScreen({ kind: 'shell', key: 'dashboard' })
    reload()
  }, [reload])

  const onSetupDone = useCallback(
    (token: string, user: Me) => {
      // 建好管理员直接进应用：用户刚做完一步就该看到成果，
      // 再让他输一遍自己刚设的密码是多余的。
      setToken(token)
      setMe(user)
      setNeedsSetup(false)
      setScreen({ kind: 'shell', key: 'dashboard' })
      reload()
    },
    [reload],
  )

  const onLogout = useCallback(async () => {
    await logout()
    setMe(null)
    setScreen({ kind: 'login' })
  }, [])

  if (!ready) {
    return <div className="grid h-full place-items-center text-[13px] text-[var(--color-fg-muted)]">正在加载…</div>
  }

  // 向导优先于登录：还没有账号时，登录页是没有意义的。
  if (needsSetup && !me) {
    return <SetupScreen version={VERSION} onDone={onSetupDone} />
  }

  if (screen.kind !== 'shell' || !me) {
    return <LoginScreen onLoggedIn={onLoggedIn} version={VERSION} />
  }

  const activeKey = screen.key

  return (
    <AppShell
      me={me}
      modules={modules}
      active={activeKey}
      version={VERSION}
      onNavigate={(key) => setScreen({ kind: 'shell', key })}
      onLogout={onLogout}
    >
      {renderScreen(activeKey)}
    </AppShell>
  )
}