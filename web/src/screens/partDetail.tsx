import { useEffect, useState } from 'react'
import { FormError, Modal } from '../components/form'
import { Btn } from '../components/ui'
import { parts as partsApi, type Part, type PartInbound } from '../lib/catalog'
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
        <col className="num" style={{ width: 84 }} />
        <col className="num" style={{ width: 84 }} />
        <col style={{ width: 112 }} />
        <col />
      </colgroup>
      <thead>
        <tr>
          <th>批次号</th>
          <th className="num">入库数量</th>
          <th className="num">剩余</th>
          <th>入库时间</th>
          <th>备注</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => (
          <tr key={r.id}>
            <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{r.batch_no}</td>
            <td className="num">
              {fmtQty(r.qty)} {unit}
            </td>
            <td className="num" style={{ fontWeight: 600 }}>
              {fmtQty(r.remaining)} {unit}
            </td>
            <td style={{ color: 'var(--color-fg-muted)' }}>{fmtInboundTime(r.created_at)}</td>
            <td style={{ color: 'var(--color-fg-muted)' }}>{r.note || '—'}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

export function PartDetailModal({
  part,
  onClose,
  onCount,
}: {
  part: Part
  onClose: () => void
  /** 打开逐批次盘点（父级关掉详情再开盘点，避免三层弹窗叠在一起）。 */
  onCount?: () => void
}) {
  const [rows, setRows] = useState<PartInbound[] | null>(null)
  const [err, setErr] = useState('')
  const [exhaustedOpen, setExhaustedOpen] = useState(false)

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
    return () => {
      alive = false
    }
  }, [part.id])

  const list = rows ?? []
  const active = list.filter((r) => r.remaining > 0)
  const exhausted = list.filter((r) => r.remaining <= 0)
  const remainingTotal = list.reduce((acc, r) => acc + r.remaining, 0)

  return (
    <>
      <Modal
        title={`零件详情 · ${part.code}`}
        onClose={onClose}
        width={620}
        zIndex={50}
        topMost={!exhaustedOpen}
        footer={
          <>
            <Btn onClick={onClose}>关闭</Btn>
            {onCount ? (
              <Btn primary onClick={onCount}>
                盘点
              </Btn>
            ) : null}
          </>
        }
      >
        <FormError text={err} />

        <div className="mb-3 flex items-center justify-between rounded-[6px] bg-[var(--color-canvas)] px-3 py-2 text-[11.5px]">
          <span>
            {part.name}
            {part.spec ? <span className="ml-1 text-[var(--color-fg-muted)]">{part.spec}</span> : null}
          </span>
          <span className="tabular-nums">
            现有 <b>{fmtQty(part.stock_qty)}</b> · 预警线 {fmtQty(part.warn_qty)} · 剩余合计{' '}
            <b>{fmtQty(remainingTotal)}</b> {part.unit}
          </span>
        </div>

        {rows === null && !err ? (
          <div className="py-6 text-center text-[12px] text-[var(--color-fg-subtle)]">加载中…</div>
        ) : (
          <>
            <div className="mb-1.5 flex items-center justify-between text-[11.5px]">
              <span className="text-[var(--color-fg-muted)]">仍有剩余的批次（{active.length}）</span>
              {exhausted.length > 0 ? (
                <button
                  type="button"
                  className="text-[var(--color-primary)] underline"
                  onClick={() => setExhaustedOpen(true)}
                >
                  查看已用完批次（{exhausted.length}）
                </button>
              ) : null}
            </div>
            <InboundTable rows={active} unit={part.unit} empty="没有仍有剩余的入库批次" />
          </>
        )}

        <p className="mt-2.5 text-[11px] text-[var(--color-fg-muted)]">
          生产扣料按先进先出扣批次，盘点可逐批改为实盘数量；现有库存恒等于各批次剩余之和。
        </p>
      </Modal>

      {/* 二级弹窗：盖在详情之上（z-60）。父弹窗此时不响应 Esc，避免一次关两层。 */}
      {exhaustedOpen ? (
        <Modal
          title={`已用完批次 · ${part.code}（${exhausted.length}）`}
          onClose={() => setExhaustedOpen(false)}
          width={600}
          zIndex={60}
          footer={<Btn onClick={() => setExhaustedOpen(false)}>返回</Btn>}
        >
          <p className="mb-2 text-[11.5px] text-[var(--color-fg-muted)]">
            这些批次的数量已经用完（剩余为 0），仍保留在台账里供追溯。若现场发现还有存货，
            可关闭本窗口后用「盘点」把实际数量填回去。
          </p>
          <InboundTable rows={exhausted} unit={part.unit} empty="没有已用完的批次" />
        </Modal>
      ) : null}
    </>
  )
}
