import { useState } from 'react'
import {
  Checkbox,
  FormError,
  FormRow,
  Modal,
  NumberInput,
  TextInput,
} from '../components/form'
import { Btn } from '../components/ui'
import { parts as partsApi, type Part } from '../lib/catalog'
import { validateCounts, type CountRow } from '../lib/inboundCount'
import { parseQuantity } from '../lib/num'

/**
 * 入库 / 盘点调整。
 *
 * 这两个动作在编辑表单之外，是刻意的（与 Fyne part.go:442-502 一致）：
 * 每次库存变动都应该带着一个**原因**和**操作人**落到审计里。一个可以随手把
 * 500 改成 0 的文本框会把这条约束废掉，所以编辑零件时库存只读。
 *
 * 两种动作的语义差别很大，界面上也说清：
 *   - 入库：在现有数量上**加**。填 0 等于什么都没做。
 *     每笔入库自动生成批次号（RK-日期-序号）并记入 part_inbounds，
 *     零件详情页可查；备注选填、随批次记档。
 *   - 盘点：**逐批次**填实盘数量。每笔入库批次单独填，填 0 是合法的
 *     （这批确实用完了），但不能是负数；服务端按各批差额之和调整库存总量。
 *
 * 前置校验与 Fyne 逐字对齐：
 *   - 入库数量必须 > 0        （part.go:462）
 *   - 调整后数量不能为负       （part.go:493）
 * 另外补一条两条小数的拦截 —— 后端 representableQuantity 会拒，但那时
 * 已经填完一整张表了，当场说清更省事。
 */

export type StockMode = 'in' | 'adjust'

export function validateStock(
  mode: StockMode,
  raw: string,
): { ok: true; qty: number } | { ok: false; msg: string } {
  const label = mode === 'in' ? '入库数量' : '调整后数量'

  // 字面量闸门，见 lib/num.ts：1e5 / 0x10 / +5 / abc / 全角数字都在这里拒掉。
  const n = parseQuantity(raw, label)
  if (!n.ok) return n

  const qty = n.value
  if (mode === 'in') {
    // 与 Fyne part.go:462 一致：入库数量必须大于 0。
    // 填 0 之所以不给过，是因为它几乎总是「忘了填」，而不是真的要入库 0 个。
    if (qty <= 0) return { ok: false, msg: '数量必须大于0' }
  } else {
    // 盘点可以调到 0（盘亏到零是真实的盘点结果），但不能是负数。
    if (qty < 0) return { ok: false, msg: '数量不能为负' }
  }
  return { ok: true, qty }
}

function fmt2(n: number): string {
  return Number.isInteger(n) ? String(n) : n.toFixed(2)
}

export function StockModal({
  part,
  mode,
  busy,
  err,
  onSubmit,
  onClose,
}: {
  part: Part
  mode: StockMode
  busy: boolean
  err: string
  onSubmit: (qty: number, note: string) => void
  onClose: () => void
}) {
  // 盘点预填当前库存：多数盘点是「没有变化」，让用户直接确认；
  // 入库则留空，因为无从猜测要进多少。
  const [qty, setQty] = useState(mode === 'adjust' ? fmt2(part.stock_qty) : '')
  const [note, setNote] = useState('')
  const [localErr, setLocalErr] = useState('')

  const submit = () => {
    const v = validateStock(mode, qty)
    if (!v.ok) {
      setLocalErr(v.msg)
      return
    }
    setLocalErr('')
    onSubmit(v.qty, note.trim())
  }

  const preview =
    mode === 'in'
      ? part.stock_qty + (Number.isFinite(Number(qty)) ? Number(qty) : 0)
      : Number.isFinite(Number(qty))
        ? Number(qty)
        : part.stock_qty

  return (
    <Modal
      title={mode === 'in' ? '入库' : '盘点调整'}
      onClose={onClose}
      width={400}
      footer={
        <>
          <Btn onClick={onClose}>取消</Btn>
          <Btn primary disabled={busy} onClick={submit}>
            {busy ? '提交中…' : '确定'}
          </Btn>
        </>
      }
    >
      <FormError text={err || localErr} />

      {/* 当前状态摆在最上面：入库和盘点都要拿它做判断 */}
      <div className="mb-3 flex items-center justify-between rounded-[6px] bg-[var(--color-canvas)] px-3 py-2 text-[11.5px]">
        <span className="text-[var(--color-fg-muted)]">
          {part.code} {part.name}
        </span>
        <span className="tabular-nums">
          当前库存 <b>{fmt2(part.stock_qty)}</b> {part.unit}
        </span>
      </div>

      <FormRow
        label={mode === 'in' ? '入库数量' : '调整后数量'}
        hint={mode === 'in' ? '在现有库存上增加' : '直接设为该值，不是增减量'}
      >
        <NumberInput value={qty} onChange={setQty} autoFocus />
      </FormRow>

      {mode === 'in' && (
        <FormRow label="备注（选填）" hint="系统自动生成入库批次号，可在零件详情中查看">
          <TextInput
            value={note}
            onChange={setNote}
            placeholder="如：供应商补货"
            maxLength={200}
          />
        </FormRow>
      )}

      <div className="mb-1 text-right text-[11px] text-[var(--color-fg-muted)]">
        调整后：
        <b className="ml-1 tabular-nums">
          {fmt2(preview)} {part.unit}
        </b>
      </div>
    </Modal>
  )
}

