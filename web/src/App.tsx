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
import { useUpdateCheckOnLogin } from './lib/useUpdateCheck'
import { UpdatePromptModal } from './screens/updatePromptModal'

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
  // 版本号以后端为准（ldflags 注入的 main.version）。此前这里是写死的
  // '1.3.0'，发 2.0.0 时登录页与右上角仍显示旧版本。
  const [version, setVersion] = useState('')
  const [versionReady, setVersionReady] = useState(false)

  // 向后端报到「界面挂载好了」。
  //
  // 程序更新后能不能回退，判据就是这一条：窗口开着、进程活着都不算——
  // 前端资源坏掉时正是那样运行，用户只看到一片空白。这个信号收不到，
  // 后端就永远不确认这一版健康，回退副本也会一直留着，下次启动会问用户
  // 要不要回退（见 internal/update/pending.go）。
  //
  // **要重试**：只发一次的话，一次瞬时失败（代理抖动、后端刚好在关）就会被
  // 当成"界面没起来"，让一个完全正常的新版本在下次启动时被问要不要回退。
  // 三次足够，间隔拉长；仍然失败就走"保守保留"那条路，不会删用户的文件。
  useEffect(() => {
    api
      .get<{ appVersion: string }>('/api/v1/serverinfo')
      .then((info) => setVersion(info.appVersion))
      .catch(() => undefined)
      .finally(() => setVersionReady(true))
  }, [])

  useEffect(() => {
    let alive = true
    const report = (attempt: number) => {
      api.post('/api/startup/ui-ready').catch(() => {
        if (!alive || attempt >= 3) return
        setTimeout(() => {
          if (alive) report(attempt + 1)
        }, attempt === 1 ? 1000 : 3000)
      })
    }
    report(1)
    return () => {
      alive = false
    }
  }, [])

  // 登录态成立后查一次更新（手动登录与「记住登录」自动恢复两条路都覆盖到）——
  // 时机与 Fyne 1.3.0 一致，都是登录之后。有新版时在这里弹窗。
  const { pending: pendingUpdate, dismiss: dismissUpdate } = useUpdateCheckOnLogin(me)

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

  const onLoggedIn = useCallback(
    (token: string, user: Me) => {
      setToken(token)
      setMe(user)
      setNeedsSetup(false)
      setSessionNotice('')
      setScreen({ kind: 'shell', key: 'dashboard' })
      reload()
    },
    [reload],
  )

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

    if (!ready || !versionReady) {
    return <div className="grid h-full place-items-center text-[13px] text-[var(--color-fg-muted)]">正在加载…</div>
  }

  // 向导优先于登录：还没有账号时，登录页是没有意义的。
  if (needsSetup && !me) {
    return <SetupScreen version={version} onDone={onSetupDone} />
  }

  if (screen.kind !== 'shell' || !me) {
    return <LoginScreen onLoggedIn={onLoggedIn} version={version} notice={sessionNotice} />
  }

  const activeKey = screen.key

  // 登录后的更新提示：放在壳**外面**。它与「登录已失效」那个弹窗不同——那一个要在
  // 用户原来的界面上就地弹出（用户正在录入数据，不能把他踢走），而更新提示是一次
  // 打断，遮住界面正合适。壳外渲染也保证它在设置屏等任何页面之上都算数。
  const updateModal = pendingUpdate ? (
    <UpdatePromptModal check={pendingUpdate} onClose={dismissUpdate} />
  ) : null

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
    <>
      <AppShell
        me={me}
        modules={modules}
        active={activeKey}
        version={version}
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

      {/* 更新提示在壳之外：它是打断而不是就地提示，遮住界面正合适，
          也不会被壳的布局约束到。 */}
      {updateModal}
    </>
  )
}