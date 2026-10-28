import { useEffect, useState } from 'react'
import { FormError, Modal } from '../components/form'
import { Btn } from '../components/ui'
import { parts as partsApi, type Part, type PartInbound } from '../lib/catalog'
import { fmtQty } from '../lib/num'

/**
 * 零件详情：这个零件的库存是哪几笔入库攒出来的。
 *
 * 每笔入库在完成时自动生成批次号（RK-YYYYMMDD-NNNN）并记入 part_inbounds；
 * 旧版数据升级时，现有库存整体记为一笔「历史库存初始化」。表里同时给出
 * 现有库存与累计入库——两者不必然相等：生产完成扣料、盘点调整会改动库存
 * 却不产生入库记录，所以现有库存永远以零件列表为准，这里只负责"来源可查"。
 */

function fmtInboundTime(iso: string | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  const p = (x: number) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
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

  // 累计入库是"来源账"的合计，与现有库存的关系见文件头说明。
  const total = (rows ?? []).reduce((acc, r) => acc + r.qty, 0)

  return (
    <Modal
      title={`零件详情 · ${part.code}`}
      onClose={onClose}
      width={560}
      footer={
        <Btn onClick={onClose}>关闭</Btn>
      }
    >
      <FormError text={err} />

      <div className="mb-3 flex items-center justify-between rounded-[6px] bg-[var(--color-canvas)] px-3 py-2 text-[11.5px]">
        <span>
          {part.name}
          {part.spec ? <span className="ml-1 text-[var(--color-fg-muted)]">{part.spec}</span> : null}
        </span>
        <span className="tabular-nums">
          现有 <b>{fmtQty(part.stock_qty)}</b> · 预警线 {fmtQty(part.warn_qty)} · 累计入库{' '}
          <b>{fmtQty(total)}</b> {part.unit}
        </span>
      </div>

      {rows === null && !err ? (
        <div className="py-6 text-center text-[12px] text-[var(--color-fg-subtle)]">加载中…</div>
      ) : rows !== null && rows.length > 0 ? (
        <table className="data-table is-narrow is-compact">
          <colgroup>
            <col style={{ width: 150 }} />
            <col className="num" style={{ width: 90 }} />
            <col style={{ width: 120 }} />
            <col />
          </colgroup>
          <thead>
            <tr>
              <th>批次号</th>
              <th className="num">入库数量</th>
              <th>入库时间</th>
              <th>备注</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.id}>
                <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{r.batch_no}</td>
                <td className="num">
                  {fmtQty(r.qty)} {part.unit}
                </td>
                <td style={{ color: 'var(--color-fg-muted)' }}>{fmtInboundTime(r.created_at)}</td>
                <td style={{ color: 'var(--color-fg-muted)' }}>{r.note || '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <div className="py-6 text-center text-[12px] text-[var(--color-fg-subtle)]">
          暂无入库记录
        </div>
      )}

      <p className="mt-2.5 text-[11px] text-[var(--color-fg-muted)]">
        生产完成扣料与盘点调整会改变现有库存，但不产生入库批次；现有库存以零件列表为准。
      </p>
    </Modal>
  )
}
