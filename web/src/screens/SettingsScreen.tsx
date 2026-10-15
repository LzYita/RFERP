import { useEffect, useState } from 'react'
import { Markdown } from '../components/Markdown'
import { Btn, PageHeader, Panel, ScreenError, classifyLoadError } from '../components/ui'
import {
  helpContent,
  releaseDate,
  updateCheck,
  updatePrompt,
  type HelpPayload,
  type HelpVersion,
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

  const runCheck = async () => {
    setChecking(true)
    setStatus('正在查询最新版本…')
    try {
      const r = await updateCheck.run()
      if (!r.enabled) {
        setStatus('未配置更新地址，无法在线查询。')
        return
      }
      if (!r.hasUpdate) {
        setStatus(`当前 v${data.currentVersion} 已是最新版本。`)
        return
      }
      // 查到新版就**顺手弹框**：旧版的更新入口是启动时那个原生对话框，
      // 用户主动来查却只能看到一句"可在启动提示中安装"，等于让他自己去找一个
      // 未必还在的入口。这里把同一个框叫出来（服务端弹，与启动时同一份实现）。
      setStatus(`发现新版本 v${r.version}（当前 v${data.currentVersion}），正在弹出更新提示框…`)
      try {
        await updatePrompt.request()
        setStatus(
          `已弹出更新提示框：在对话框中点「是」即可下载并安装 v${r.version}` +
            '（更新包带数字签名，签名不通过会被拒绝）。',
        )
      } catch (e) {
        setStatus(
          `发现新版本 v${r.version}，但弹出更新提示失败：${(e as Error).message}` +
            '　可在下次启动时安装。',
        )
      }
    } catch (e) {
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
        </span>
      </div>

      {status ? (
        <div role="status" className="mb-2 text-[11.5px] text-[var(--color-fg-muted)]">
          {status}
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
