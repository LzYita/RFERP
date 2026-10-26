import { useEffect, useMemo, useState } from 'react'
import {
  Checkbox,
  FormError,
  FormGrid,
  FormRow,
  Modal,
  NumberInput,
  TextInput,
} from '../components/form'
import { Btn, fmtQty } from '../components/ui'
import { parseQuantity } from '../lib/num'
import type { BOMItem, Part, Product } from '../lib/catalog'
import { bom as bomApi } from '../lib/catalog'
import { buildPreviewRows, projection as projectionApi, type PreviewRow } from '../lib/projection'
import {
  bomConsume,
  cascadeFor,
  defaultBatchNo,
  resolveProduct,
  type Shortage,
} from '../lib/batch'

/* ============================================================
 * 新建生产批次
 * ============================================================ */

/** 新建批次的提交载荷。status 不在这里——它恒为 0，见 BatchScreen 的说明。 */
export interface NewBatchInput {
  product: Product
  batchNo: string
  planQty: number
  customer: string
}

export function CreateBatchModal({
  products,
  parts,
  onSubmit,
  onClose,
}: {
  products: Product[]
  parts: Part[]
  onSubmit: (input: NewBatchInput, shortages: Shortage[]) => void
  onClose: () => void
}) {
  const [name, setName] = useState('')
  const [code, setCode] = useState('')
  const [spec, setSpec] = useState('')
  const [batchNo, setBatchNo] = useState(() => defaultBatchNo())
  const [planQty, setPlanQty] = useState('100')
  const [customer, setCustomer] = useState('')
  const [err, setErr] = useState('')

  const cas = useMemo(() => cascadeFor(products, name, code), [products, name, code])
  const product = useMemo(() => resolveProduct(products, name, code, spec), [products, name, code, spec])

  // BOM 明细用于缺料预检。屏幕上有 BOM 屏的数据，但没有现成的"按产品取 BOM"缓存，
  // 所以这里由调用方传入（下面用 useBOMFor 拉）。
  const [bom, setBom] = useState<BOMItem[] | null>(null)
  const [loadErr, setLoadErr] = useState('')

  useEffect(() => {
    if (!product) {
      setBom(null)
      setLoadErr('')
      return
    }
    let alive = true
    setLoadErr('')
    bomApi
      .list(product.id)
      .then((rows) => {
        if (alive) setBom(rows)
      })
      .catch((e) => {
        if (alive) setLoadErr(e instanceof Error ? e.message : '查询BOM失败')
      })
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [product?.id])

  function submit() {
    if (!name) return setErr('请选择产品名称')
    if (!code) return setErr('请选择产品编码')
    if (!product) return setErr('未找到匹配的产品')
    const q = parseQuantity(planQty, '计划数量')
    if (!q.ok) return setErr('计划数量必须是大于0的整数')
    if (q.value <= 0 || !Number.isInteger(q.value)) {
      return setErr('计划数量必须是大于0的整数')
    }
    setErr('')
    onSubmit(
      { product, batchNo: batchNo.trim(), planQty: q.value, customer },
      shortagesOf(q.value, bom ?? [], parts),
    )
  }

  return (
    <Modal
      title="新建生产批次"
      onClose={onClose}
      // 备料预览有 6 列，500px 会把名称列压到 0 宽、文字直接压到下一列上。
      width={600}
      footer={
        <>
          <Btn onClick={onClose}>取消</Btn>
          <Btn primary onClick={submit}>
            确定
          </Btn>
        </>
      }
    >
      <FormError text={err || loadErr} />

      {/* 级联三级：与 Fyne batch.go:284-286 同构，下级未就绪时禁用 */}
      <FormRow label="产品名称">
        <select
          className={selCls}
          value={name}
          onChange={(e) => {
            setName(e.target.value)
            setCode('')
            setSpec('')
          }}
        >
          <option value="">全部</option>
          {cas.names.map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
      </FormRow>
      <FormGrid>
        <FormRow label="产品编码">
          <select
            className={selCls}
            value={code}
            disabled={!name}
            onChange={(e) => {
              setCode(e.target.value)
              setSpec('')
            }}
          >
            <option value="">全部</option>
            {cas.codes.map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </select>
        </FormRow>
        <FormRow label="规格型号">
          <select
            className={selCls}
            value={spec}
            disabled={cas.specs.length === 0}
            onChange={(e) => setSpec(e.target.value)}
          >
            <option value="">全部</option>
            {cas.specs.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        </FormRow>
      </FormGrid>

      <FormGrid>
        <FormRow label="批次号">
          <TextInput value={batchNo} onChange={setBatchNo} mono />
        </FormRow>
        <FormRow label="计划数量">
          <NumberInput value={planQty} onChange={setPlanQty} />
        </FormRow>
      </FormGrid>
      <FormRow label="客户">
        <TextInput value={customer} onChange={setCustomer} placeholder="客户名称（选填）" />
      </FormRow>

      {bom && bom.length === 0 && (
        <WarnBox>该产品没有 BOM 明细。批次仍可创建，但完成生产时不会扣除任何零件。</WarnBox>
      )}
      {product && bom && bom.length > 0 ? (
        <StockPreview productId={product.id} planQty={planQty} parts={parts} fallbackItems={bom} />
      ) : null}
    </Modal>
  )
}

const selCls =
  'h-[26px] w-full rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-2 text-[11.5px] outline-none focus:border-[var(--color-primary)] disabled:bg-[var(--color-canvas)] disabled:text-[var(--color-fg-subtle)]'

/**
 * 建批预览：把这一批的计划并入投影，逐零件显示
 * 现有 / 其它在产占用 / 本批需要 / 全部完成后 / 预警线。
 *
 * 它补的正是「库存只在完成时才扣、未完成批次看起来库存充足」造成的空档：
 * 两个批次各自都够、合起来不够，在建第二批时就能看见。
 * **不阻止保存**——建批只是登记计划，硬拦仍在完成生产那一步。
 *
 * 投影接口失败时退回原来的「按 BOM 预计消耗」表：预测拿不到，至少让用户
 * 看到本批会用多少料，而不是一片空白。
 */
function StockPreview({
  productId,
  planQty,
  parts,
  fallbackItems,
}: {
  productId: number
  planQty: string
  parts: Part[]
  fallbackItems: BOMItem[]
}) {
  const parsed = parseQuantity(planQty, '计划数量')
  const qty = parsed.ok && Number.isInteger(parsed.value) && parsed.value > 0 ? parsed.value : 0
  const [rows, setRows] = useState<PreviewRow[] | null>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    if (qty <= 0) return
    let alive = true
    // 计划数量是逐位输入的：等停顿再请求，300ms 足以区分"手停"与"还在打"。
    // 清空旧结果放在定时器里（异步），不在 effect 体内同步 setState——
    // 后者会触发一次多余渲染，也是 lint 明令警告的写法。
    const timer = setTimeout(() => {
      setRows(null)
      setFailed(false)
      projectionApi
        .get({ productId, planQty: qty })
        .then((res) => {
          if (!alive) return
          setRows(buildPreviewRows(res, parts))
          setFailed(false)
        })
        .catch(() => {
          if (alive) setFailed(true)
        })
    }, 300)
    return () => {
      alive = false
      clearTimeout(timer)
    }
  }, [productId, qty, parts])

  if (qty <= 0) {
    return (
      <div className="mt-1 rounded-[6px] border border-[var(--color-border-subtle)] px-2.5 py-2 text-[11px] text-[var(--color-fg-muted)]">
        填写计划数量后显示备料预览。
      </div>
    )
  }
  if (failed) return <BomPreview planQty={qty} items={fallbackItems} />
  if (rows === null) {
    return (
      <div className="mt-1 rounded-[6px] border border-[var(--color-border-subtle)] px-2.5 py-2 text-[11px] text-[var(--color-fg-muted)]">
        正在计算本批备料情况…
      </div>
    )
  }
  if (rows.length === 0) {
    return (
      <div className="mt-1 rounded-[6px] border border-[var(--color-border-subtle)] px-2.5 py-2 text-[11px] text-[var(--color-fg-muted)]">
        该产品没有需要消耗的零件。
      </div>
    )
  }

  const short = rows.filter((r) => r.level === 'short')
  const low = rows.filter((r) => r.level === 'low')
  const conclusion =
    short.length > 0
      ? `现有在产批次全部完成后，${short.length} 个零件会不足：本批无法完成，需先补货或调整用量。`
      : low.length > 0
        ? `本批可以完成，但 ${low.length} 个零件完成后将低于预警线，建议提前补货。`
        : '本批所需零件库存充足。'
  const conclusionColor =
    short.length > 0
      ? 'text-[var(--color-danger)]'
      : low.length > 0
        ? 'text-[var(--color-warn)]'
        : 'text-[var(--color-ok)]'

  return (
    <div className="mt-1 rounded-[6px] border border-[var(--color-border-subtle)]">
      <div className="flex items-center gap-2 border-b border-[var(--color-border-subtle)] px-2.5 py-1 text-[11px]">
        <span className="shrink-0 text-[var(--color-fg-muted)]">备料预览</span>
        <span className={conclusionColor}>{conclusion}</span>
      </div>
      <div className="max-h-[170px] overflow-auto">
        {/*
          列宽必须留够：`table-layout: fixed` 下，固定列之和一旦超过弹窗内宽，
          表格就会横向溢出，`auto` 那一列被压到 0 宽，文字直接压到相邻列上
          （就是"装饰圈0"那种重叠）。所以名称并入零件列，不再单独占一列。
        */}
        <table className="data-table is-narrow">
          <colgroup>
            <col />
            <col style={{ width: 62 }} />
            <col style={{ width: 72 }} />
            <col style={{ width: 72 }} />
            <col style={{ width: 80 }} />
            <col style={{ width: 62 }} />
          </colgroup>
          <thead>
            <tr>
              <th>零件</th>
              <th className="num">现有</th>
              <th className="num" title="其它未完成批次已占用、完成时才扣的数量">
                其它占用
              </th>
              <th className="num">本批需要</th>
              <th className="num">全部完成后</th>
              <th className="num">预警线</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.partId}>
                <td className="tree-inline" title={`${r.code} ${r.name}`}>
                  <span style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{r.code}</span>
                  <span className="ml-1.5 text-[var(--color-fg-muted)]">{r.name}</span>
                </td>
                <td className="num">{fmtQty(r.stock)}</td>
                <td className="num" style={{ color: 'var(--color-fg-muted)' }}>
                  {r.others > 0 ? fmtQty(r.others) : '—'}
                </td>
                <td className="num">{fmtQty(r.planned)}</td>
                <td
                  className="num"
                  style={{
                    color:
                      r.level === 'short'
                        ? 'var(--color-danger)'
                        : r.level === 'low'
                          ? 'var(--color-warn)'
                          : undefined,
                    fontWeight: r.level === 'ok' ? undefined : 600,
                  }}
                >
                  {fmtQty(r.after)}
                </td>
                <td className="num" style={{ color: 'var(--color-fg-muted)' }}>
                  {fmtQty(r.warn)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function BomPreview({ planQty, items }: { planQty: number; items: BOMItem[] }) {
  return (
    <div className="mt-1 rounded-[6px] border border-[var(--color-border-subtle)]">
      <div className="border-b border-[var(--color-border-subtle)] px-2.5 py-1 text-[11px] text-[var(--color-fg-muted)]">
        按 BOM 预计消耗（完成生产时实际扣除）
      </div>
      <div className="max-h-[110px] overflow-auto">
        <table className="data-table is-compact">
          <colgroup>
            <col style={{ width: 130 }} />
            <col />
            <col className="num" style={{ width: 90 }} />
          </colgroup>
          <thead>
            <tr>
              <th>零件编码</th>
              <th>零件名称</th>
              <th className="num">预计消耗</th>
            </tr>
          </thead>
          <tbody>
            {items.map((b) => (
              <tr key={b.id}>
                <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{b.part_code ?? '—'}</td>
                <td>{b.part_name ?? '—'}</td>
                <td className="num">{fmtQty(bomConsume(planQty, b))}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

export function WarnBox({ children }: { children: React.ReactNode }) {
  return (
    <div className="mt-2 rounded-[6px] border border-[var(--color-warn)]/30 bg-[var(--color-warn-bg)] px-2.5 py-1.5 text-[11px] text-[var(--color-warn)]">
      {children}
    </div>
  )
}

export function shortagesOf(planQty: number, bom: BOMItem[], parts: Part[]): Shortage[] {
  const map = new Map(parts.map((p) => [p.id, p]))
  const out: Shortage[] = []
  for (const b of bom) {
    const p = map.get(b.part_id)
    if (!p) continue
    const need = bomConsume(planQty, b)
    if (need > p.stock_qty) {
      out.push({ partId: b.part_id, name: b.part_name ?? '', code: b.part_code ?? '', needed: need, have: p.stock_qty })
    }
  }
  return out
}

/** 缺料清单。标题与逐行格式与 Fyne batch.go:364-368 一致。 */
export function ShortageModal({
  lines,
  onCancel,
}: {
  lines: string[]
  onCancel: () => void
}) {
  return (
    <Modal
      title="库存不足"
      onClose={onCancel}
      dismissible={false}
      width={440}
      footer={
        <Btn primary onClick={onCancel}>
          我知道了
        </Btn>
      }
    >
      <div className="text-[12px] leading-relaxed">
        以下零件库存不足，无法创建批次：
        {lines.map((text, i) => (
          <div key={i} className="mt-1.5">
            {text}
          </div>
        ))}
      </div>
    </Modal>
  )
}

/* ============================================================
 * 记录投料
 * ============================================================ */

export function RecordTraceModal({
  batchNo,
  planQty,
  items,
  busy,
  err,
  onSubmit,
  onClose,
}: {
  batchNo: string
  planQty: number
  items: BOMItem[]
  busy: boolean
  err: string
  onSubmit: (rows: { partId: number; usedQty: number; partBatchNo: string; supplier: string }[]) => void
  onClose: () => void
}) {
  // 用量预填按 BOM 算出的消耗量，四舍五入到整数（Fyne batch.go:434 用 %.0f）
  const [rows, setRows] = useState(() =>
    items.map((b) => ({
      partId: b.part_id,
      usedQty: String(Math.round(bomConsume(planQty, b))),
      partBatchNo: '',
      supplier: '',
    })),
  )
  const [localErr, setLocalErr] = useState('')

  function submit() {
    // 先就地校验，出错时留在弹窗里并把错误显示出来，而不是把坏数据交给
    // 父组件再等它报错——用户已经填了一屏，弹回去才知道第几行错了很糟。
    const out: { partId: number; usedQty: number; partBatchNo: string; supplier: string }[] = []
    for (const r of rows) {
      const raw = r.usedQty.trim()
      if (raw === '') continue // Fyne batch.go:455-457：空用量静默跳过该行
      const q = parseQuantity(raw, '投料数量')
      if (!q.ok) {
        setLocalErr(q.msg)
        return
      }
      if (q.value <= 0) {
        setLocalErr('投料数量必须是大于0的有限数值')
        return
      }
      out.push({ partId: r.partId, usedQty: q.value, partBatchNo: r.partBatchNo, supplier: r.supplier })
    }
    setLocalErr('')
    onSubmit(out)
  }

  return (
    <Modal
      title={`记录投料 - ${batchNo}`}
      onClose={onClose}
      width={620}
      footer={
        <>
          <Btn onClick={onClose}>取消</Btn>
          <Btn primary disabled={busy} onClick={submit}>
            {busy ? '保存中…' : '确定'}
          </Btn>
        </>
      }
    >
      <FormError text={localErr || err} />
      <div className="mb-2 text-[11px] text-[var(--color-fg-muted)]">
        留空表示不记录该零件。投料记录只作追溯，不影响库存——库存只在「完成生产」时扣除。
      </div>
      <div className="max-h-[320px] overflow-auto rounded-[6px] border border-[var(--color-border)]">
        <table className="data-table is-compact">
          <colgroup>
            <col style={{ width: 150 }} />
            <col style={{ width: 90 }} />
            <col style={{ width: 130 }} />
            <col />
          </colgroup>
          <thead>
            <tr>
              <th>零件</th>
              <th className="num">用量</th>
              <th>批次号</th>
              <th>供应商</th>
            </tr>
          </thead>
          <tbody>
            {items.map((b, i) => (
              <tr key={b.part_id}>
                <td>
                  <div style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{b.part_code ?? '—'}</div>
                  <div className="text-[11px] text-[var(--color-fg-muted)]">{b.part_name ?? '—'}</div>
                </td>
                <td>
                  <NumberInput
                    value={rows[i].usedQty}
                    onChange={(v) =>
                      setRows((prev) => prev.map((r, j) => (j === i ? { ...r, usedQty: v } : r)))
                    }
                  />
                </td>
                <td>
                  <TextInput
                    value={rows[i].partBatchNo}
                    placeholder="批次号"
                    onChange={(v) =>
                      setRows((prev) => prev.map((r, j) => (j === i ? { ...r, partBatchNo: v } : r)))
                    }
                  />
                </td>
                <td>
                  <TextInput
                    value={rows[i].supplier}
                    placeholder="供应商"
                    onChange={(v) =>
                      setRows((prev) => prev.map((r, j) => (j === i ? { ...r, supplier: v } : r)))
                    }
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Modal>
  )
}

/* ============================================================
 * 跳过零件
 * ============================================================ */

export function SkipPartsModal({
  batchNo,
  items,
  already,
  busy,
  onSubmit,
  onClose,
}: {
  batchNo: string
  items: BOMItem[]
  already: Set<number>
  busy: boolean
  onSubmit: (next: Set<number>) => void
  onClose: () => void
}) {
  const [checked, setChecked] = useState<Set<number>>(() => new Set(already))

  // 只有标记了「可替换」的零件才能跳过：完成生产时跳过的零件不扣库存，
  // 而不可替换的零件是 BOM 的必需项，不该允许跳过（Fyne batch.go:554）。
  const replaceable = items.filter((b) => b.replaceable === 1)

  return (
    <Modal
      title={`跳过零件 - ${batchNo}`}
      onClose={onClose}
      width={460}
      footer={
        <>
          <Btn onClick={onClose}>取消</Btn>
          <Btn primary disabled={busy} onClick={() => onSubmit(checked)}>
            {busy ? '保存中…' : '保存'}
          </Btn>
        </>
      }
    >
      <div className="mb-2.5">
        <div className="text-[11.5px] font-semibold">选择要跳过的零件</div>
        <div className="text-[11px] text-[var(--color-fg-muted)]">
          勾选后，完成生产时将不扣除该零件库存
        </div>
      </div>
      {replaceable.length === 0 ? (
        <WarnBox>
          该产品的BOM中没有标记为「可替换」的零件
          <br />
          请在BOM管理中设置
        </WarnBox>
      ) : (
        <div className="max-h-[260px] overflow-auto">
          {replaceable.map((b) => (
            <label
              key={b.part_id}
              className="flex cursor-pointer items-center gap-2 rounded-[4px] px-1.5 py-1.5 text-[12px] hover:bg-[var(--color-canvas)]"
            >
              <Checkbox
                checked={checked.has(b.part_id)}
                onChange={(c) => {
                  setChecked((prev) => {
                    const next = new Set(prev)
                    if (c) next.add(b.part_id)
                    else next.delete(b.part_id)
                    return next
                  })
                }}
                label=""
              />
              <span>
                {b.part_name ?? ''} <span className="text-[var(--color-fg-muted)]">[{b.part_code ?? ''}]</span>
              </span>
            </label>
          ))}
        </div>
      )}
    </Modal>
  )
}