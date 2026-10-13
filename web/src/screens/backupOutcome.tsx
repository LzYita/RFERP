/**
 * 破坏性操作的结局呈现（三分类）。
 *
 * 这是 §Round 5 里最不能含糊的一条：「已回滚 / 结果不确定 / 已应用但收尾失败」
 * 三种情况的**正确处置完全相反**，界面如果只显示一句文案，用户就会照着最直觉的
 * 做法——再点一次重试——把数据搞得更糟。所以：
 *
 * | state        | 配色 | 重试按钮 | 理由 |
 * |--------------|------|----------|------|
 * | rolled_back  | 中性 | **渲染** | 已确认没生效，修正原因后重试是正确做法 |
 * | unknown      | **黄** | **不渲染** | 可能已部分应用，再来一次等于叠加 |
 * | applied      | 红   | **不渲染** | 已经生效了，重试等于做两遍 |
 *
 * 「不渲染」而不是「置灰」：置灰的按钮会被读成「等会儿再试」。
 *
 * 恢复与清空共用这一个组件——两处各写一份必然漂移，而这里漂移的后果是让用户在
 * 数据可能已经损坏时又点一次恢复。
 */
import type { ReactNode } from 'react'
import { Modal } from '../components/form'
import { Btn } from '../components/ui'
import type { OperationState } from '../lib/api'

export interface OutcomeView {
  /** 操作名：「整库恢复」「清空业务数据」 */
  op: string
  state: OperationState
  /** 服务端给的中文主文案 */
  message: string
  /** 操作前副本的文件名（只给文件名，服务端不回绝对路径） */
  safetyCopy?: string
  /** 成功时的补充信息，例如各表行数 */
  details?: ReactNode
}

/** 标题。三个标题必须一眼可分——用户往往先看标题再决定要不要细读。 */
export function outcomeTitle(op: string, state: OperationState): string {
  if (state === 'rolled_back') return `${op}失败，数据未改动`
  if (state === 'unknown') return `${op}结果不明，须核对数据`
  return `${op}已生效，后续处理失败`
}

/**
 * 主文案。unknown 与 applied 必须直接把「不要重试」写出来。
 *
 * 不依赖服务端那句「请先核对…不要直接重试」被完整传回来——那是一整段话，
 * 用户很可能只读到前半句。这里在界面侧再钉一次。
 */
export function outcomeLead(op: string, state: OperationState): string {
  if (state === 'rolled_back') {
    return '数据库已回滚到操作前状态，数据没有变化。修正原因后可以重新执行。'
  }
  if (state === 'unknown') {
    return `事务提交或回滚的结果不明，${op}可能已经生效了一部分。请先核对数据与操作前副本，确认之前不要重试${op}。`
  }
  return `${op}已经生效，但后续的收尾处理失败。请先核对数据与操作前副本，再次执行会造成重复操作，不要重试${op}。`
}

/** 配色档位。unknown 必须是 yellow，不能是红色——红色会诱导人"赶紧补救"。 */
export function outcomeTone(state: OperationState): 'ok' | 'warn' | 'danger' {
  if (state === 'rolled_back') return 'ok'
  if (state === 'unknown') return 'warn'
  return 'danger'
}

const toneCls: Record<'ok' | 'warn' | 'danger', string> = {
  ok: 'border-[var(--color-border)] bg-[var(--color-canvas)]',
  warn: 'border-[#E5A50A] bg-[#FFFBEB]',
  danger: 'border-[var(--color-danger)] bg-[#FEF2F2]',
}
const toneTitleCls: Record<'ok' | 'warn' | 'danger', string> = {
  ok: 'text-[var(--color-fg)]',
  warn: 'text-[#8A6100]',
  danger: 'text-[var(--color-danger)]',
}

export function OutcomeDialog({
  view,
  onClose,
}: {
  view: OutcomeView
  onClose: () => void
}) {
  const tone = outcomeTone(view.state)
  const canRetry = view.state === 'rolled_back'
  return (
    <Modal
      title={outcomeTitle(view.op, view.state)}
      onClose={onClose}
      width={560}
      footer={
        // 只有「已确认未生效」才给重试。其余两种不渲染——置灰也会被当成「稍后再试」。
        canRetry ? (
          <Btn primary onClick={onClose}>
            我知道了，重新操作
          </Btn>
        ) : (
          <Btn primary onClick={onClose}>
            我知道了
          </Btn>
        )
      }
    >
      <div className={['rounded-[6px] border p-3', toneCls[tone]].join(' ')}>
        <p className={['text-[13px] font-semibold', toneTitleCls[tone]].join(' ')}>
          {outcomeLead(view.op, view.state)}
        </p>
        {view.message ? (
          <p className="mt-2 text-[12px] whitespace-pre-line text-[var(--color-fg-muted)]">
            {view.message}
          </p>
        ) : null}
      </div>

      {view.safetyCopy ? (
        <p className="mt-3 text-[12px] text-[var(--color-fg-muted)]">
          操作前副本：<span style={{ fontFamily: 'Consolas, monospace' }}>{view.safetyCopy}</span>
          （在备份列表中可选择它来退回）
        </p>
      ) : null}

      {view.details ? <div className="mt-3 text-[12px]">{view.details}</div> : null}
    </Modal>
  )
}
