import { useEffect, useState } from 'react'
import { Markdown } from '../components/Markdown'
import { Btn, PageHeader, Panel, ScreenError, classifyLoadError } from '../components/ui'
import {
  helpContent,
  releaseDate,
  updateCheck,
  type HelpPayload,
  type HelpVersion,
  type UpdateCheck,
} from '../lib/help'
import { UpdatePromptModal } from './updatePromptModal'

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
  // 弹窗另开一个开关，而不是直接拿 available 当条件：用户点「稍后」之后弹窗要关，
  // 但 available 得留着 —— 顶部那个「下载并安装」按钮要能再把它叫出来。
  const [modalOpen, setModalOpen] = useState(false)

  const runCheck = async () => {
    setChecking(true)
    setStatus('正在查询最新版本…')
    try {
      const r = await updateCheck.run()
      if (!r.enabled) {
        setAvailable(null)
        setModalOpen(false)
        setStatus('未配置更新地址，无法在线查询。')
        return
      }
      if (!r.hasUpdate) {
        setAvailable(null)
        setModalOpen(false)
        // 有源"验签被拒/清单不可用"时**不能**说"已是最新版本"：那是把一次发版事故
        // （比如签错了密钥）伪装成一切正常，用户和你都看不出来（先决项⑤）。
        setStatus(
          r.warning
            ? `更新源返回的清单未通过校验，已拒绝：${r.warning}`
            : `当前 v${data.currentVersion} 已是最新版本。`,
        )
        return
      }
      setAvailable(r)
      // 查到就直接弹窗，与登录后那次自动检查是**同一个** UpdatePromptModal。
      // 之前这里是"状态行 + 一个确认框"，更新说明只躺在下面的指南正文里，
      // 用户点「下载并安装」也看不到这一版改了什么（用户报的现象）。
      setModalOpen(true)
      setStatus(`发现新版本 v${r.version}（当前 v${data.currentVersion}），请在新窗口中确认。`)
    } catch (e) {
      setAvailable(null)
      setModalOpen(false)
      setStatus(`查询失败：${(e as Error).message}`)
    } finally {
      setChecking(false)
    }
  }

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
          <Btn onClick={runCheck} disabled={checking}>
            {checking ? '查询中…' : '查询最新更新'}
          </Btn>
          {/* 用户点「稍后」关掉弹窗后，这就是把它再叫回来的入口。 */}
          {available ? (
            <Btn primary onClick={() => setModalOpen(true)}>
              下载并安装
            </Btn>
          ) : null}
        </span>
      </div>

      {status ? (
        <div role="status" className="mb-2 text-[11.5px] text-[var(--color-fg-muted)]">
          {status}
        </div>
      ) : null}

      {/* 下载进度与失败原因现在都在弹窗里（UpdatePromptModal 自带），
          这一层不再重复一份 —— 两条入口共用同一个弹窗，也就只有一处进度条。 */}

      {available && modalOpen ? (
        <UpdatePromptModal check={available} onClose={() => setModalOpen(false)} />
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
