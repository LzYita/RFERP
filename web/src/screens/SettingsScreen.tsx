import { useEffect, useState } from 'react'
import { Markdown } from '../components/Markdown'
import { ConfirmDialog } from '../components/form'
import { Btn, PageHeader, Panel, ScreenError, classifyLoadError } from '../components/ui'
import {
  helpContent,
  releaseDate,
  updateApply,
  updateCheck,
  updateDownload,
  updateProgress,
  type HelpPayload,
  type HelpVersion,
  type UpdateCheck,
} from '../lib/help'

/**
 * 设置屏：使用指南 / 常见问题 / 更新说明。
 *
 * 对齐 Fyne 版 settings.go 的三个页签与位置（侧栏左下角 → 右侧主内容区，
 * **不弹窗**）：设置属于主界面的一个视图，做成弹窗会打断对当前业务页的参照
 * （例如照着指南核对某个页面）。
 *
 * 内容来自 /api/help 一次取全——三份都是随程序内置的离线文档，
 * 没有哪一页是可选加载的。
 */
type Tab = 'guide' | 'faq' | 'changelog'

const TABS: { key: Tab; label: string }[] = [
  { key: 'guide', label: '使用指南' },
  { key: 'faq', label: '常见问题' },
  { key: 'changelog', label: '更新说明' },
]

