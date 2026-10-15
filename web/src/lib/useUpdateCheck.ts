import { useEffect, useRef } from 'react'
import { updatePrompt } from './help'

/**
 * 登录成功后触发一次更新检查（对齐 Fyne 1.3.0）。
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
 * 结果一律忽略：服务端查不到新版、未配置更新源、或已有流程在跑都安静退出。这里
 * 绝不能因为一次查不到更新就让登录流程出问题或弹错误。
 */
export function useUpdateCheckOnLogin(me: { id: number } | null): void {
  const checked = useRef(false)

  useEffect(() => {
    if (!me || checked.current) return
    checked.current = true
    void updatePrompt.request().catch(() => undefined)
  }, [me])
}