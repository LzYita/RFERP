import { useCallback, useEffect, useState } from 'react'
import { AppShell } from './components/AppShell'
import { PartsScreen } from './screens/PartsScreen'
import { LoginScreen } from './screens/LoginScreen'
import { PlaceholderScreen } from './screens/PlaceholderScreen'
import { NAV } from './lib/nav'
import { api, getToken, logout, setToken, usePermissions, type Me } from './lib/api'

const VERSION = '1.3.0'

type Screen =
  | { kind: 'login' }
  | { kind: 'shell'; key: string }

export default function App() {
  const [me, setMe] = useState<Me | null>(null)
  const [ready, setReady] = useState(false)
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
        .catch(() => setToken(null))
        .finally(() => setReady(true))
    } else {
      setReady(true)
    }
  }, [])

  const onLoggedIn = useCallback((token: string, user: Me) => {
    setToken(token)
    setMe(user)
    setScreen({ kind: 'shell', key: 'dashboard' })
    reload()
  }, [reload])

  const onLogout = useCallback(async () => {
    await logout()
    setMe(null)
    setScreen({ kind: 'login' })
  }, [])

  if (!ready) {
    return <div className="grid h-full place-items-center text-[13px] text-[var(--color-fg-muted)]">正在加载…</div>
  }

  if (screen.kind === 'login' || !me) {
    return <LoginScreen onLoggedIn={onLoggedIn} />
  }

  const activeKey = screen.key
  const entry = NAV.find((n) => n.key === activeKey)

  return (
    <AppShell
      me={me}
      modules={modules}
      active={activeKey}
      version={VERSION}
      onNavigate={(key) => setScreen({ kind: 'shell', key })}
      onLogout={onLogout}
    >
      {activeKey === 'parts' ? (
        <PartsScreen />
      ) : (
        <PlaceholderScreen title={entry?.label ?? '设置'} />
      )}
    </AppShell>
  )
}