export function SettingsScreen() {
  const [data, setData] = useState<HelpPayload | null>(null)
  const [err, setErr] = useState<{ kind: 'forbidden' | 'server'; text: string } | null>(null)
  const [tab, setTab] = useState<Tab>('guide')

  useEffect(() => {
    let alive = true
    helpContent
      .load()
      .then((d) => {
        if (alive) setData(d)
      })
      .catch((e) => {
        if (alive) setErr(classifyLoadError(e, '帮助内容'))
      })
    return () => {
      alive = false
    }
  }, [])

  if (err) return <ScreenError kind={err.kind} text={err.text} />
  if (!data) {
    return (
      <div className="grid flex-1 place-items-center text-[12px] text-[var(--color-fg-muted)]">
        正在加载帮助内容…
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-2.5">
      <PageHeader title="设置" />

      <div role="tablist" aria-label="设置" className="flex gap-1">
        {TABS.map((t) => (
          <button
            key={t.key}
            type="button"
            role="tab"
            aria-selected={tab === t.key}
            onClick={() => setTab(t.key)}
            className={[
              'h-[26px] rounded-[6px] border px-3 text-[11.5px]',
              tab === t.key
                ? 'border-[var(--color-primary)] bg-[var(--color-primary)] font-semibold text-white'
                : 'border-[var(--color-border)] bg-[var(--color-surface)] hover:bg-[var(--color-canvas)]',
            ].join(' ')}
          >
            {t.label}
          </button>
        ))}
      </div>

      <div role="tabpanel" aria-label={TABS.find((t) => t.key === tab)?.label}>
        {tab === 'guide' ? <GuideTab data={data} /> : null}
        {tab === 'faq' ? (
          <Panel className="p-3">
            <Markdown source={data.faq} />
          </Panel>
        ) : null}
        {tab === 'changelog' ? <ChangelogTab versions={data.versions} /> : null}
      </div>
    </div>
  )
}

/**
 * 使用指南页签：顶部版本区 + 指南正文。
 *
 * 版本区刻意用**服务端自报**的 currentVersion，而不是前端常量：
 * 前端写死一个版本号，发出去的 exe 一升级，界面就还在报旧号，
 * 而这种错误在任何测试里都不明显（两边各自都"对"）。
 */
function GuideTab({ data }: { data: HelpPayload }) {
  const [checking, setChecking] = useState(false)
  const [status, setStatus] = useState('')
  // 查到的新版本。放在本地 state 而不是 data 上：data 是 /api/help 的离线内容，
  // "有没有新版本"是一次在线查询的结果，两者生命周期不同。
  const [available, setAvailable] = useState<UpdateCheck | null>(null)
  const [confirming, setConfirming] = useState(false)
  const [downloading, setDownloading] = useState(false)
  const [progress, setProgress] = useState({ done: 0, total: 0 })
  const [installing, setInstalling] = useState(false)

  const runCheck = async () => {
    setChecking(true)
    setStatus('正在查询最新版本…')
    try {
      const r = await updateCheck.run()
      if (!r.enabled) {
        setAvailable(null)
        setStatus('未配置更新地址，无法在线查询。')
        return
      }
      if (!r.hasUpdate) {
        setAvailable(null)
        setStatus(`当前 v${data.currentVersion} 已是最新版本。`)
        return
      }
      setAvailable(r)
      setStatus(`发现新版本 v${r.version}（当前 v${data.currentVersion}），可点「下载并安装」。`)
    } catch (e) {
      setAvailable(null)
      setStatus(`查询失败：${(e as Error).message}`)
    } finally {
      setChecking(false)
    }
  }

  /**
   * 下载并安装（走 HTTP 端点，界面里能看到进度）。
   *
   * 与「查询」按钮服务端弹框那条路的区别：这条把进度、失败原因都留在界面里，
   * 用户能看到"正在下载 43%（8.1/18.7 MB）"而不是一个转圈。
   */
  const runDownloadAndApply = async () => {
    setConfirming(false)
    setDownloading(true)
    setProgress({ done: 0, total: 0 })
    setStatus('正在开始下载…')
    try {
      const job = await updateDownload.run({ async: true })
      if (!job.jobId) throw new Error('服务端没有返回下载任务 id')

      // 轮询进度。服务端在下载完成后再留 5 分钟记录，所以这里慢一点也不会漏掉完成状态。
      for (;;) {
        await new Promise((r) => setTimeout(r, 500))
        const p = await updateProgress.run(job.jobId)
        setProgress({ done: p.done, total: p.total })
        if (p.state === 'done') break
        if (p.state === 'error') throw new Error(p.error || '下载失败')
      }

      setStatus(`v${job.version} 下载完成，正在应用…程序会自动重启。`)
      setInstalling(true)
      await updateApply.run({ confirm: true })
      // 走到这里说明服务端已安排退出；界面会被新进程接管。
      // 刻意不改状态：重启前那一瞬间显示"正在应用…"才对。
    } catch (e) {
      setInstalling(false)
      setStatus(`更新失败：${(e as Error).message}`)
    } finally {
      setDownloading(false)
    }
  }

  const pct = progress.total > 0 ? Math.round((progress.done / progress.total) * 100) : 0
  const busy = checking || downloading || installing

  return (
    <Panel className="p-3">
      <div className="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1.5 border-b border-[var(--color-border)] pb-2.5">
        <span className="text-[13px] font-bold">软件使用指南</span>
        <span className="text-[11.5px] text-[var(--color-fg-muted)]">当前版本 v{data.currentVersion}</span>
        {data.latestVersion ? (
          <span className="text-[11.5px] text-[var(--color-fg-muted)]">
            内置记录最新 v{data.latestVersion}
          </span>
        ) : null}
        <span className="ml-auto flex items-center gap-2">
          <Btn onClick={runCheck} disabled={busy}>
            {checking ? '查询中…' : '查询最新更新'}
          </Btn>
          {available ? (
            <Btn primary onClick={() => setConfirming(true)} disabled={busy}>
              {downloading && progress.total > 0
                ? `下载中 ${pct}%`
                : installing
                  ? '正在应用…'
                  : '下载并安装'}
            </Btn>
          ) : null}
        </span>
      </div>

      {confirming && available ? (
        <ConfirmDialog
          title="安装更新"
          message={
            <>
              将下载并安装 <strong>v{available.version}</strong>（当前 v{data.currentVersion}）。
              <br />
              安装完成后程序会<b>自动重启</b>，未保存的输入会丢失。
              <br />
              更新包带数字签名，签名不通过会被拒绝。
            </>
          }
          confirmLabel={`下载 v${available.version}`}
          onConfirm={runDownloadAndApply}
          onCancel={() => setConfirming(false)}
        />
      ) : null}

      {status ? (
        <div role="status" className="mb-2 text-[11.5px] text-[var(--color-fg-muted)]">
          {status}
        </div>
      ) : null}

      {downloading && progress.total > 0 ? (
        <div
          className="mb-2"
          role="progressbar"
          aria-valuenow={pct}
          aria-valuemin={0}
          aria-valuemax={100}
        >
          <div className="h-2 rounded bg-[var(--color-canvas)]">
            <div className="h-2 rounded bg-[var(--color-primary)]" style={{ width: `${pct}%` }} />
          </div>
          <div className="mt-1 text-[11px] text-[var(--color-fg-muted)]">
            {(progress.done / 1048576).toFixed(1)} MB / {(progress.total / 1048576).toFixed(1)} MB
          </div>
        </div>
      ) : null}

      <Markdown source={data.guide} />
    </Panel>
  )
}

/**
 * 更新说明页签：左侧版本列表，右侧该版本的说明。
 *
 * 说明正文已在服务端切好小节（help.ParseNotes），这里只负责排版——
 * 前端不再实现一份「按 == 中文 == 切分」的逻辑，否则同一份 notes
 * 在两个界面上迟早切出不同段落。
 */
function ChangelogTab({ versions }: { versions: HelpVersion[] }) {
  const [selected, setSelected] = useState(0)

  if (versions.length === 0) {
    return (
      <Panel className="p-3 text-[12px] text-[var(--color-fg-muted)]">暂无历史版本记录。</Panel>
    )
  }
  const cur = versions[Math.min(selected, versions.length - 1)]

  return (
    <Panel className="flex gap-3 p-3">
      <ul aria-label="版本列表" className="w-[104px] shrink-0 space-y-0.5 border-r border-[var(--color-border)] pr-2">
        {versions.map((v, i) => (
          <li key={v.version}>
            <button
              type="button"
              aria-current={i === selected ? 'true' : undefined}
              onClick={() => setSelected(i)}
              className={[
                'w-full rounded-[4px] px-1.5 py-1 text-left text-[11.5px]',
                i === selected ? 'bg-[var(--color-canvas)] font-semibold' : 'hover:bg-[var(--color-canvas)]',
              ].join(' ')}
            >
              v{v.version}
            </button>
          </li>
        ))}
      </ul>

      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-baseline gap-2">
          <h2 className="text-[13.5px] font-bold">v{cur.version}</h2>
          {releaseDate(cur.publishedAt) ? (
            <span className="text-[11.5px] text-[var(--color-fg-muted)]">
              发布于 {releaseDate(cur.publishedAt)}
            </span>
          ) : null}
        </div>
        {cur.sections.map((sec, i) => (
          <div key={i}>
            {sec.title ? <h3 className="mt-3 text-[12.5px] font-bold">{sec.title}</h3> : null}
            <Markdown source={sec.body} />
          </div>
        ))}
      </div>
    </Panel>
  )
}
