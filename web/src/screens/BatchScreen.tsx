import { useEffect, useMemo, useState } from 'react'
import { Badge, Btn, ScreenError, classifyLoadError, fmtQty, shortTime } from '../components/ui'
import { ConfirmDialog } from '../components/form'
import { usePermissions } from '../lib/api'
import { canWrite } from '../lib/permissions'
import { TraceQuery } from './traceQuery'
import { parts as partsApi, products as productsApi, bom as bomApi } from '../lib/catalog'
import type { Part, PartInbound, Product } from '../lib/catalog'
import {
  BATCH_STATUS,
  batches,
  canChangeStatus,
  canManageParts,
  findShortages,
  statusKind,
  statusText,
  traces,
  type Batch,
  type BatchTrace,
  type TempPart,
  type TempPartDerivation,
} from '../lib/batch'
import {
  CreateBatchModal,
  type NewBatchInput,
  PartsManagerModal,
  RecordTraceModal,
  ShortageModal,
  shortagesOf,
} from './batchForms'
import {
  projection as projectionApi,
  RESERVE_TEXT,
  type BatchReserve,
  type PartProjection,
} from '../lib/projection'

/**
 * 批次追溯。
 *
 * 上下两栏，对应 Fyne 的垂直分屏（batch.go:145-157）：上面批次列表，
 * 下面投料追溯。追溯只在点「查看选中批次的追溯」时才加载，不是自动的——
 * Fyne 也不自动（batch.go:392-403），而且切批次不重新加载的话，用户很容易
 * 把上一个批次的投料当成本批的看。这里改成选中即加载并清空，避免这个坑。
 *
 * 写权限：ModuleBatch 只有 admin 和 production 拿得到 write，仓库员与访客
 * 只有 read（Fyne auth.go:87 同）。所以「新建批次」到「撤销」六个按钮全部
 * 按 canWrite('batch') 隐藏，而不是置灰——置灰会让人反复点。
 */

