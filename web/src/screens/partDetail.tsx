import { useEffect, useState } from 'react'
import { FormError, Modal } from '../components/form'
import { Btn } from '../components/ui'
import { parts as partsApi, type Part, type PartInbound } from '../lib/catalog'
import { projection as projectionApi } from '../lib/projection'
import { fmtQty } from '../lib/num'

/**
 * 零件详情：这个零件的库存是哪几笔入库攒出来的、每批还剩多少。
 *
 * 每笔入库在完成时自动生成批次号（RK-YYYYMMDD-NNNN）并记入 part_inbounds；
 * 旧版数据升级时，现有库存整体记为一笔「历史库存初始化」。
 *
 * 一级界面只列**仍有剩余**的批次——盘点时要数的就是这些；数量已用完的
 * 批次收进二级弹窗，避免几十笔历史批次把"现在还有什么"淹没掉。
 * 总量恒等于全部批次剩余之和（扣料按先进先出扣、盘点逐批改），
 * 所以「现有」与「剩余合计」永远相等；两者都显示是为了让用户一眼
 * 看出账本对得上，而不是靠猜。
 */

function fmtInboundTime(iso: string | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  const p = (x: number) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

function InboundTable({ rows, unit, empty }: { rows: PartInbound[]; unit: string; empty: string }) {
  if (rows.length === 0) {
    return <div className="py-6 text-center text-[12px] text-[var(--color-fg-subtle)]">{empty}</div>
  }
  return (
    <table className="data-table is-narrow is-compact">
      <colgroup>
        <col style={{ width: 150 }} />
        <col className="num" style={{ width: 72 }} />
        <col className="num" style={{ width: 72 }} />
        <col className="num" style={{ width: 72 }} />
        <col style={{ width: 126 }} />
        <col />
      </colgroup>
      <thead>
        <tr>
          <th>批次号</th>
          <th className="num">入库数量</th>
          <th className="num">剩余</th>
          <th className="num">已排定</th>
          <th>入库时间</th>
          <th>备注</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => {
          const holders = r.occupied_by ?? []
          const title =
            holders.length > 0
              ? `被未完成批次预定：${holders.map((h) => `${h.batch_no} ${fmtQty(h.qty)}`).join('、')}`
              : '没有未完成批次预定这批料'
          return (
            <tr key={r.id}>
              <td
                className="tree-inline"
                title={r.batch_no}
                style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}
              >
                {r.batch_no}
              </td>
              <td className="num">
                {fmtQty(r.qty)} {unit}
              </td>
              <td className="num" style={{ fontWeight: 600 }}>
                {fmtQty(r.remaining)} {unit}
              </td>
              <td className="num" title={title} style={{ color: r.occupied > 0 ? 'var(--color-warn)' : undefined }}>
                {r.occupied > 0 ? `${fmtQty(r.occupied)} ${unit}` : '—'}
              </td>
              <td className="tabular-nums" style={{ color: 'var(--color-fg-muted)' }}>
                {fmtInboundTime(r.created_at)}
              </td>
              <td
                className="tree-inline"
                title={r.note || ''}
                style={{ color: 'var(--color-fg-muted)' }}
              >
                {r.note || '—'}
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

export function PartDetailModal({
  part,
  onClose,
}: {
  part: Part
  onClose: () => void
}) {
  const [rows, setRows] = useState<PartInbound[] | null>(null)
  const [err, setErr] = useState('')
  const [exhaustedOpen, setExhaustedOpen] = useState(false)
  // 投影里的在产占用（全部未完成批次对这个零件的需求合计）。
  // null = 取不到（投影失败），此时退回"已排定"的数量并在标题里说明。
  const [committed, setCommitted] = useState<number | null>(null)

  // part.id 变化由父级用 key 重挂整个弹窗（见 PartsScreen），
  // 这里只负责挂载时拉一次数据，不做同步重置。
  useEffect(() => {
    let alive = true
    partsApi
      .inbounds(part.id)
      .then((list) => {
        if (alive) setRows(list ?? [])
      })
      .catch((e) => {
        if (alive) setErr(e instanceof Error ? e.message : '入库记录加载失败')
      })
    projectionApi
      .get()
      .then((res) => {
        if (!alive) return
        const hit = res.parts.find((p) => p.part_id === part.id)
        setCommitted(hit ? hit.committed : 0)
      })
      .catch(() => {
        if (alive) setCommitted(null)
      })
    return () => {
      alive = false
    }
  }, [part.id])

  const list = rows ?? []
  const active = list.filter((r) => r.remaining > 0)
  const exhausted = list.filter((r) => r.remaining <= 0)
  const remainingTotal = list.reduce((acc, r) => acc + r.remaining, 0)
  // 在产占用：全部未完成批次对这个零件的需求合计（投影口径，和零件页一致）。
  // 「已排定」只统计投料计划里指名从某一批扣的部分；没排定的完成时按先进先出，
  // 分不到具体批次上，所以批次列的占用可能小于占用总量——标题里写清楚。
  const allottedTotal = list.reduce((acc, r) => acc + (r.occupied ?? 0), 0)
  const occupiedTotal = committed ?? allottedTotal
  const available = Math.round((remainingTotal - occupiedTotal) * 100) / 100

  return (
    <>
      <Modal
        title={`零件详情 · ${part.code}`}
        onClose={onClose}
        width={720}
        zIndex={50}
        topMost={!exhaustedOpen}
        footer={<Btn onClick={onClose}>关闭</Btn>}
      >
        <FormError text={err} />

        <div className="mb-3 flex items-center justify-between rounded-[6px] bg-[var(--color-canvas)] px-3 py-2 text-[11.5px]">
          <span>
            {part.name}
            {part.spec ? <span className="ml-1 text-[var(--color-fg-muted)]">{part.spec}</span> : null}
          </span>
          <span
            className="tabular-nums"
            data-testid="part-detail-summary"
            title={
              committed === null
                ? '投影取不到，只显示投料计划里已排定的数量'
                : '现有库存恒等于各批次剩余之和；在产占用 = 全部未完成批次的需求合计；可用 = 现有 − 在产占用'
            }
          >
            现有 <b>{fmtQty(part.stock_qty)}</b> · 在产占用 <b>{fmtQty(occupiedTotal)}</b> ·
            可用 <b>{fmtQty(available)}</b> {part.unit}
          </span>
        </div>

        {rows === null && !err ? (
          <div className="py-6 text-center text-[12px] text-[var(--color-fg-subtle)]">加载中…</div>
        ) : (
          <>
            <div className="mb-1.5 flex items-center justify-between text-[11.5px]">
              <span className="text-[var(--color-fg-muted)]">仍有剩余的批次（{active.length}）</span>
              {/* 入口常驻：只在有已用完批次时才出现的话，用户手上全是新批次就
                  完全看不到这个界面，会以为功能没做。空态由二级弹窗说明。 */}
              <button
                type="button"
                className="whitespace-nowrap px-1 text-[var(--color-primary)] leading-5 underline decoration-1 underline-offset-4"
                onClick={() => setExhaustedOpen(true)}
              >
                查看已用完批次（{exhausted.length}）
              </button>
            </div>
            <InboundTable rows={active} unit={part.unit} empty="没有仍有剩余的入库批次" />
          </>
        )}

        <p className="mt-2.5 text-[11px] text-[var(--color-fg-muted)]">
          现有库存恒等于各批次剩余之和。「已排定」是投料计划里指名从这批扣的数量；
          没排定的占用在完成时按先进先出分派，所以各批已排定之和可能小于在产占用。
          盘点请回零件列表点「盘点」。
        </p>
      </Modal>

      {/* 二级弹窗：盖在详情之上（z-60）。父弹窗此时不响应 Esc，避免一次关两层。 */}
      {exhaustedOpen ? (
        <Modal
          title={`已用完批次 · ${part.code}（${exhausted.length}）`}
          onClose={() => setExhaustedOpen(false)}
          width={720}
          zIndex={60}
          footer={<Btn onClick={() => setExhaustedOpen(false)}>返回</Btn>}
        >
          <p className="mb-2 text-[11.5px] text-[var(--color-fg-muted)]">
            这些批次的数量已经用完（剩余为 0），仍保留在台账里供追溯。若现场发现还有存货，
            可关闭本窗口后用「盘点」把实际数量填回去。
          </p>
          <InboundTable rows={exhausted} unit={part.unit} empty="还没有已用完的批次" />
        </Modal>
      ) : null}
    </>
  )
}
