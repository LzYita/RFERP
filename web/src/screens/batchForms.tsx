import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Checkbox,
  ComboInput,
  FormError,
  FormGrid,
  FormRow,
  Modal,
  NumberInput,
  TextInput,
} from '../components/form'
import { Badge, Btn, fmtQty } from '../components/ui'
import { parseQuantity } from '../lib/num'
import type { BOMItem, Part, Product } from '../lib/catalog'
import { bom as bomApi } from '../lib/catalog'
import {
  buildPreviewRows,
  projection as projectionApi,
  type PartProjection,
  type PreviewRow,
} from '../lib/projection'
import {
  bomConsume,
  cascadeFor,
  defaultBatchNo,
  resolveProduct,
  type Shortage,
  type TempPart,
  type TempPartDerivation,
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
  if (failed) {
    return (
      <div>
        <div className="mt-1 rounded-[6px] border border-[var(--color-warn)] bg-[var(--color-warn-bg)] px-2.5 py-1.5 text-[11px] text-[var(--color-warn)]">
          在产占用数据加载失败：以下只显示本批的 BOM 用量，不含其它在产批次的占用。
        </div>
        <BomPreview planQty={qty} items={fallbackItems} />
      </div>
    )
  }
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
      ? // 不足是"全部在产批次（含本批）合起来"的结论：本批自己可能先做就能做完，
        // 说成"本批无法完成"会误导。缺的永远是"这些批次不能同时完成"。
        `全部在产批次（含本批）完成后，${short.length} 个零件会不足：这些批次无法全部完成，需先补货或调整计划。`
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

/**
 * 投料行的展示元数据，与 rows 一一对应（同下标）。
 * tag 告诉渲染层这一行是什么：BOM 常规行 / 已跳过（禁用）/ 临时零件（新增行）。
 */
type TraceLine = { partId: number; code: string; name: string; tag: 'bom' | 'skipped' | 'temp' }
type TraceRow = { partId: number; usedQty: string; partBatchNo: string; supplier: string; locked: boolean }

export function RecordTraceModal({
  batchNo,
  planQty,
  items,
  skipped,
  temps,
  busy,
  err,
  onSubmit,
  onClose,
}: {
  batchNo: string
  planQty: number
  items: BOMItem[]
  /** 已跳过的零件：完成时不扣库存，也不允许记投料。行保留但禁用——
      直接消失会让人以为漏了，摆出来带「已跳过」标记才看得懂状态变化。 */
  skipped: ReadonlySet<number>
  /** 临时零件：不在 BOM 里、这批实际要用，作为新增的元组行参与投料。 */
  temps: ReadonlyArray<TempPart>
  busy: boolean
  err: string
  onSubmit: (rows: { partId: number; usedQty: number; partBatchNo: string; supplier: string }[]) => void
  onClose: () => void
}) {
  // 行清单在打开时定死：BOM 行在前（被跳过的行禁用），临时零件行跟在后面。
  // 「零件管理」里刚加的跳过/临时零件，下次打开这里自然生效。
  const lines: TraceLine[] = [
    ...items.map((b) => ({
      partId: b.part_id,
      code: b.part_code ?? '—',
      name: b.part_name ?? '—',
      tag: (skipped.has(b.part_id) ? 'skipped' : 'bom') as TraceLine['tag'],
    })),
    ...temps.map((tp) => ({ partId: tp.part_id, code: tp.code, name: tp.name, tag: 'temp' as const })),
  ]
  const [rows, setRows] = useState<TraceRow[]>(() => {
    const bomRows: TraceRow[] = items.map((b) => ({
      partId: b.part_id,
      // 用量预填按 BOM 算出的消耗量，四舍五入到整数（Fyne batch.go:434 用 %.0f）
      usedQty: String(Math.round(bomConsume(planQty, b))),
      partBatchNo: '',
      supplier: '',
      locked: skipped.has(b.part_id),
    }))
    const tempRows: TraceRow[] = temps.map((tp) => ({
      partId: tp.part_id,
      usedQty: String(tp.qty),
      partBatchNo: '',
      supplier: '',
      locked: false,
    }))
    return [...bomRows, ...tempRows]
  })
  const [localErr, setLocalErr] = useState('')

  function submit() {
    // 先就地校验，出错时留在弹窗里并把错误显示出来，而不是把坏数据交给
    // 父组件再等它报错——用户已经填了一屏，弹回去才知道第几行错了很糟。
    const out: { partId: number; usedQty: number; partBatchNo: string; supplier: string }[] = []
    for (const r of rows) {
      if (r.locked) continue // 已跳过的零件不能记投料（完成时也不扣它）
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
        标记「已跳过」的零件完成时不扣库存，不能记录投料。
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
            {lines.map((line, i) => {
              const row = rows[i]
              const tag =
                line.tag === 'skipped' ? (
                  <Badge kind="neutral">已跳过</Badge>
                ) : line.tag === 'temp' ? (
                  <Badge kind="warn">临时</Badge>
                ) : null
              return (
                <tr
                  key={`${line.tag}-${line.partId}-${i}`}
                  style={row.locked ? { opacity: 0.55 } : undefined}
                >
                  <td>
                    <div className="flex items-center gap-1.5">
                      <div style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{line.code}</div>
                      {tag}
                    </div>
                    <div className="text-[11px] text-[var(--color-fg-muted)]">{line.name}</div>
                  </td>
                  <td>
                    <NumberInput
                      value={row.usedQty}
                      disabled={row.locked}
                      onChange={(v) =>
                        setRows((prev) => prev.map((r, j) => (j === i ? { ...r, usedQty: v } : r)))
                      }
                    />
                  </td>
                  <td>
                    <TextInput
                      value={row.partBatchNo}
                      placeholder="批次号"
                      disabled={row.locked}
                      onChange={(v) =>
                        setRows((prev) => prev.map((r, j) => (j === i ? { ...r, partBatchNo: v } : r)))
                      }
                    />
                  </td>
                  <td>
                    <TextInput
                      value={row.supplier}
                      placeholder="供应商"
                      disabled={row.locked}
                      onChange={(v) =>
                        setRows((prev) => prev.map((r, j) => (j === i ? { ...r, supplier: v } : r)))
                      }
                    />
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </Modal>
  )
}

/* ============================================================
 * 零件管理（跳过零件 / 临时零件，两个分页）
 * ============================================================ */

/**
 * 一个入口管本批的零件增减：跳过（BOM 里有但不扣）与临时（BOM 里没有但要扣）。
 *
 * 两个分页的提交语义不同，footer 因此跟着切换：
 *   - 跳过是「勾选 → 保存」的差集提交（沿用原 SkipPartsModal 的行为）；
 *   - 临时零件是逐条立即生效——加一个就要当场看到库存检测结果，攒着批量
 *     提交反而会把"哪一条被拒"搅在一起。
 */
export function PartsManagerModal({
  batchNo,
  planQty,
  items,
  parts,
  skipped,
  temps,
  projection,
  projectionFailed = false,
  busy,
  onSaveSkips,
  onAddTemp,
  onRemoveTemp,
  onClose,
}: {
  batchNo: string
  /** 本批计划数：临时零件按 BOM 公式自动算总量时要用。 */
  planQty: number
  items: BOMItem[]
  /** 全量零件表：临时零件从中搜索挑选。选项里带上现库存，填数量时心里有数。 */
  parts: Part[]
  skipped: ReadonlySet<number>
  temps: ReadonlyArray<TempPart>
  /** 零件级投影：选择器用它算"可用库存"（after = 现有 − 全部在产占用）。 */
  projection?: ReadonlyMap<number, PartProjection>
  /** 投影取不到时把"可用"标成未知，不拿现有库存冒充。 */
  projectionFailed?: boolean
  busy: boolean
  /** 保存跳过差集。成功后父组件关弹窗；失败会把错误写进弹窗顶部。 */
  onSaveSkips: (next: Set<number>) => void
  /** 加/改一个临时零件。失败抛错，由本组件展示；成功后父组件会刷新列表。
      derivation 为空表示用户手填了总量。 */
  onAddTemp: (partId: number, qty: number, derivation?: TempPartDerivation) => Promise<void>
  onRemoveTemp: (partId: number) => Promise<void>
  onClose: () => void
}) {
  const [tab, setTab] = useState<'skip' | 'temp'>('skip')
  const [checked, setChecked] = useState<Set<number>>(() => new Set(skipped))
  const [localErr, setLocalErr] = useState('')
  const [pending, setPending] = useState(false)

  // ---- 临时零件添加表单 ----
  // 零件用文字查询（编码/名称都匹配）；总量默认按 BOM 公式
  // （使用方式 × 单位用量 × 损耗率 × 计划数）自动算，手改后即以手填为准，
  // 再改公式任一项会重新计算。
  const [pickQuery, setPickQuery] = useState('')
  const [pickedPart, setPickedPart] = useState<Part | null>(null)
  const [useMode, setUseMode] = useState(0)
  const [perQty, setPerQty] = useState('1')
  const [lossRate, setLossRate] = useState('0')
  const [totalQty, setTotalQty] = useState(planQty > 0 ? String(planQty) : '')
  const [totalDirty, setTotalDirty] = useState(false)

  const partLabel = useCallback(
    (p: Part) => {
      const available = projectionFailed ? null : (projection?.get(p.id)?.after ?? p.stock_qty)
      const availText = available === null ? '—' : fmtQty(available)
      return `${p.code} ${p.name}（现有 ${fmtQty(p.stock_qty)}，可用 ${availText} ${p.unit}）`
    },
    [projection, projectionFailed],
  )
  const q = pickQuery.trim().toLowerCase()
  const partOptions = useMemo(
    () =>
      parts
        .filter(
          (p) =>
            q === '' ||
            p.code.toLowerCase().includes(q) ||
            (p.name ?? '').toLowerCase().includes(q) ||
            (p.spec ?? '').toLowerCase().includes(q),
        )
        .map(partLabel),
    [parts, q, partLabel],
  )

  /** 把公式项换算成本批总量；数量还没填好时返回空串（添加时会被拦）。 */
  function deriveTotal(mode: number, per: string, loss: string): string {
    const perN = Number(per)
    const lossN = Number(loss)
    if (per === '' || !Number.isFinite(perN) || !Number.isFinite(lossN)) return ''
    return String(bomConsume(planQty, { quantity: perN, loss_rate: lossN, use_mode: mode }))
  }

  function onPickChange(v: string) {
    setPickQuery(v)
    setPickedPart(parts.find((p) => partLabel(p) === v) ?? null)
    setLocalErr('')
  }

  function updateDerivation(patch: { useMode?: number; per?: string; loss?: string }) {
    const mode = patch.useMode ?? useMode
    const per = patch.per ?? perQty
    const loss = patch.loss ?? lossRate
    setUseMode(mode)
    setPerQty(per)
    setLossRate(loss)
    // 公式任一项一动，总量回到"自动"，手填值不再保留
    setTotalDirty(false)
    setTotalQty(deriveTotal(mode, per, loss))
  }

  function resetAddForm() {
    setPickQuery('')
    setPickedPart(null)
    setUseMode(0)
    setPerQty('1')
    setLossRate('0')
    setTotalDirty(false)
    setTotalQty(deriveTotal(0, '1', '0'))
  }

  // 只有标记了「可替换」的零件才能跳过：完成生产时跳过的零件不扣库存，
  // 而不可替换的零件是 BOM 的必需项，不该允许跳过（Fyne batch.go:554）。
  const replaceable = items.filter((b) => b.replaceable === 1)

  async function runTemp(fn: () => Promise<void>) {
    setLocalErr('')
    setPending(true)
    try {
      await fn()
    } catch (e) {
      setLocalErr(e instanceof Error ? e.message : '操作失败')
    } finally {
      setPending(false)
    }
  }

  function addTemp() {
    if (!pickedPart) {
      setLocalErr('请从列表中选择零件（输入编码或名称搜索）')
      return
    }
    // 自动模式下先校验公式项：否则非法损耗率会一路走到后端，报错说的
    // 是看不见的字段，用户对着"本批需要量"发懵。
    if (!totalDirty) {
      const per = parseQuantity(perQty, '单位用量')
      if (!per.ok) {
        setLocalErr(per.msg)
        return
      }
      if (per.value <= 0) {
        setLocalErr('单位用量必须是大于0的有限数值')
        return
      }
      const loss = parseQuantity(lossRate.trim() === '' ? '0' : lossRate, '损耗率')
      if (!loss.ok) {
        setLocalErr(loss.msg)
        return
      }
      if (loss.value < 0 || loss.value > 100) {
        setLocalErr('损耗率必须在 0 到 100 之间')
        return
      }
    }
    const t = parseQuantity(totalQty, '本批需要量')
    if (!t.ok) {
      setLocalErr(t.msg)
      return
    }
    if (t.value <= 0) {
      setLocalErr('本批需要量必须是大于0的有限数值')
      return
    }
    // 手改过总量就不再提交推导依据：存下来的三件套必须能解释总量是怎么来的。
    const derivation: TempPartDerivation | undefined = totalDirty
      ? undefined
      : { useMode, quantity: Number(perQty), lossRate: Number(lossRate) }
    void runTemp(async () => {
      await onAddTemp(pickedPart.id, t.value, derivation)
      resetAddForm()
    })
  }

  /** 列表里的"用法"一行：有推导依据就讲清楚怎么算的，手填就直说。 */
  function deriveText(tp: TempPart): string {
    if (
      tp.use_mode === undefined ||
      tp.use_mode === null ||
      tp.quantity === undefined ||
      tp.quantity === null ||
      tp.loss_rate === undefined ||
      tp.loss_rate === null
    ) {
      return '手填总量'
    }
    const loss = tp.loss_rate > 0 ? `，损耗 ${fmtQty(tp.loss_rate)}%` : ''
    return tp.use_mode === 0
      ? `每台 ${fmtQty(tp.quantity)} ${tp.unit || '个'}${loss}`
      : `每 ${fmtQty(tp.quantity)} 台 1 个${loss}`
  }

  return (
    <Modal
      title={`零件管理 - ${batchNo}`}
      onClose={onClose}
      width={560}
      footer={
        tab === 'skip' ? (
          <>
            <Btn onClick={onClose}>取消</Btn>
            <Btn primary disabled={busy} onClick={() => onSaveSkips(checked)}>
              {busy ? '保存中…' : '保存'}
            </Btn>
          </>
        ) : (
          <Btn primary onClick={onClose}>
            关闭
          </Btn>
        )
      }
    >
      {/* 分页切换：两个 aria 名就是分页语义，测试与读屏都靠它们 */}
      <div className="mb-3 flex gap-1 rounded-[6px] border border-[var(--color-border)] p-0.5" role="tablist">
        {(
          [
            ['skip', `跳过零件${checked.size > 0 ? `（${checked.size}）` : ''}`],
            ['temp', `临时零件${temps.length > 0 ? `（${temps.length}）` : ''}`],
          ] as const
        ).map(([key, label]) => (
          <button
            key={key}
            type="button"
            role="tab"
            aria-selected={tab === key}
            onClick={() => {
              setTab(key)
              setLocalErr('')
            }}
            className={
              'rounded-[5px] px-3 py-1 text-[12px] ' +
              (tab === key
                ? 'bg-[var(--color-primary)] text-white'
                : 'text-[var(--color-fg-muted)] hover:text-[var(--color-fg)]')
            }
          >
            {label}
          </button>
        ))}
      </div>
      <FormError text={localErr} />

      {tab === 'skip' ? (
        <>
          <div className="mb-2.5">
            <div className="text-[11.5px] font-semibold">选择要跳过的零件</div>
            <div className="text-[11px] text-[var(--color-fg-muted)]">
              勾选后，完成生产时将不扣除该零件库存，且不能记录投料
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
        </>
      ) : (
        <>
          <div className="mb-2.5">
            <div className="text-[11.5px] font-semibold">本批临时零件</div>
            <div className="text-[11px] text-[var(--color-fg-muted)]">
              不在产品 BOM 里、但这一批实际要用的料。添加时检查库存；完成生产时按总量整笔扣除。
            </div>
          </div>
          {/*
            添加区放在列表上方：零件搜索的下拉向下弹开（max-h 180px），
            放在弹窗底部会被 overflow-hidden 的弹窗边缘裁掉。
          */}
          <div className="mb-2.5 rounded-[6px] border border-[var(--color-border-subtle)] bg-[var(--color-canvas)] px-2.5 py-2">
            <div className="flex items-end gap-2">
              <div className="min-w-0 flex-1">
                <label className="mb-0.5 block text-[11px] text-[var(--color-fg-muted)]">
                  零件（编码/名称搜索）
                </label>
                <ComboInput
                  aria-label="临时零件"
                  value={pickQuery}
                  onChange={onPickChange}
                  options={partOptions}
                  placeholder="输入编码或名称搜索"
                  allowCustomValue={false}
                />
              </div>
              <Btn disabled={pending} onClick={addTemp}>
                {pending ? '添加中…' : '添加'}
              </Btn>
            </div>
            {q !== '' && partOptions.length === 0 ? (
              <div className="mt-1 text-[10px] text-[var(--color-warn)]">没有匹配的零件</div>
            ) : null}
            <div className="mt-1.5 flex items-end gap-2">
              <div style={{ width: 108 }}>
                <label className="mb-0.5 block text-[11px] text-[var(--color-fg-muted)]">使用方式</label>
                <select
                  aria-label="使用方式"
                  value={useMode}
                  onChange={(e) => updateDerivation({ useMode: Number(e.target.value) })}
                  className={selCls}
                >
                  <option value={0}>每台用N个</option>
                  <option value={1}>每 M 台 1 个</option>
                </select>
              </div>
              <div style={{ width: 74 }}>
                <label className="mb-0.5 block text-[11px] text-[var(--color-fg-muted)]">
                  {useMode === 0 ? '单位用量' : 'M（台）'}
                </label>
                <TextInput
                  value={perQty}
                  placeholder={useMode === 0 ? '个数' : '台数'}
                  onChange={(v) => updateDerivation({ per: v })}
                />
              </div>
              <div style={{ width: 74 }}>
                <label className="mb-0.5 block text-[11px] text-[var(--color-fg-muted)]">损耗率%</label>
                <TextInput value={lossRate} placeholder="0" onChange={(v) => updateDerivation({ loss: v })} />
              </div>
              <div className="min-w-0 flex-1">
                <label className="mb-0.5 block text-[11px] text-[var(--color-fg-muted)]">本批需要量</label>
                <TextInput
                  value={totalQty}
                  placeholder="自动"
                  mono
                  onChange={(v) => {
                    setTotalQty(v)
                    setTotalDirty(true)
                  }}
                />
                <div className="mt-0.5 text-[10px] leading-tight text-[var(--color-fg-subtle)]">
                  自动按公式计算；直接修改即手填
                </div>
              </div>
            </div>
            <div className="mt-1 text-[10px] leading-tight text-[var(--color-fg-subtle)]">
              可用 = 现有 − 全部在产批次的占用；添加/移除后会立即重算。
              {projectionFailed ? (
                <span className="ml-1 text-[var(--color-warn)]">
                  在产占用加载失败，可用库存暂不可用；添加时仍由服务端校验库存。
                </span>
              ) : null}
            </div>
          </div>
          <div className="max-h-[220px] overflow-auto rounded-[6px] border border-[var(--color-border-subtle)]">
            <table className="data-table is-narrow is-compact">
              <colgroup>
                <col style={{ width: 104 }} />
                <col />
                <col style={{ width: 78 }} />
                <col style={{ width: 56 }} />
                <col style={{ width: 52 }} />
              </colgroup>
              <thead>
                <tr>
                  <th>编码</th>
                  <th>名称</th>
                  <th className="num">本批需要量</th>
                  <th className="mid">单位</th>
                  <th className="mid">操作</th>
                </tr>
              </thead>
              <tbody>
                {temps.map((tp) => (
                  <tr key={tp.part_id}>
                    <td className="tree-inline" style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }} title={tp.code}>
                      {tp.code}
                    </td>
                    <td title={tp.name}>
                      <div className="tree-inline">{tp.name}</div>
                      <div className="text-[11px] text-[var(--color-fg-muted)]">{deriveText(tp)}</div>
                    </td>
                    <td className="num">{fmtQty(tp.qty)}</td>
                    <td className="mid">{tp.unit || '—'}</td>
                    <td className="mid">
                      <button
                        type="button"
                        className="text-[var(--color-danger)] disabled:opacity-50"
                        disabled={pending}
                        onClick={() => void runTemp(() => onRemoveTemp(tp.part_id))}
                      >
                        移除
                      </button>
                    </td>
                  </tr>
                ))}
                {temps.length === 0 ? (
                  <tr>
                    <td colSpan={5} className="py-5 text-center text-[12px] text-[var(--color-fg-subtle)]">
                      还没有临时零件
                    </td>
                  </tr>
                ) : null}
              </tbody>
            </table>
          </div>
        </>
      )}
    </Modal>
  )
}