export function BatchScreen() {
  const [queryOpen, setQueryOpen] = useState(false)
  const [list, setList] = useState<Batch[] | null>(null)
  const [products, setProducts] = useState<Product[]>([])
  const [parts, setParts] = useState<Part[]>([])
  // 按批次的备料结论（来自投影）：只用于列表角标，取不到就不显示。
  const [reserve, setReserve] = useState<Map<number, BatchReserve>>(new Map())
  const [error, setError] = useState<{ kind: 'forbidden' | 'server'; text: string } | null>(null)

  const [sel, setSel] = useState<number | null>(null)
  const [trace, setTrace] = useState<BatchTrace[] | null>(null)
  const [traceBatch, setTraceBatch] = useState<number | null>(null)

  const [dialog, setDialog] = useState<
    | { kind: 'create' }
    | { kind: 'trace' }
    | { kind: 'parts' }
    | { kind: 'status'; to: number }
    | { kind: 'revoke' }
    | null
  >(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [pendingShortages, setPendingShortages] = useState<{ title: string; lines: string[] } | null>(null)
  const [notice, setNotice] = useState('')

  const { modules } = usePermissions()
  const canEdit = canWrite(modules, 'batch')

  const selected = useMemo(() => list?.find((b) => b.id === sel) ?? null, [list, sel])

  async function reload() {
    setList(await batches.list())
    // 状态一变在产占用就变，备料角标与临时零件选择器的"可用"都必须重取。
    const proj = await loadProjection()
    applyProjection(proj)
  }

  /**
   * 投影同时供两处使用：批次备料角标，以及临时零件选择器里的"可用库存"。
   * 失败不抛错——列表本身不该因为预测挂了打不开；调用方用 ok 决定是否
   * 把"可用"标成未知，绝不拿 0 冒充。
   */
  async function loadProjection(): Promise<{
    parts: Map<number, PartProjection>
    batches: BatchReserve[]
    ok: boolean
  }> {
    try {
      const res = await projectionApi.get()
      return {
        parts: new Map(res.parts.map((p) => [p.part_id, p])),
        batches: res.batches,
        ok: true,
      }
    } catch {
      return { parts: new Map(), batches: [], ok: false }
    }
  }

  function applyProjection(proj: Awaited<ReturnType<typeof loadProjection>>) {
    setPartsProj(proj.parts)
    setPartsProjFailed(!proj.ok)
    // 取不到批次结论时保留角标为空（—），不猜"充足"。
    setReserve(new Map(proj.batches.map((b) => [b.batch_id, b])))
  }

  useEffect(() => {
    let alive = true
    Promise.all([batches.list(), productsApi.list(), partsApi.list()])
      .then(([bs, ps, pts]) => {
        if (!alive) return
        setList(bs)
        setProducts(ps)
        setParts(pts)
        setError(null)
        loadProjection().then((proj) => {
          if (alive) applyProjection(proj)
        })
      })
      .catch((e) => {
        if (alive) setError(classifyLoadError(e, '批次'))
      })
    return () => {
      alive = false
    }
  }, [])

  // 选中即加载追溯。切批次时先清空，避免看到上一批的数据。
  useEffect(() => {
    if (sel === null) {
      setTrace(null)
      setTraceBatch(null)
      return
    }
    let alive = true
    setTrace(null)
    traces
      .byBatch(sel)
      .then((rows) => {
        if (alive) {
          setTrace(rows)
          setTraceBatch(sel)
        }
      })
      .catch(() => undefined)
    return () => {
      alive = false
    }
  }, [sel])

  function open(kind: NonNullable<typeof dialog>) {
    setErr('')
    // 上一次操作的提示（如"已撤销的批次不能更改状态"）不该跟着新操作一直挂着。
    setNotice('')
    setDialog(kind)
  }

  function needSelection(): boolean {
    if (selected) return true
    setNotice('请先选择一行批次')
    return false
  }

  /* ---------- 新建 ---------- */

  async function createBatch(
    input: NewBatchInput,
    shortages: ReturnType<typeof shortagesOf>,
  ) {
    if (shortages.length > 0) {
      // Fyne batch.go:363-368：缺料直接中止创建，不提交。
      setPendingShortages({
        title: '库存不足',
        lines: shortages.map((s) => `${s.name} [${s.code}]: 需要 ${s.needed.toFixed(2)}，当前 ${s.have.toFixed(2)}`),
      })
      return
    }
    setBusy(true)
    try {
      // status 恒为 0：CreateBatch 不校验 status（service.go:470-504），
      // 传 2 能造出一个「已完成」但没有冻结消耗记录的批次，之后撤销不了。
      await batches.create({
        batch_no: input.batchNo,
        product_id: input.product.id,
        plan_qty: input.planQty,
        status: BATCH_STATUS.pending,
        customer: input.customer.trim() || undefined,
      })
      setDialog(null)
      await reload()
    } catch (e) {
      setErr(e instanceof Error ? e.message : '创建批次失败')
    } finally {
      setBusy(false)
    }
  }

  /* ---------- 状态变更 ---------- */

  async function doStatus(to: number) {
    if (!selected) return
    const guard = canChangeStatus(selected, to)
    if (!guard.ok) {
      setDialog(null)
      setNotice(guard.msg)
      return
    }

    // 完成生产前在界面再查一次缺料。
    //
    // 服务端现在也会拒（缺料即中止，与建批次同语义），但这里仍要查：服务端
    // 拒绝发生在事务里、错误信息是纯文本拼接，而界面能列出全部缺料项、格式
    // 对齐，用户一次看清要补什么。界面这一层不是安全边界，是提示层。
    if (to === BATCH_STATUS.done) {
      const [bom, skipped] = await Promise.all([
        bomApi.list(selected.product_id),
        batches.skipped(selected.id),
      ])
      const short = findShortages(selected.plan_qty, bom, parts, skipped)
      if (short.length > 0) {
        setDialog(null)
        setPendingShortages({
          title: '库存不足',
          lines: short.map((s) => `${s.name} [${s.code}]: 需要 ${s.needed.toFixed(2)}，当前 ${s.have.toFixed(2)}`),
        })
        return
      }
    }

    setBusy(true)
    try {
      await batches.setStatus(selected.id, to)
      setDialog(null)
      await reload()
    } catch (e) {
      setErr(e instanceof Error ? e.message : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  async function doRevoke() {
    if (!selected) return
    setBusy(true)
    try {
      await batches.revoke(selected.id)
      setDialog(null)
      setNotice(`批次 ${selected.batch_no} 已撤销`)
      await reload()
    } catch (e) {
      setDialog(null)
      setNotice(e instanceof Error ? e.message : '撤销失败')
    } finally {
      setBusy(false)
    }
  }

  /* ---------- 投料 ---------- */

  async function recordTraces(
    rows: {
      partId: number
      usedQty: number
      allocations: { inbound_id: number; qty: number }[]
    }[],
  ) {
    if (!selected) return
    if (rows.length === 0) {
      // 全空 = 什么都没填。Fyne 这里直接静默关掉（Fyne batch.go:480），
      // 用户会以为保存成功了。明确说一句。
      setDialog(null)
      setNotice('没有填写任何投料数量，未保存')
      return
    }
    setBusy(true)
    setErr('')
    try {
      await traces.recordBatch(
        rows.map((r) => ({
          batch_id: selected.id,
          part_id: r.partId,
          used_qty: r.usedQty,
          allocations: r.allocations,
        })),
      )
      setDialog(null)
      setTrace(await traces.byBatch(selected.id))
      setNotice(`已保存 ${rows.length} 个零件的投料信息（重复记录是覆盖，不会追加）`)
    } catch (e) {
      setErr(e instanceof Error ? e.message : '批量记录投料失败')
    } finally {
      setBusy(false)
    }
  }

  /* ---------- 零件管理（跳过 / 临时） ---------- */

  const [partsBom, setPartsBom] = useState<Awaited<ReturnType<typeof bomApi.list>> | null>(null)
  const [partsSkipped, setPartsSkipped] = useState<Set<number>>(new Set())
  const [partsTemps, setPartsTemps] = useState<TempPart[]>([])
  // 选择器要同时显示现有与可用库存；可用 = 投影里的 after（全部在产完成后）。
  const [partsProj, setPartsProj] = useState<Map<number, PartProjection>>(new Map())
  const [partsProjFailed, setPartsProjFailed] = useState(false)

  async function openPartsManager() {
    if (!selected) return
    const guard = canManageParts(selected)
    if (!guard.ok) {
      setNotice(guard.msg)
      return
    }
    setBusy(true)
    try {
      const [bom, already, temps, proj] = await Promise.all([
        bomApi.list(selected.product_id),
        batches.skipped(selected.id),
        batches.tempParts(selected.id),
        loadProjection(),
      ])
      setPartsBom(bom)
      setPartsSkipped(new Set(already))
      setPartsTemps(temps)
      applyProjection(proj)
      setErr('')
      setDialog({ kind: 'parts' })
    } catch (e) {
      setNotice(e instanceof Error ? e.message : '查询零件数据失败')
    } finally {
      setBusy(false)
    }
  }

  async function saveSkips(next: Set<number>) {
    if (!selected) return
    setBusy(true)
    try {
      // 只提交差集：API 对重复新增/删除缺失都是幂等返回 200，逐条全量提交
      // 会为 N 个零件发 2N 个请求，其中大部分什么都没做。
      const toAdd = [...next].filter((id) => !partsSkipped.has(id))
      const toRemove = [...partsSkipped].filter((id) => !next.has(id))
      for (const partId of toAdd) await batches.addSkip(selected.id, partId)
      for (const partId of toRemove) await batches.removeSkip(selected.id, partId)
      // 跳过会改本批占用，可用库存与备料角标都跟着变。
      await reload()
      setDialog(null)
      setNotice(`已跳过 ${toAdd.length} 个零件，取消跳过 ${toRemove.length} 个零件`)
    } catch (e) {
      setErr(e instanceof Error ? e.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  async function addTemp(partId: number, qty: number, derivation?: TempPartDerivation) {
    if (!selected) return
    // 不走全局 busy：添加是弹窗内的连续小操作，锁整个屏反而打断节奏。
    await batches.addTempPart(selected.id, partId, qty, derivation)
    await refreshTempParts()
  }

  async function removeTemp(partId: number) {
    if (!selected) return
    await batches.removeTempPart(selected.id, partId)
    await refreshTempParts()
  }

  /** 临时零件一变，占用就变：列表与"可用库存"必须一起刷新，否则显示会停在旧值。 */
  async function refreshTempParts() {
    if (!selected) return
    // 后端是 upsert：重加同零件=改数量，直接用返回后的最新列表刷新。
    const [temps, proj] = await Promise.all([batches.tempParts(selected.id), loadProjection()])
    setPartsTemps(temps)
    applyProjection(proj)
  }

  /* ---------- 追溯 ---------- */

  const [traceBom, setTraceBom] = useState<Awaited<ReturnType<typeof bomApi.list>> | null>(null)
  // 跳过与临时零件随弹窗打开时一起取：跳过的不给投，临时的加一行，
  // 「零件管理」里刚改过的内容在这里立刻可见。
  const [traceSkipped, setTraceSkipped] = useState<Set<number>>(new Set())
  const [traceTemps, setTraceTemps] = useState<TempPart[]>([])
  // 每个零件的入库批次（带在产占用）与已保存的投料记录：
  // 弹窗要按真实批次号排顺序，也要回显上一次保存的内容（改而不是重填）。
  const [traceInbounds, setTraceInbounds] = useState<Map<number, PartInbound[]>>(new Map())
  const [traceSaved, setTraceSaved] = useState<Map<number, BatchTrace>>(new Map())

  async function openTrace() {
    if (!selected) return
    setBusy(true)
    try {
      const [bom, skipped, temps, saved] = await Promise.all([
        bomApi.list(selected.product_id),
        batches.skipped(selected.id),
        batches.tempParts(selected.id),
        traces.byBatch(selected.id),
      ])
      const ids = new Set<number>()
      for (const b of bom) ids.add(b.part_id)
      for (const t of temps) ids.add(t.part_id)
      for (const t of saved) ids.add(t.part_id)
      // 一个零件一次请求：本地桌面应用，零件数不多；失败按"没有批次"降级，
      // 不因为某一条取不到就把整个记录投料挡住。
      const lists = await Promise.all(
        [...ids].map(async (id) => {
          try {
            return [id, await partsApi.inbounds(id)] as const
          } catch {
            return [id, [] as PartInbound[]] as const
          }
        }),
      )
      setTraceBom(bom)
      setTraceSkipped(new Set(skipped))
      setTraceTemps(temps)
      setTraceInbounds(new Map(lists))
      setTraceSaved(new Map(saved.map((t) => [t.part_id, t])))
      setErr('')
      setDialog({ kind: 'trace' })
    } catch (e) {
      setNotice(e instanceof Error ? e.message : '查询BOM失败')
    } finally {
      setBusy(false)
    }
  }

  if (error) return <ScreenError kind={error.kind} text={error.text} />

  return (
    <>
      <div className="flex items-center gap-2.5">
        <h1 className="text-[15px] font-bold">批次追溯</h1>
        <Btn onClick={() => setQueryOpen(true)}>双向查询</Btn>
        <div className="ml-auto flex gap-2">
          {canEdit ? (
            <>
              <Btn primary onClick={() => open({ kind: 'create' })}>
                新建批次
              </Btn>
              <Btn disabled={!selected || busy} onClick={() => void openPartsManager()}>
                零件管理
              </Btn>
              <Btn disabled={!selected || busy} onClick={() => void openTrace()}>
                记录投料
              </Btn>
              <Btn
                disabled={!selected || busy}
                onClick={() => {
                  if (!needSelection()) return
                  open({ kind: 'status', to: BATCH_STATUS.running })
                }}
              >
                开始生产
              </Btn>
              <Btn
                disabled={!selected || busy}
                onClick={() => {
                  if (!needSelection()) return
                  open({ kind: 'status', to: BATCH_STATUS.done })
                }}
              >
                完成生产
              </Btn>
              <Btn
                danger
                disabled={!selected || busy}
                onClick={() => {
                  if (!needSelection()) return
                  open({ kind: 'revoke' })
                }}
              >
                撤销
              </Btn>
            </>
          ) : null}
        </div>
      </div>

      {notice && (
        <div
          role="status"
          className="flex items-start gap-2 rounded-[8px] border border-[var(--color-warn)]/30 bg-[var(--color-warn-bg)] px-3 py-2 text-[12px] text-[var(--color-warn)]"
        >
          <span className="min-w-0 flex-1">{notice}</span>
          {/* 提示会一直留在页面上挡住列表，必须能手动关掉。 */}
          <button
            type="button"
            aria-label="关闭提示"
            title="关闭提示"
            onClick={() => setNotice('')}
            className="-mr-1 shrink-0 rounded-[4px] px-1 text-[13px] leading-none hover:opacity-70"
          >
            ✕
          </button>
        </div>
      )}

      {/* 批次列表 */}
      <section className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-[8px] border border-[var(--color-border)] bg-[var(--color-surface)]">
        <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-2.5 py-1.5">
          <span className="text-[12px] font-semibold">生产批次</span>
          <span className="text-[11.5px] text-[var(--color-fg-subtle)]">共 {list?.length ?? 0} 批</span>
        </div>
        <div className="min-h-0 flex-1 overflow-auto">
          <table className="data-table">
            <colgroup>
              <col style={{ width: 46 }} />
              <col style={{ width: 190 }} />
              <col />
              <col style={{ width: 80 }} />
              <col style={{ width: 80 }} />
              <col style={{ width: 90 }} />
              <col style={{ width: 78 }} />
              <col style={{ width: 130 }} />
              <col style={{ width: 100 }} />
            </colgroup>
            <thead>
              <tr>
                <th />
                <th>批次号</th>
                <th>产品</th>
                <th className="num">计划数</th>
                <th className="num">完成数</th>
                <th className="mid">状态</th>
                {/* 备料按"全部在产批次都完成"的口径给结论：只在未完成批次上显示，
                    已完成/已撤销的消耗与库存早已结清，再标"不足"是误导。 */}
                <th className="mid" title="按全部在产批次完成后推算的备料情况">
                  备料
                </th>
                <th>客户</th>
                <th>创建时间</th>
              </tr>
            </thead>
            <tbody>
              {(list ?? []).map((b) => (
                <tr
                  key={b.id}
                  data-selected={sel === b.id}
                  onClick={() => setSel(sel === b.id ? null : b.id)}
                >
                  <td className="mid">
                    <input
                      type="checkbox"
                      checked={sel === b.id}
                      onChange={() => setSel(sel === b.id ? null : b.id)}
                      aria-label={`选择 ${b.batch_no}`}
                    />
                  </td>
                  <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{b.batch_no}</td>
                  <td>
                    {b.product_name ?? ''} <span className="text-[var(--color-fg-muted)]">[{b.product_code ?? ''}]</span>
                  </td>
                  <td className="num">{b.plan_qty}</td>
                  <td className="num">{b.produced_qty}</td>
                  <td className="mid">
                    <Badge kind={statusKind(b.status)}>{statusText(b.status)}</Badge>
                  </td>
                  <td className="mid">
                    <ReserveBadge reserve={reserve.get(b.id)} status={b.status} />
                  </td>
                  <td>{b.customer ?? ''}</td>
                  <td style={{ color: 'var(--color-fg-muted)' }}>{shortTime(b.created_at)}</td>
                </tr>
              ))}
              {(list ?? []).length === 0 && (
                <tr>
                  <td colSpan={9} className="py-8 text-center text-[12px] text-[var(--color-fg-subtle)]">
                    还没有批次，点「新建批次」开始
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      {queryOpen ? <TraceQuery batches={list ?? []} parts={parts} onClose={() => setQueryOpen(false)} /> : null}
      {/* 投料追溯 */}
      <section className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-[8px] border border-[var(--color-border)] bg-[var(--color-surface)]">
        <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-2.5 py-1.5">
          <span className="text-[12px] font-semibold">投料追溯记录</span>
          <span className="text-[11.5px] text-[var(--color-fg-subtle)]">
            {sel === null ? '请先选择批次' : traceBatch === sel ? `共 ${(trace ?? []).length} 条` : '加载中…'}
          </span>
        </div>
        <div className="min-h-0 flex-1 overflow-auto">
          <table className="data-table is-compact">
            <colgroup>
              {/* 比例按内容定：编码/ID 定宽，名称给中等宽度，批次分配最长
                  （RK-日期-序号 × 数量），供应商留得下常见厂名。 */}
              <col style={{ width: 60 }} />
              <col style={{ width: 110 }} />
              <col style={{ width: 220 }} />
              <col className="num" style={{ width: 84 }} />
              <col style={{ width: 300 }} />
              <col style={{ width: 180 }} />
            </colgroup>
            <thead>
              <tr>
                <th className="num">零件ID</th>
                <th>零件编码</th>
                <th>零件名称</th>
                <th className="num">用量</th>
                <th>批次分配（按顺序扣）</th>
                <th>供应商</th>
              </tr>
            </thead>
            <tbody>
              {(trace ?? []).map((t) => (
                <tr key={t.id}>
                  <td className="num" style={{ color: 'var(--color-fg-subtle)' }}>
                    {t.part_id}
                  </td>
                  <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{partById(t.part_id)?.code ?? '—'}</td>
                  <td className="tree-inline" title={partById(t.part_id)?.name ?? ''}>
                    {partById(t.part_id)?.name ?? '—'}
                  </td>
                  {/* Fyne 用 %.2f 固定两位 */}
                  <td className="num">{t.used_qty.toFixed(2)}</td>
                  <td>
                    {t.allocations && t.allocations.length > 0 ? (
                      <div className="flex flex-col gap-0.5">
                        {t.allocations.map((a) => (
                          <span key={a.inbound_id} style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>
                            {a.batch_no} × {fmtQty(a.qty)}
                          </span>
                        ))}
                      </div>
                    ) : (
                      // v18 之前的记录只有自由文本批次号，原样显示，不伪造分配。
                      (t.part_batch_no ?? '')
                    )}
                  </td>
                  <td className="tree-inline" title={t.supplier ?? ''}>
                    {t.supplier ?? ''}
                  </td>
                </tr>
              ))}
              {sel !== null && (trace ?? []).length === 0 && traceBatch === sel && (
                <tr>
                  <td colSpan={6} className="py-6 text-center text-[12px] text-[var(--color-fg-subtle)]">
                    该批次还没有投料记录
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      {/* ---------- 弹窗 ---------- */}

      {dialog?.kind === 'create' ? (
        <CreateBatchModal
          products={products}
          parts={parts}
          onSubmit={createBatch}
          onClose={() => setDialog(null)}
        />
      ) : null}

      {dialog?.kind === 'trace' && selected && traceBom ? (
        <RecordTraceModal
          batchNo={selected.batch_no}
          planQty={selected.plan_qty}
          items={traceBom}
          skipped={traceSkipped}
          temps={traceTemps}
          inbounds={traceInbounds}
          existing={traceSaved}
          busy={busy}
          err={err}
          onSubmit={recordTraces}
          onClose={() => setDialog(null)}
        />
      ) : null}

      {dialog?.kind === 'parts' && selected && partsBom ? (
        <PartsManagerModal
          batchNo={selected.batch_no}
          planQty={selected.plan_qty}
          items={partsBom}
          parts={parts}
          skipped={partsSkipped}
          temps={partsTemps}
          projection={partsProj}
          projectionFailed={partsProjFailed}
          busy={busy}
          onSaveSkips={(next) => void saveSkips(next)}
          onAddTemp={addTemp}
          onRemoveTemp={removeTemp}
          onClose={() => setDialog(null)}
        />
      ) : null}

      {dialog?.kind === 'status' && selected ? (
        <ConfirmDialog
          title="确认"
          message={`确定将批次 ${selected.batch_no} 标记为【${statusText(dialog.to)}】？`}
          confirmLabel="确定"
          busy={busy}
          error={err}
          onConfirm={() => void doStatus(dialog.to)}
          onCancel={() => setDialog(null)}
        />
      ) : null}

      {dialog?.kind === 'revoke' && selected ? (
        <ConfirmDialog
          title="确认撤销"
          danger
          error={err}
          busy={busy}
          message={
            <>
              确定撤销批次 {selected.batch_no}（当前状态：{statusText(selected.status)}）？
              <div className="mt-2">
                {selected.status === BATCH_STATUS.done
                  ? '该批次已完成生产，撤销后将回退已扣除的零件库存。'
                  : '撤销后状态将标记为「已撤销」，数据保留。'}
              </div>
            </>
          }
          onConfirm={() => void doRevoke()}
          onCancel={() => setDialog(null)}
        />
      ) : null}

      {pendingShortages && (
        <ShortageModal lines={pendingShortages.lines} onCancel={() => setPendingShortages(null)} />
      )}
    </>
  )

  function partById(id: number): Part | undefined {
    return parts.find((p) => p.id === id)
  }
}

/**
 * 批次行的「备料」角标。
 *
 * 已完成/已撤销不显示：前者的库存已经扣过，后者永远不会扣，
 * 给它们标"不足"只会制造噪声。未完成但没有投影数据时也留空——
 * 拿不到就什么都不说，比猜一个"充足"安全。
 */
function ReserveBadge({ reserve, status }: { reserve?: BatchReserve; status: number }) {
  if (!reserve || status === BATCH_STATUS.done || status === BATCH_STATUS.revoked) {
    return <span className="text-[var(--color-fg-subtle)]">—</span>
  }
  const kind = reserve.level === 'short' ? 'danger' : reserve.level === 'low' ? 'warn' : 'ok'
  const detail = (reserve.parts ?? [])
    .map((p) => `${p.name} [${p.code}]：完成后可用 ${p.after}`)
    .join('；')
  return (
    <Badge kind={kind} title={detail || undefined}>
      {RESERVE_TEXT[reserve.level]}
    </Badge>
  )
}
