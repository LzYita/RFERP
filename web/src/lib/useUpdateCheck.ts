import { useCallback, useEffect, useRef, useState } from 'react'
import { updateCheck, type UpdateCheck } from './help'

/**
 * 登录成功后检查一次更新（对齐 Fyne 1.3.0 的时机），有新版就交回给调用方弹窗。
 *
 * 为什么放在"登录态成立"这个点上，而不是启动时：
 * 1.3.0 的检查写在 `launchMain` 里（cmd/desktop/main.go:291-309），也就是**登录之后**；
 * Wails 版曾经放在启动后 3 秒（登录之前），于是用户刚坐下、可能正在录入数据时弹框，
 * 点「是」就立刻重启。时机改回登录后。
 *
 * 为什么是 hook 而不是写进登录回调：登录有两条路——手输账号密码，和"记住登录"
 * 启动时用旧 token 直接恢复（App.tsx 里 `GET /api/me` 那条）。挂在登录态上，
 * 两条路都覆盖到，登录回调里就不用各写一遍。
 *
 * 每次挂载只查一次：查完就标记。反复触发没有意义，而且登录态可能被会话过期清掉再
 * 恢复——那种情况下重新查一次反而更烦人（用户正在做事又弹框）。
 *
 * 返回「有新版时的清单 + 一个收起方法」而不是自己弹窗：弹窗是界面，放在 App 那一层
 * 渲染，这里只管"什么时候有新版"和"用户点了稍后"。原来这条是调
 * `POST /api/update/prompt` 让**服务端**弹原生框（2026-10-17 已删），现在两条入口
 * 共用同一个网页弹窗。
 */
export function useUpdateCheckOnLogin(me: { id: number } | null): {
  pending: UpdateCheck | null
  dismiss: () => void
} {
  const checked = useRef(false)
  const [pending, setPending] = useState<UpdateCheck | null>(null)

  useEffect(() => {
    if (!me || checked.current) return
    checked.current = true
    void updateCheck
      .run()
      .then((r) => {
        // 只有"确实有更高版本"才弹。没配置更新源、已是最新、验签被拒（warning）
        // 这些都不弹窗：前两种是常态，弹窗只会变成噪音；第三种由设置屏的
        // 「查询最新更新」把 warning 显示出来，那里有上下文、也更该由用户主动触发。
        if (r.enabled && r.hasUpdate) setPending(r)
      })
      // 查不到就安静退出。这里绝不能因为一次网络失败就让登录流程出问题。
      .catch(() => undefined)
  }, [me])

  return { pending, dismiss: useCallback(() => setPending(null), []) }
}
