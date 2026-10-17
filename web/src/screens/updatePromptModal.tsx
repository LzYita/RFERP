import { useState } from 'react'
import { Modal } from '../components/form'
import { Markdown } from '../components/Markdown'
import { Btn } from '../components/ui'
import { releaseDate, updateApply, updateDownload, updateProgress, type UpdateCheck } from '../lib/help'

/**
 * 安装流程（下载 → 看进度 → 应用并重启）。
 *
 * 「下载 → 看进度 → 应用并重启」这一段有实打实的坑——job id 要编码、轮询间隔、
 * 完成后必须带 confirm、apply 成功后进程会被换掉所以**不能**再改状态。
 * 它只服务于本组件：登录后的自动检查与设置屏的「查询最新更新」都渲染**这个**
 * 组件，所以这里也只有一份实现可分叉（分叉的表现是「一个入口能装、另一个装完
 * 程序没换掉」）。
 */
interface Install {
  phase: 'idle' | 'downloading' | 'installing'
  done: number
  total: number
  error: string
}

const IDLE: Install = { phase: 'idle', done: 0, total: 0, error: '' }

async function runInstall(
  onTick: (s: Install) => void,
): Promise<void> {
  onTick({ ...IDLE, phase: 'downloading' })
  try {
    const job = await updateDownload.run({ async: true })
    if (!job.jobId) throw new Error('服务端没有返回下载任务 id')

    // 轮询进度。服务端在下载完成后再留 5 分钟记录，所以慢一点也不会漏掉完成状态。
    for (;;) {
      await new Promise((r) => setTimeout(r, 500))
      const p = await updateProgress.run(job.jobId)
      onTick({ phase: 'downloading', done: p.done, total: p.total, error: '' })
      if (p.state === 'done') break
      if (p.state === 'error') throw new Error(p.error || '下载失败')
    }

    onTick({ phase: 'installing', done: 0, total: 0, error: '' })
    await updateApply.run({ confirm: true })
    // 走到这里说明服务端已安排退出；界面会被新进程接管。
    // 刻意不再改状态：重启前那一瞬间显示"正在应用…"才对。
  } catch (e) {
    onTick({ ...IDLE, error: (e as Error).message })
  }
}

/**
 * 「发现新版本」弹窗。
 *
 * 它替代的是原来的原生 MessageBox（2026-10-17）。原生框那条路有两个没法绕的问题：
 *
 *  1. **撑爆窗口**：MessageBox 画不了 markdown、也没有滚动条，只能靠拉大窗口来
 *     容纳整份更新说明。结果是窗口高到「是/否」按钮被顶出屏幕，用户既看不全
 *     也点不到——实测中 1.3.0 的说明有上百行，按钮完全不可达。
 *  2. **符号裸露**：`== 中文 ==`、`- ` 这些标记以原文显示。
 *
 * 网页弹窗把这两件事一起解决：正文按 `sections` 渲染成真正的排版，放在一个
 * **固定高度可滚动**的区域里，按钮留在底部的 footer（不随正文滚动），因此正文
 * 再长按钮也始终可见。
 *
 * 安装动作复用设置屏「下载并安装」那套（useUpdateInstall），所以登录提示与手动
 * 查询在下载、进度、失败处理上完全一致。
 */
