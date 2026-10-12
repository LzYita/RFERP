import { useState } from 'react'
import {
  Checkbox,
  FormError,
  FormRow,
  Modal,
  NumberInput,
} from '../components/form'
import { Btn } from '../components/ui'
import { parts as partsApi, type Part } from '../lib/catalog'

/**
 * 入库 / 盘点调整。
 *
 * 这两个动作在编辑表单之外，是刻意的（与 Fyne part.go:442-502 一致）：
 * 每次库存变动都应该带着一个**原因**和**操作人**落到审计里。一个可以随手把
 * 500 改成 0 的文本框会把这条约束废掉，所以编辑零件时库存只读。
 *
 * 两种动作的语义差别很大，界面上也说清：
 *   - 入库：在现有数量上**加**。填 0 等于什么都没做。
 *   - 盘点：直接设为**绝对值**。填 0 是合法的（盘亏到零），但负数不行。
 *
 * 前置校验与 Fyne 逐字对齐：
 *   - 入库数量必须 > 0        （part.go:462）
 *   - 调整后数量不能为负       （part.go:493）
 * 另外补一条两条小数的拦截 —— 后端 representableQuantity 会拒，但那时
 * 已经填完一整张表了，当场说清更省事。
 */

export type StockMode = 'in' | 'adjust'

function tooManyDecimals(t: string): boolean {
  const m = /^(\d*)(?:\.(\d*))?$/.exec(t.trim())
  if (!m) return false
  const frac = (m[2] ?? '').replace(/0+$/, '')
  return frac.length > 2
}

export function validateStock(
  mode: StockMode,
  raw: string,
): { ok: true; qty: number } | { ok: false; msg: string } {
  const t = raw.trim()
  if (t === '') return { ok: false, msg: mode === 'in' ? '请填写入库数量' : '请填写调整后数量' }
  if (!Number.isFinite(Number(t))) {
    return { ok: false, msg: '请输入有效数值' }
  }
  if (tooManyDecimals(t)) return { ok: false, msg: '数量最多支持两位小数' }

  const qty = Number(t)
  if (mode === 'in') {
    // 与 Fyne 一致：入库数量必须大于 0。
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
  onSubmit: (qty: number) => void
  onClose: () => void
}) {
  // 盘点预填当前库存：多数盘点是「没有变化」，让用户直接确认；
  // 入库则留空，因为无从猜测要进多少。
  const [qty, setQty] = useState(mode === 'adjust' ? fmt2(part.stock_qty) : '')
  const [localErr, setLocalErr] = useState('')

  const submit = () => {
    const v = validateStock(mode, qty)
    if (!v.ok) {
      setLocalErr(v.msg)
      return
    }
    setLocalErr('')
    onSubmit(v.qty)
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
        <Checkbox checked disabled label={`合计增加 ${items.length} × 数量`} />
      </label>
    </Modal>
  )
}

export { partsApi }
