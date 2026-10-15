import { api } from './api'

/**
 * 内置帮助内容（设置屏的三个页签）。
 *
 * 与服务端 internal/api/help.go 的 handleHelp 一一对应：一个请求拿全三份，
 * 因为三个页签总是一起出现，没有哪一份是可选加载的。
 */
export interface HelpSection {
  title?: string
  body: string
}

export interface HelpVersion {
  version: string
  publishedAt?: string
  sections: HelpSection[]
}

export interface HelpPayload {
  /** 进程自报的版本（发布时 ldflags 注入），不是前端写死的常量 */
  currentVersion: string
  /** 内置更新说明里记录的最新版本；断网时这是唯一的「最新」 */
  latestVersion: string
  guide: string
  faq: string
  versions: HelpVersion[]
}

export const helpContent = {
  load: () => api.get<HelpPayload>('/api/help'),
}

/**
 * 在线查询结果的形状，对应 handleCheckUpdate。
 *
 * `enabled:false` 是默认状态（本地部署没配更新源）：此时不该发任何网络请求，
 * 界面应直说「未配置」，而不是报一个看起来像故障的错误。
 */
export interface UpdateCheck {
  enabled: boolean
  currentVersion?: string
  hasUpdate?: boolean
  version?: string
  notes?: string
  size?: number
  publishedAt?: string
  channel?: string
}

export const updateCheck = {
  run: () => api.get<UpdateCheck>('/api/update/check'),
}

/**
 * 弹更新确认框（服务端弹，不是网页弹）。
 *
 * 手动「查询最新更新」查到新版本后调它：弹出来的框与启动时那个**完全同一个**
 * （同一份 Go 代码），用户点「是」就下载、替换 exe 并重启。
 *
 * 为什么不走 download + apply 两个 HTTP 端点由前端自己串：那条链回完响应后
 * 没有人退出旧进程，新进程会等满20 秒单实例锁然后自己退出——用户在界面上点了
 * 安装，程序却没换掉。走服务端弹框就不用碰那个坑。
 *
 * 服务端立即返回 202：对话框要等人点，下载可能几分钟，**不能**把 HTTP 请求挂在
 * 那里等。所以返回 202 只表示"框已经安排上了"，不代表装完了——装完之前程序会
 * 被换掉并重启。
 */
export const updatePrompt = {
  request: () => api.post<{ status: string }>('/api/update/prompt'),
}

/**
 * 更新说明里的发布时间只取日期部分。
 *
 * 首页的 shortTime 会连时分一起显示，那对「某个版本什么时候发的」是噪音；
 * 格式不认识时原样返回，不猜。
 */
export function releaseDate(ts: string | undefined): string {
  if (!ts) return ''
  return /^\d{4}-\d{2}-\d{2}/.test(ts) ? ts.slice(0, 10) : ts
}
