import { useEffect, useMemo, useState } from 'react'
import { Badge, Btn, ScreenError, classifyLoadError, shortTime } from '../components/ui'
import { ConfirmDialog } from '../components/form'
import { usePermissions } from '../lib/api'
import { canWrite } from '../lib/permissions'
import { parts as partsApi, products as productsApi, bom as bomApi } from '../lib/catalog'
import type { Part, Product } from '../lib/catalog'
import {
  BATCH_STATUS,
  batches,
  canChangeStatus,
  canEditSkips,
  findShortages,
  statusKind,
  statusText,
  traces,
  type Batch,
  type BatchTrace,
} from '../lib/batch'
import {
  CreateBatchModal,
  type NewBatchInput,
  RecordTraceModal,
  ShortageModal,
  SkipPartsModal,
  shortagesOf,
} from './batchForms'

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
  const [list, setList] = useState<Batch[] | null>(null)
  const [products, setProducts] = useState<Product[]>([])
  const [parts, setParts] = useState<Part[]>([])
  const [error, setError] = useState<{ kind: 'forbidden' | 'server'; text: string } | null>(null)

  const [sel, setSel] = useState<number | null>(null)
  const [trace, setTrace] = useState<BatchTrace[] | null>(null)
  const [traceBatch, setTraceBatch] = useState<number | null>(null)

  const [dialog, setDialog] = useState<
    | { kind: 'create' }
    | { kind: 'trace' }
    | { kind: 'skip' }
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

    // 完成生产会真的扣库存，而服务端在库存不足时是静默夹取（只扣到 0、
    // 批次照常完成），所以必须在这里再查一次。Fyne 只在建批次时查，
    // 而库存会变，旧批次被完成时早就缺料了。
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
    rows: { partId: number; usedQty: number; partBatchNo: string; supplier: string }[],
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
          part_batch_no: r.partBatchNo || undefined,
          supplier: r.supplier || undefined,
        })),
      )
      setDialog(null)
      setTrace(await traces.byBatch(selected.id))
      setNotice(`成功记录 ${rows.length} 个零件的投料信息`)
    } catch (e) {
      setErr(e instanceof Error ? e.message : '批量记录投料失败')
    } finally {
      setBusy(false)
    }
  }

  /* ---------- 跳过零件 ---------- */

  const [skipBom, setSkipBom] = useState<Awaited<ReturnType<typeof bomApi.list>> | null>(null)
  const [skipAlready, setSkipAlready] = useState<Set<number>>(new Set())

  async function openSkip() {
    if (!selected) return
    const guard = canEditSkips(selected)
    if (!guard.ok) {
      setNotice(guard.msg)
      return
    }
    setBusy(true)
    try {
      const [bom, already] = await Promise.all([
        bomApi.list(selected.product_id),
        batches.skipped(selected.id),
      ])
      if (bom.length === 0) {
        setNotice('该产品没有BOM明细，无需跳过')
        return
      }
      setSkipBom(bom)
      setSkipAlready(new Set(already))
      setErr('')
      setDialog({ kind: 'skip' })
    } catch (e) {
      setNotice(e instanceof Error ? e.message : '查询BOM失败')
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
      const toAdd = [...next].filter((id) => !skipAlready.has(id))
      const toRemove = [...skipAlready].filter((id) => !next.has(id))
      for (const partId of toAdd) await batches.addSkip(selected.id, partId)
      for (const partId of toRemove) await batches.removeSkip(selected.id, partId)
      setDialog(null)
      setNotice(`已跳过 ${toAdd.length} 个零件，取消跳过 ${toRemove.length} 个零件`)
    } catch (e) {
      setErr(e instanceof Error ? e.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  /* ---------- 追溯 ---------- */

  const [traceBom, setTraceBom] = useState<Awaited<ReturnType<typeof bomApi.list>> | null>(null)

  async function openTrace() {
    if (!selected) return
    setBusy(true)
    try {
      setTraceBom(await bomApi.list(selected.product_id))
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
        <div className="ml-auto flex gap-2">
          {canEdit ? (
            <>
              <Btn primary onClick={() => open({ kind: 'create' })}>
                新建批次
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
              <Btn disabled={!selected || busy} onClick={() => void openTrace()}>
                记录投料
              </Btn>
              <Btn disabled={!selected || busy} onClick={() => void openSkip()}>
                跳过零件
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
          className="rounded-[8px] border border-[var(--color-warn)]/30 bg-[var(--color-warn-bg)] px-3 py-2 text-[12px] text-[var(--color-warn)]"
        >
          {notice}
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
                  <td>{b.customer ?? ''}</td>
                  <td style={{ color: 'var(--color-fg-muted)' }}>{shortTime(b.created_at)}</td>
                </tr>
              ))}
              {(list ?? []).length === 0 && (
                <tr>
                  <td colSpan={8} className="py-8 text-center text-[12px] text-[var(--color-fg-subtle)]">
                    还没有批次，点「新建批次」开始
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

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
              <col style={{ width: 70 }} />
              <col style={{ width: 130 }} />
              <col />
              <col className="num" style={{ width: 90 }} />
              <col style={{ width: 150 }} />
              <col style={{ width: 120 }} />
            </colgroup>
            <thead>
              <tr>
                <th className="num">零件ID</th>
                <th>零件编码</th>
                <th>零件名称</th>
                <th className="num">用量</th>
                <th>零件批次</th>
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
                  <td>{partById(t.part_id)?.name ?? '—'}</td>
                  {/* Fyne 用 %.2f 固定两位 */}
                  <td className="num">{t.used_qty.toFixed(2)}</td>
                  <td>{t.part_batch_no ?? ''}</td>
                  <td>{t.supplier ?? ''}</td>
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
          busy={busy}
          err={err}
          onSubmit={recordTraces}
          onClose={() => setDialog(null)}
        />
      ) : null}

      {dialog?.kind === 'skip' && selected && skipBom ? (
        <SkipPartsModal
          batchNo={selected.batch_no}
          items={skipBom}
          already={skipAlready}
          busy={busy}
          onSubmit={saveSkips}
          onClose={() => setDialog(null)}
        />
      ) : null}

      {dialog?.kind === 'status' && selected ? (
        <ConfirmDialog
          title="确认"
          message={`确定将批次 ${selected.batch_no} 标记为【${statusText(dialog.to)}】？`}
          confirmLabel="确定"
          busy={busy}
          onConfirm={() => void doStatus(dialog.to)}
          onCancel={() => setDialog(null)}
        />
      ) : null}

      {dialog?.kind === 'revoke' && selected ? (
        <ConfirmDialog
          title="确认撤销"
          danger
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