export function UpdatePromptModal({
  check,
  onClose,
}: {
  check: UpdateCheck
  onClose: () => void
}) {
  // confirm 这一步单独留在点「下载并安装」之后：弹窗本身就是给用户看新版本内容的，
  // 「要不要现在装」需要他自己按一次，不能默认替他决定。
  const [confirming, setConfirming] = useState(false)
  const [install, setInstall] = useState<Install>(IDLE)

  const version = check.version ?? ''
  const sections = check.sections ?? []
  const busy = install.phase !== 'idle'
  const pct = install.total > 0 ? Math.round((install.done / install.total) * 100) : 0

  if (confirming) {
    return (
      <Modal title="安装更新" onClose={() => setConfirming(false)} width={440}>
        <p className="text-[12.5px] leading-relaxed">
          将下载并安装 <strong>v{version}</strong>
          {check.currentVersion ? `（当前 v${check.currentVersion}）` : ''}。
        </p>
        <p className="mt-2 text-[12.5px] leading-relaxed">
          安装完成后程序会<b>自动重启</b>，未保存的输入会丢失。
        </p>
        <p className="mt-2 text-[11px] text-[var(--color-fg-muted)]">
          更新包带数字签名，签名不通过会被拒绝。
        </p>
        <div className="mt-4 flex justify-end gap-2">
          <Btn onClick={() => setConfirming(false)} disabled={busy}>
            取消
          </Btn>
          {/* 开始下载时必须**退回主视图**：进度条和失败原因都画在主视图上，
              停在确认框里的话用户点了「下载」之后什么都看不到，只有一个不再
              变化的确认框——看起来像卡死了（这一条是 settings 的下载失败用例
              逼出来的：确认后找不到「更新失败：…」这句话）。 */}
          <Btn
            primary
            onClick={() => {
              setConfirming(false)
              void runInstall(setInstall)
            }}
          >
            下载 v{version}
          </Btn>
        </div>
      </Modal>
    )
  }

  return (
    <Modal
      title="软件更新"
      // 下载/应用期间不给出口：此时关掉弹窗，用户既看不到进度也不知道程序在干什么，
      // 而后台的下载并不会因此停下。
      onClose={onClose}
      dismissible={!busy}
      width={560}
    >
      <div className="flex min-h-0 flex-col">
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1 px-4 pt-3 pb-2">
          <span className="text-[13px] font-semibold">发现新版本 v{version}</span>
          {check.currentVersion ? (
            <span className="text-[11.5px] text-[var(--color-fg-muted)]">
              当前 v{check.currentVersion}
            </span>
          ) : null}
          {releaseDate(check.publishedAt) ? (
            <span className="text-[11.5px] text-[var(--color-fg-muted)]">
              发布于 {releaseDate(check.publishedAt)}
            </span>
          ) : null}
          {check.size ? (
            <span className="text-[11.5px] text-[var(--color-fg-muted)]">
              {(check.size / 1024 / 1024).toFixed(1)} MB
            </span>
          ) : null}
        </div>

        {/* 正文：限高 + 滚动。按钮在下面的 footer 里，不跟着正文走 ——
            这正是原生框做不到的那一点（那边只能整体拉高窗口）。 */}
        <div className="max-h-[46vh] min-h-0 overflow-y-auto border-y border-[var(--color-border)] px-4 py-3">
          {sections.length > 0 ? (
            sections.map((sec, i) => (
              <div key={i}>
                {sec.title ? <h3 className="mt-3 text-[12.5px] font-bold">{sec.title}</h3> : null}
                <Markdown source={sec.body} />
              </div>
            ))
          ) : (
            <p className="text-[12px] text-[var(--color-fg-muted)]">
              这个版本没有附带更新说明。
            </p>
          )}
        </div>

        <div className="px-4 pt-3 pb-4">
          {install.error ? (
            <p className="mb-2 text-[11.5px]" style={{ color: 'var(--color-danger)' }}>
              更新失败：{install.error}
            </p>
          ) : null}

          {install.phase === 'downloading' ? (
            <div className="mb-3" aria-label="下载进度">
              <div className="mb-1 flex items-baseline justify-between text-[11.5px] text-[var(--color-fg-muted)]">
                <span>正在下载…</span>
                <span>
                  {install.total > 0
                    ? `${pct}%（${(install.done / 1024 / 1024).toFixed(1)}/${(
                        install.total /
                        1024 /
                        1024
                      ).toFixed(1)} MB）`
                    : '正在获取包大小…'}
                </span>
              </div>
              <div className="h-[6px] overflow-hidden rounded-[3px] bg-[var(--color-canvas)]">
                <div
                  className="h-full bg-[var(--color-accent)] transition-[width] duration-200"
                  style={{ width: `${pct}%` }}
                />
              </div>
            </div>
          ) : null}

          {install.phase === 'installing' ? (
            <p className="mb-3 text-[11.5px] text-[var(--color-fg-muted)]">
              下载完成，正在应用…程序会自动重启。
            </p>
          ) : null}

          <div className="flex justify-end gap-2">
            <Btn onClick={onClose} disabled={busy}>
              稍后
            </Btn>
            <Btn primary onClick={() => setConfirming(true)} disabled={busy}>
              下载并安装
            </Btn>
          </div>
        </div>
      </div>
    </Modal>
  )
}