/**
 * 逐批次盘点。
 *
 * 现场就是一批一批数：每个入库批次对着一张货/一箱料，填它实际还剩多少。
 * 因此不提供"总量盘点"输入框——总量由各批实盘之和得出，让用户同时填
 * 总量和明细，迟早会出现两者矛盾而界面不知道该信谁。
 *
 * 已用完（剩余 0）的批次同样列出来：现场翻出一箱旧的、账上已经用光的，
 * 正是要把它改回去的时候。不做筛选，避免用户以为"这里没有就是不能盘"。
 */
export function BatchCountModal({
  part,
  rows,
  busy,
  err,
  onChange,
  onSubmit,
  onClose,
}: {
  part: Part
  rows: ReadonlyArray<CountRow>
  busy: boolean
  err: string
  onChange: (next: CountRow[]) => void
  onSubmit: () => void
  onClose: () => void
}) {
  const [localErr, setLocalErr] = useState('')

  const setRaw = (id: number, raw: string) =>
    onChange(rows.map((r) => (r.inbound.id === id ? { ...r, raw } : r)))

  const bookTotal = rows.reduce((acc, r) => acc + r.inbound.remaining, 0)
  const countedTotal = rows.reduce((acc, r) => {
    const n = parseQuantity(r.raw, '实盘数量')
    return acc + (n.ok ? n.value : r.inbound.remaining)
  }, 0)
  const diff = Math.round((countedTotal - bookTotal) * 100) / 100

  const submit = () => {
    const v = validateCounts(rows)
    if (!v.ok) {
      setLocalErr(v.msg)
      return
    }
    setLocalErr('')
    onSubmit()
  }

  return (
    <Modal
      title={`盘点 · ${part.code}`}
      onClose={onClose}
      width={620}
      footer={
        <>
          <Btn onClick={onClose}>取消</Btn>
          <Btn primary disabled={busy || rows.length === 0} onClick={submit}>
            {busy ? '提交中…' : '确认盘点'}
          </Btn>
        </>
      }
    >
      <FormError text={err || localErr} />

      <div className="mb-2 flex items-center justify-between rounded-[6px] bg-[var(--color-canvas)] px-3 py-2 text-[11.5px]">
        <span className="text-[var(--color-fg-muted)]">
          {part.name} · 现有 <b>{fmt2(part.stock_qty)}</b> {part.unit}
        </span>
        <span className="tabular-nums">
          账面合计 <b>{fmt2(bookTotal)}</b> · 实盘合计 <b>{fmt2(countedTotal)}</b> · 差异{' '}
          <b style={{ color: diff === 0 ? undefined : 'var(--color-danger)' }}>
            {diff > 0 ? '+' : ''}
            {fmt2(diff)}
          </b>
        </span>
      </div>

      {rows.length === 0 ? (
        <div className="py-6 text-center text-[12px] text-[var(--color-fg-subtle)]">
          该零件还没有入库批次，无法盘点。请先「入库」建立批次。
        </div>
      ) : (
        <div className="max-h-[320px] overflow-auto rounded-[6px] border border-[var(--color-border-subtle)]">
          <table className="data-table is-compact">
            <colgroup>
              <col style={{ width: 150 }} />
              <col className="num" style={{ width: 84 }} />
              <col className="num" style={{ width: 96 }} />
              <col style={{ width: 112 }} />
              <col />
            </colgroup>
            <thead>
              <tr>
                <th>批次号</th>
                <th className="num">入库量</th>
                <th className="num">账面剩余</th>
                <th>实盘数量</th>
                <th>备注</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.inbound.id}>
                  <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>
                    {r.inbound.batch_no}
                  </td>
                  <td className="num">{fmt2(r.inbound.qty)}</td>
                  <td className="num" style={{ color: 'var(--color-fg-muted)' }}>
                    {fmt2(r.inbound.remaining)}
                  </td>
                  <td>
                    <NumberInput
                      value={r.raw}
                      onChange={(v) => setRaw(r.inbound.id, v)}
                      ariaLabel={`批次 ${r.inbound.batch_no} 的实盘数量`}
                    />
                  </td>
                  <td style={{ color: 'var(--color-fg-muted)' }}>{r.inbound.note || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <p className="mt-2 text-[11px] text-[var(--color-fg-muted)]">
        只提交改动过的批次；差异按各批之和调整库存总量。已用完的批次也可以填回数量。
      </p>
    </Modal>
  )
}

/**
 * 批量入库。
 *
 * Fyne 只做了单件入库（part.go:442）。批量这里刻意做成**同一数量逐件加**，
 * 而不是让用户填一张不同的数字表：批量入库的现实场景是「这箱 50 个，每种来
 * 一件」。真要每件不同，逐件入库即可，那本来就是单件入口的用途。
 */
export function BatchStockInModal({
  items,
  busy,
  err,
  onSubmit,
  onClose,
}: {
  items: Part[]
  busy: boolean
  err: string
  onSubmit: (partIds: number[], qty: number) => void
  onClose: () => void
}) {
  const [qty, setQty] = useState('1')
  const [localErr, setLocalErr] = useState('')

  const submit = () => {
    const v = validateStock('in', qty)
    if (!v.ok) {
      setLocalErr(v.msg)
      return
    }
    setLocalErr('')
    onSubmit(
      items.map((p) => p.id),
      v.qty,
    )
  }

  return (
    <Modal
      title={`批量入库（${items.length} 个零件）`}
      onClose={onClose}
      width={460}
      footer={
        <>
          <Btn onClick={onClose}>取消</Btn>
          <Btn primary disabled={busy} onClick={submit}>
            {busy ? '提交中…' : '确定入库'}
          </Btn>
        </>
      }
    >
      <FormError text={err || localErr} />

      <FormRow label="每个零件的入库数量" hint="对所选每个零件各增加此数量">
        <NumberInput value={qty} onChange={setQty} autoFocus />
      </FormRow>

      <div className="mb-1 max-h-[140px] overflow-auto rounded-[6px] border border-[var(--color-border-subtle)]">
        <table className="data-table is-compact">
          <colgroup>
            <col style={{ width: 130 }} />
            <col />
            <col className="num" style={{ width: 80 }} />
          </colgroup>
          <thead>
            <tr>
              <th>编码</th>
              <th>名称</th>
              <th className="num">当前</th>
            </tr>
          </thead>
          <tbody>
            {items.map((p) => (
              <tr key={p.id}>
                <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{p.code}</td>
                <td>{p.name}</td>
                <td className="num">{fmt2(p.stock_qty)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <label className="mt-2.5 flex items-center gap-1.5 text-[11px] text-[var(--color-fg-muted)]">
        <Checkbox checked disabled label={`合计增加 ${items.length} 个零件 × 数量`} />
      </label>
    </Modal>
  )
}

export { partsApi }
