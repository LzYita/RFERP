import { Fragment, useCallback, useEffect, useState } from 'react'
import { AppShell } from './components/AppShell'
import { ConfirmDialog } from './components/form'
import { LoginScreen } from './screens/LoginScreen'
import { SetupScreen } from './screens/SetupScreen'
import { renderScreen } from './screens/registry'
import {
  api,
  getToken,
  logout,
  onUnauthorized,
  setToken,
  setup,
  usePermissions,
  type Me,
} from './lib/api'

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
  // 会话在别处失效时给一句说明；否则用户只看到自己被弹回登录页，
  // 不知道是超时了、被踢了，还是程序出错了。
  const [sessionNotice, setSessionNotice] = useState('')
  // 会话已失效但用户还没确认回登录：弹窗期间保留 me，让界面停在原处
  const [sessionExpired, setSessionExpired] = useState(false)
  // 每次 +1 就让当前屏重挂载，实现「刷新」
  const [refreshToken, setRefreshToken] = useState(0)
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
    setSessionNotice('')
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

  // 会话在别处失效（空闲超时、绝对上限到了、管理员改角色强制下线）时，
  // api 层只清 token 并广播；界面归谁负责、要不要提示，由这里决定。
  //
  // 这里**不立刻 setMe(null)** —— 那样会直接跳登录页，用户还没反应过来，
  // 正在填的表单就消失了。改为原地弹窗：屏保/离开时回来看到的是原界面
  // 加一个说明，点确认才回登录。会话已经没了，所以弹窗不可取消。
  //
  // 弹窗期间 me 仍在，于是壳照常渲染。遮罩挡住交互，底下的旧数据既点不到
  // 也看不真切——不是把它藏起来，是不再假装它还能用。
  useEffect(
    () =>
      onUnauthorized(() => {
        setSessionExpired(true)
      }),
    [],
  )

  const goToLoginAfterExpiry = useCallback(() => {
    setSessionExpired(false)
    setMe(null)
    setScreen({ kind: 'login' })
    setSessionNotice('登录状态已失效，请重新登录')
  }, [])

  if (!ready) {
    return <div className="grid h-full place-items-center text-[13px] text-[var(--color-fg-muted)]">正在加载…</div>
  }

  // 向导优先于登录：还没有账号时，登录页是没有意义的。
  if (needsSetup && !me) {
    return <SetupScreen version={VERSION} onDone={onSetupDone} />
  }

  if (screen.kind !== 'shell' || !me) {
    return <LoginScreen onLoggedIn={onLoggedIn} version={VERSION} notice={sessionNotice} />
  }

  const activeKey = screen.key

  // 「刷新」用改 key 强制重挂载当前屏，而不是逐屏暴露一个 reload()。
  // 九个屏都在 mount 时自己取数，所以重挂载就等于重新取数——顺带把筛选、
  // 页码、勾选一并复位，这正是刷新该做的事。要逐屏加 reload 回调，就得在
  // 每个屏里多维护一份与自身逻辑重复的状态出口。
  //
  // 用 Fragment 而不是 cloneElement：renderScreen 的返回类型是 ReactNode，
  // 里面可能有非元素节点，cloneElement 对那种输入没有重载。
  const screenNode = (
    <Fragment key={refreshToken}>{renderScreen(activeKey, { meId: me.id })}</Fragment>
  )

  return (
    <AppShell
      me={me}
      modules={modules}
      active={activeKey}
      version={VERSION}
      onNavigate={(key) => setScreen({ kind: 'shell', key })}
      onLogout={onLogout}
      onRefresh={() => setRefreshToken((n) => n + 1)}
    >
      {screenNode}

      {/* 会话失效：原地弹窗，确认后才回登录。
          放在壳内部而不是壳外——壳外就意味着整棵树已经换成登录页了，
          「在原界面弹窗」的前提是原界面还在。 */}
      {sessionExpired ? (
        <ConfirmDialog
          title="登录状态已失效"
          confirmLabel="重新登录"
          message={
            <>
              登录已超时（长时间未操作，或已达单次登录上限）。
              <div className="mt-1.5 text-[11px] text-[var(--color-fg-muted)]">
                为保护数据，需要重新登录后才能继续。未保存的编辑内容会丢失。
              </div>
            </>
          }
          onConfirm={goToLoginAfterExpiry}
        />
      ) : null}
    </AppShell>
  )
}