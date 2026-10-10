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

  // 启动时用已存的 token 换一次身份；失败即视为未登录。
  useEffect(() => {
    const t = getToken()
    if (!t) {
      setReady(true)
      return
    }
    api
      .get<Me>('/api/me')
      .then((u) => {
        setMe(u)
        setScreen({ kind: 'shell', key: 'dashboard' })
      })
      .catch(() => setToken(null))
      .finally(() => setReady(true))
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
