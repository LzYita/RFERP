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
  /**
   * 有源"验签被拒 / 清单不可用"时的说明；没有异常时不出现。
   *
   * 为什么必须有这个字段：更新检查会先试配置源、再试内置回退源。配置源被生产公钥拒
   * （例如发版时签错了密钥）而回退源显示没有新版时，净结果就是"没有更新"——
   * 若不把它带出来，界面只会说"已是最新版本"，**发版事故就完全不可见**
   * （Round 8 先决项⑤）。
   */
  warning?: string
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
/**
 * 下载更新包。
 *
 * `async: true` 时服务端立刻返回一个 job id，下载在后台跑，前端用 updateProgress
 * 轮询进度——界面里因此能显示"正在下载 x%"，而不是一个转圈图标。
 * 不带 async 时是同步模式（下载完才响应），给脚本用。
 *
 * 下载成功才会在服务端登记"待应用文件"；失败不会留下可 apply 的东西。
 */
export const updateDownload = {
  run: (opts?: { async?: boolean }) =>
    api.post<{ jobId: string; version: string; total: number }>(
      `/api/update/download${opts?.async ? '?async=1' : ''}`,
    ),
}

export interface UpdateProgress {
  jobId: string
  version: string
  done: number
  total: number
  state: 'running' | 'done' | 'error'
  path?: string
  error?: string
}

export const updateProgress = {
  run: (jobId: string) =>
    api.get<UpdateProgress>(`/api/update/progress?job=${encodeURIComponent(jobId)}`),
}

/**
 * 应用已下载的更新：替换 exe、重启。
 *
 * 必须带 `confirm: true`——它是"下载"到"动手"之间给用户看的闸门。
 * 服务端只认自己记下的那个"刚校验过的文件"，请求体里的 path 一律忽略。
 *
 * 成功后服务端会退出旧进程让新版本接管；调用方看到的是连接断开，那是预期的。
 */
export const updateApply = {
  run: (opts: { confirm: boolean }) =>
    api.post<{ status: string }>('/api/update/apply', { confirm: opts.confirm }),
}

/**
 * 弹更新确认框（**服务端**弹原生框，不是网页弹）。
 *
 * 与上面的 download + apply 是两条路：
 *   - 这条路：服务端自己弹原生框、自己下载、自己替换重启（界面看不到进度）；
 *   - download + apply：界面里显示进度条与失败重试（设置屏的「下载并安装」走这条）。
 * 保留两条是因为早期只有第一条（与 Fyne 版行为一致），而它无法显示进度。
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
