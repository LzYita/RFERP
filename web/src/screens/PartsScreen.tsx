import { useEffect, useMemo, useState } from 'react'
import { ApiError, usePermissions } from '../lib/api'
import { canWrite } from '../lib/permissions'
import { isStockAlert, stockLevel, STOCK_LEVEL_TEXT } from '../lib/lowStock'
import { parts as partsApi, type Part } from '../lib/catalog'
import { PartFormModal, emptyPartDraft, partToDraft, validatePartDraft, draftToWrite, type PartDraft } from './partForm'
import { StockModal, BatchStockInModal, type StockMode } from './stockForm'
import { ConfirmDialog, Modal } from '../components/form'
import {
  Badge,
  Btn,
  Field,
  Pagination,
  ScreenError,
  classifyLoadError,
  fmtQty,
  shortTime,
} from '../components/ui'


const DEFAULT_PAGE_SIZE = 20

/** 删除被拒时后端回显的产品（只需要编码与名称）。 */
type BOMBlocker = { code: string; name: string }
/** 删除被拒时后端回显的批次（按批次汇总的投料记录条数）。 */
type BatchBlocker = { batch_no: string; records: number }
/** 两种「不能删」的原因给的建议不一样，所以分开建模，不合并成一个 products 数组。 */
type DeleteBlock =
  | { kind: 'bom'; partCode: string; products: BOMBlocker[] }
  | { kind: 'trace'; partCode: string; batches: BatchBlocker[]; records: number }

/**
 * 「分类」筛选的候选项。
 *
 * 必须从当前数据里现推，而不是用 form 里那份 PART_TYPES 常量：
 * 库里可能存着常量之外的分类（历史数据、导入数据），常量筛不出它们，
 * 用户会以为自己那些零件丢了。Fyne 版 `syncTypeFilter`（ui/part.go:210）
 * 就是按数据重建的，这里照同一套语义。
 *
 * 排序**有意偏离** Fyne：那边用 sort.Strings，中文按码点排，下拉里看起来
 * 是随机的（五金/塑料/电子）。这是 Go 默认排序的副产物，不是设计，
 * 没有可保真的语义；中文界面按拼音排才是用户预期的顺序。
 */
export function typeFilterOptions(rows: readonly Part[]): string[] {
  const seen = new Set<string>()
  for (const p of rows) {
    const t = (p.part_type ?? '').trim()
    if (t !== '') seen.add(t)
  }
  return ['全部', ...[...seen].sort((a, b) => a.localeCompare(b, 'zh'))]
}

/**
 * 失效分类回退。
 *
 * 选中的分类可能在数据变化后消失（删掉最后一个该分类的零件）。Fyne 版在
 * syncTypeFilter 里回退到「全部」（ui/part.go:226）；不回退的话，筛选会停在
 * 一个选项都不存在的值上，列表空白而用户看不出原因。
 *
 * 单独抽出来是为了能在渲染期直接派生，而不是用 effect 去同步这个状态：
 * effect 里 setState 会多一轮渲染，React Compiler 也会因此放弃优化整个组件。
 */
export function effectiveTypeFilter(options: readonly string[], selected: string): string {
  return options.includes(selected) ? selected : '全部'
}

/**
 * 按分类 + 关键字筛选。
 *
 * 关键字范围＝编码/名称/规格/分类/**供应商**五处。Fyne 版这里自相矛盾：输入框占位
 * 写的是「编码/名称/规格」，实际匹配的却是「编码 名称 分类」（ui/part.go:252），
 * 规格根本没进全文。既然两边各覆盖一半，就取并集，并把占位文案写实——
 * 照抄任何一边都会留下「说了能搜却搜不到」的坑。
 *
 * 供应商是 2026-10-13 用户明确要求纳入的：它是常用筛选维度（"这个厂供的件有哪些"），
 * 而 Fyne 版没把它放进全文，等于用户搜供应商名会一无所获却毫无提示。
 * 纳入后不再是对齐 Fyne，而是**有意超出**——记在这里，免得后来人当成疏漏"改回去"。
 *
 * 分类是精确匹配（与 Fyne 一致）：分类是有限枚举，模糊匹配会让「五金」
 * 顺带捞出「五金配件」，筛选结果无法解释。
 */
export function filterParts(
  rows: readonly Part[],
  q: string,
  typeFilter: string,
  lowOnly: boolean,
): Part[] {
  let out = [...rows]
  if (typeFilter !== '全部') {
    out = out.filter((p) => (p.part_type ?? '') === typeFilter)
  }
  const k = q.trim().toLowerCase()
  if (k !== '') {
    out = out.filter((p) =>
      [p.code, p.name, p.spec ?? '', p.part_type ?? '', p.supplier ?? ''].some((f) =>
        f.toLowerCase().includes(k),
      ),
    )
  }
  if (lowOnly) out = out.filter(isStockAlert)
  return out
}

/**
 * 零件列表：Round 1 的「一个真实列表屏」关口。
 *
 * 页结构按中后台通行骨架：页头 → 筛选区 → 批量栏（选中时）→ 表格 → 分页。
 * 密度、圆角、阈值全部来自 index.css 的定稿 token，这里不写副本。
 *
 * 列的取舍（§Round 1.1）：
 *  - 单位并入库存（`1,284 个`），少一列
 *  - 更新时间去年份（`10-01 08:12`），省约 60px；年份由筛选语境承担
 * 这两处是「一屏能读完」的关键，比调阈值有效得多。
 */
export function PartsScreen() {
  const [parts, setParts] = useState<Part[] | null>(null)
  const [error, setError] = useState<{ kind: 'forbidden' | 'server'; text: string } | null>(null)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE)
  const [q, setQ] = useState('')
  const [lowOnly, setLowOnly] = useState(false)
  // 「分类」筛选：Fyne 原版有的维度，迁移时整条丢了（连带列表里的分类列）。
  // 后端为它建了 idx_parts_type，说明这个维度本来就是要用的。
  const [typeFilter, setTypeFilter] = useState('全部')
  // Round 3：编辑弹窗 / 删除确认。零件的写权限里 **admin 和仓库员都有**，
  // 只读角色（viewer、生产员）连行内按钮都不该看到——但前端隐藏不是安全边界，
  // 后端 Catalog.UpdatePart 才是。
  const { modules } = usePermissions()
  const canEdit = canWrite(modules, 'parts')
  const [formOpen, setFormOpen] = useState(false)
  const [editing, setEditing] = useState<Part | null>(null)
  const [draft, setDraft] = useState<PartDraft>(emptyPartDraft())
  const [formErr, setFormErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [deleting, setDeleting] = useState<Part | null>(null)
  // 删除失败（非 BOM 引用）的提示。放列表上方的横幅里：确认框会关掉，
  // 塞进 formErr 就只有表单弹窗才渲染，等于没提示。
  const [deleteErr, setDeleteErr] = useState('')
  // 删除被 BOM 引用拒绝时，把「被哪些产品用了」直接摆出来。
  // 后端回 409 + products；只显示一句「删除失败」等于让用户自己去 BOM 表里翻。
  const [blocked, setBlocked] = useState<DeleteBlock | null>(null)
  // 入库 / 盘点 / 批量。三个都是 Round 3 之前完全不存在的入口 ——
  // 后端端点与权限点早就有了，只差 UI。
  const [stockFor, setStockFor] = useState<Part | null>(null)
  const [stockMode, setStockMode] = useState<StockMode>('in')
  const [batchInOpen, setBatchInOpen] = useState(false)
  const [batchDelList, setBatchDelList] = useState<Part[] | null>(null)

  useEffect(() => {
    let alive = true
    partsApi
      .list()
      .then((rows) => {
        if (alive) {
          setParts(rows)
          setError(null)
        }
      })
      .catch((err) => {
        if (!alive) return
        // 权限拒绝与后端故障要分开：前者是账号问题，后者是系统问题，
        // 混成一句「加载失败」会让用户既不知道该找谁、也不知道该重试。
        setError(classifyLoadError(err, '零件'))
      })
    return () => {
      alive = false
    }
  }, [])

  const typeOptions = useMemo(() => typeFilterOptions(parts ?? []), [parts])
  const activeType = effectiveTypeFilter(typeOptions, typeFilter)
  const filtered = useMemo(
    () => filterParts(parts ?? [], q, activeType, lowOnly),
    [parts, q, activeType, lowOnly],
  )

  const pages = Math.max(1, Math.ceil(filtered.length / pageSize))
  const current = Math.min(page, pages)
  const rows = filtered.slice((current - 1) * pageSize, current * pageSize)

  const allChecked = rows.length > 0 && rows.every((p) => selected.has(p.id))

  function toggleAll() {
    setSelected((prev) => {
      const next = new Set(prev)
      if (allChecked) rows.forEach((p) => next.delete(p.id))
      else rows.forEach((p) => next.add(p.id))
      return next
    })
  }

  function toggleOne(id: number) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function openCreate() {
    setEditing(null)
    setDraft(emptyPartDraft())
    setFormErr('')
    setFormOpen(true)
  }

  function openEdit(p: Part) {
    setEditing(p)
    setDraft(partToDraft(p))
    setFormErr('')
    setFormOpen(true)
  }

  async function reload() {
    const rows = await partsApi.list()
    setParts(rows)
  }

  async function submit() {
    const v = validatePartDraft(draft, editing !== null)
    if (!v.ok) {
      setFormErr(v.msg)
      return
    }
    setBusy(true)
    setFormErr('')
    try {
      const body = draftToWrite(draft, editing !== null, editing ?? undefined)
      if (editing) await partsApi.update(body as never)
      else await partsApi.create(body)
      await reload()
      setFormOpen(false)
    } catch (e) {
      // 直接把后端文案放进表单：唯一键冲突等都已经翻成中文了
      setFormErr(e instanceof Error ? e.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  function openStock(p: Part, mode: StockMode) {
    setStockFor(p)
    setStockMode(mode)
    setFormErr('')
  }

  async function doStock(qty: number) {
    if (!stockFor) return
    setBusy(true)
    setFormErr('')
    try {
      if (stockMode === 'in') await partsApi.stockIn(stockFor.id, qty)
      else await partsApi.adjustStock(stockFor.id, qty)
      setStockFor(null)
      await reload()
    } catch (e) {
      setFormErr(e instanceof Error ? e.message : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  /** 批量入库：对所选每个零件各加 qty。逐个 await，中途失败不吞掉前面的结果。 */
  async function doBatchStockIn(ids: number[], qty: number) {
    setBusy(true)
    setFormErr('')
    try {
      for (const id of ids) {
        await partsApi.stockIn(id, qty)
      }
      setBatchInOpen(false)
      setSelected(new Set())
      await reload()
    } catch (e) {
      // 已成功的部分不会回滚——库存变动本身逐条入审计，没有"整体撤销"这回事。
      // 这里如实说明已经做了一半。
      setFormErr(
        (e instanceof Error ? e.message : '批量入库失败') + '（已完成的零件不会回滚）',
      )
      setBatchInOpen(false)
      await reload()
    } finally {
      setBusy(false)
    }
  }

  async function doBatchDelete() {
    if (!batchDelList) return
    setBusy(true)
    setFormErr('')
    setDeleteErr('')
    try {
      for (const p of batchDelList) {
        try {
          await partsApi.remove(p.id)
        } catch (e) {
          if (showDeleteBlockers(e, p.code)) {
            setBatchDelList(null)
            return
          }
          throw e
        }
      }
      setSelected(new Set())
      setBatchDelList(null)
      await reload()
    } catch (e) {
      setDeleteErr(
        (e instanceof Error ? e.message : '批量删除失败') + '（已删除的不会回滚）',
      )
      setBatchDelList(null)
      await reload()
    } finally {
      setBusy(false)
    }
  }

  async function doDelete() {
    if (!deleting) return
    setBusy(true)
    setDeleteErr('')
    try {
      await partsApi.remove(deleting.id)
      setSelected((prev) => {
        const next = new Set(prev)
        next.delete(deleting.id)
        return next
      })
      setDeleting(null)
      await reload()
    } catch (e) {
      if (showDeleteBlockers(e, deleting.code)) {
        setDeleting(null)
        return
      }
      setDeleteErr(e instanceof Error ? e.message : '删除失败')
      setDeleting(null)
    } finally {
      setBusy(false)
    }
  }

  /**
   * 409 表示「零件被引用，不能删」。两种载荷对应两种建议：
   * - products：BOM 引用，用户去 BOM 管理删条目就能解开；
   * - batches：生产投料记录，删不掉，只能把零件改为「停用」。
   * 其它错误（404、非引用类失败）照旧走错误横幅，不冒充引用提示。
   */
  function showDeleteBlockers(e: unknown, partCode: string): boolean {
    if (!(e instanceof ApiError) || e.status !== 409) return false
    const data = e.data as
      | { products?: BOMBlocker[]; batches?: BatchBlocker[]; records?: number }
      | undefined
    if (Array.isArray(data?.products) && data.products.length > 0) {
      setFormErr('')
      setDeleteErr('')
      setBlocked({ kind: 'bom', partCode, products: data.products })
      return true
    }
    if (Array.isArray(data?.batches) && data.batches.length > 0) {
      const records =
        typeof data.records === 'number'
          ? data.records
          : data.batches.reduce((n, b) => n + b.records, 0)
      setFormErr('')
      setDeleteErr('')
      setBlocked({ kind: 'trace', partCode, batches: data.batches, records })
      return true
    }
    return false
  }

  if (error) return <ScreenError kind={error.kind} text={error.text} />

  return (
    <>
      <div className="flex items-center gap-2.5">
        <h1 className="text-[15px] font-bold">零件管理</h1>
        <div className="ml-auto flex gap-2">
          {canEdit ? <Btn primary onClick={openCreate}>+ 新增零件</Btn> : null}
        </div>
      </div>

      {deleteErr ? (
        <div
          role="alert"
          className="flex items-center gap-2 rounded-[6px] border border-[var(--color-danger)]/30 bg-[var(--color-danger-bg)] px-2.5 py-1.5 text-[12px] text-[var(--color-danger)]"
        >
          <span>{deleteErr}</span>
          <button type="button" className="ml-auto underline" onClick={() => setDeleteErr('')}>
            知道了
          </button>
        </div>
      ) : null}

      {/* 筛选区 */}
      <section className="rounded-[8px] border border-[var(--color-border)] bg-[var(--color-surface)] p-2.5">
        <div className="flex flex-wrap items-end gap-2.5">
          <Field label="编码 / 名称 / 规格 / 分类 / 供应商">
            <input
              value={q}
              onChange={(e) => {
                setQ(e.target.value)
                setPage(1)
              }}
              placeholder="全部"
              className="h-[26px] w-[150px] rounded-[6px] border border-[var(--color-border)] px-2.5 text-[11.5px] outline-none focus:border-[var(--color-primary)]"
            />
          </Field>
          <Field label="分类">
            <select
              value={activeType}
              onChange={(e) => {
                setTypeFilter(e.target.value)
                setPage(1)
              }}
              className="h-[26px] w-[110px] rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-2 text-[11.5px] outline-none focus:border-[var(--color-primary)]"
            >
              {typeOptions.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </Field>
          <Field label="库存">
            <label className="flex h-[26px] items-center gap-1.5 rounded-[6px] border border-[var(--color-border)] px-2.5 text-[11.5px]">
              <input type="checkbox" checked={lowOnly} onChange={(e) => setLowOnly(e.target.checked)} />
              {/* 勾选后列出「零库存 + 跌破预警线」，不只是跌破预警线，
                  所以文案跟着口径改，否则用户会以为漏掉了没货的零件。 */}
              库存预警
            </label>
          </Field>
          <Btn primary onClick={() => setPage(1)}>
            查询
          </Btn>
          <Btn
            onClick={() => {
              setQ('')
              setLowOnly(false)
              setTypeFilter('全部')
              setPage(1)
            }}
          >
            重置
          </Btn>
        </div>
      </section>

      {/* 批量操作栏：选中行才出现（Ant 的 tableAlertRender） */}
      {selected.size > 0 && (
        <div className="flex items-center gap-2.5 rounded-[8px] border border-[var(--color-primary-subtle-border)] bg-[var(--color-primary-subtle)] px-3 py-2 text-[12px]">
          <span>
            已选 <b className="text-[var(--color-primary)]">{selected.size}</b> 项（共{' '}
            {filtered.length} 项）
          </span>
          <button type="button" className="text-[var(--color-primary)]" onClick={() => setSelected(new Set())}>
            取消选择
          </button>
          <div className="ml-auto flex gap-2">
            <Btn primary onClick={() => setBatchInOpen(true)} disabled={selected.size === 0}>
              批量入库
            </Btn>
            <Btn danger onClick={() => setBatchDelList(rows.filter((p) => selected.has(p.id)))}
              disabled={selected.size === 0}>
              批量删除
            </Btn>
          </div>
        </div>
      )}

      {/* 表格 */}
      <section className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-[8px] border border-[var(--color-border)] bg-[var(--color-surface)]">
        <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-2.5 py-1.5">
          <span className="text-[13px] font-semibold">零件管理</span>
          <span className="text-[11.5px] text-[var(--color-fg-subtle)]">共 {filtered.length} 项</span>
        </div>

        <div className="min-h-0 flex-1 overflow-auto">
          <table className="data-table">
            <thead>
              <tr>
                <th className="mid" style={{ width: 34 }}>
                  <input type="checkbox" checked={allChecked} onChange={toggleAll} aria-label="全选本页" />
                </th>
                <th>编码</th>
                <th>名称</th>
                <th>规格</th>
                {/* 分类是 Fyne 版的列，迁移时漏了；它同时是筛选维度，
                    不显示的话用户设了分类也无从核对 */}
                <th>分类</th>
                <th className="num">库存</th>
                <th className="num">预警值</th>
                <th className="mid">状态</th>
                {/* 供应商：Fyne 版有（part.go:80 第 8 列），迁移时漏了。
                    它与「分类」一样是表单里能填、CSV 导出里有、审计 JSON 里有、
                    唯独列表看不见的字段——用户填了无从核对。 */}
                <th>供应商</th>
                <th>更新时间</th>
                <th className="mid col-fix" style={{ width: 120 }}>
                  操作
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((p) => {
                const level = stockLevel(p)
                return (
                  <tr key={p.id} data-selected={selected.has(p.id)}>
                    <td className="mid">
                      <input
                        type="checkbox"
                        checked={selected.has(p.id)}
                        onChange={() => toggleOne(p.id)}
                        aria-label={`选择 ${p.code}`}
                      />
                    </td>
                    <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{p.code}</td>
                    <td>{p.name}</td>
                    <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{p.spec ?? '—'}</td>
                    <td>{p.part_type || '—'}</td>
                    {/* 单位并入库存，省一列 */}
                    <td className="num">
                      {fmtQty(p.stock_qty)} {p.unit}
                    </td>
                    {/* 预警值：与库存并排才看得出差多少。藏在详情里的话，
                        用户只会看到"低库存"两个字，不知道差多少、补多少。 */}
                    <td className="num" style={{ color: 'var(--color-fg-muted)' }}>
                      {fmtQty(p.warn_qty)} {p.unit}
                    </td>
                    <td className="mid">
                      {/* 停用优先：停用件显示灰色「停用」，不再叠加库存档位。
                          零件已经停用、不会补货，把它的库存问题标红只会淹没真正
                          要处理的零件。库存维度在 status===1 时才显示。 */}
                      <Badge
                        kind={
                          p.status !== 1
                            ? 'neutral'
                            : level === 'out'
                              ? 'danger'
                              : level === 'low'
                                ? 'warn'
                                : 'ok'
                        }
                      >
                        {p.status !== 1 ? '停用' : STOCK_LEVEL_TEXT[level]}
                      </Badge>
                    </td>
                    <td style={{ color: 'var(--color-fg-muted)' }}>{p.supplier || '—'}</td>
                    {/* 去年份：年份由筛选/排序语境承担 */}
                    <td style={{ color: 'var(--color-fg-muted)' }}>{shortTime(p.updated_at)}</td>
                    <td className="mid col-fix">
                      {canEdit ? (
                        <>
                          <button
                            className="mr-2 text-[var(--color-primary)]"
                            onClick={() => openEdit(p)}
                          >
                            编辑
                          </button>
                          {/* 库存变动与主数据编辑分开成两个动作：
                              编辑表单里库存是只读的。 */}
                          <button
                            className="mr-2 text-[var(--color-primary)]"
                            onClick={() => openStock(p, 'in')}
                          >
                            入库
                          </button>
                          <button
                            className="mr-2 text-[var(--color-primary)]"
                            onClick={() => openStock(p, 'adjust')}
                          >
                            盘点
                          </button>
                          <button
                            className="text-[var(--color-primary)]"
                            onClick={() => setDeleting(p)}
                          >
                            删除
                          </button>
                        </>
                      ) : (
                        <span className="text-[var(--color-fg-subtle)]">—</span>
                      )}
                    </td>
                  </tr>
                )
              })}
              {rows.length === 0 && (
                <tr>
                  <td colSpan={11} className="py-8 text-center text-[12px] text-[var(--color-fg-subtle)]">
                    没有符合条件的零件
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        {/* 分页 */}
        <Pagination
        total={filtered.length}
        shown={rows.length}
        page={current}
        pages={pages}
        pageSize={pageSize}
        onPage={setPage}
        onPageSize={(n) => {
          setPageSize(n)
          setPage(1)
        }}
      />
      </section>

      {formOpen ? (
        <PartFormModal
          draft={draft}
          setDraft={setDraft}
          editing={editing !== null}
          orig={editing ?? undefined}
          busy={busy}
          err={formErr}
          onSubmit={submit}
          onClose={() => setFormOpen(false)}
        />
      ) : null}

      {stockFor ? (
        <StockModal
          part={stockFor}
          mode={stockMode}
          busy={busy}
          err={formErr}
          onSubmit={doStock}
          onClose={() => setStockFor(null)}
        />
      ) : null}

      {batchInOpen ? (
        <BatchStockInModal
          items={rows.filter((p) => selected.has(p.id))}
          busy={busy}
          err={formErr}
          onSubmit={doBatchStockIn}
          onClose={() => setBatchInOpen(false)}
        />
      ) : null}

      {batchDelList ? (
        <ConfirmDialog
          title="确认批量删除"
          danger
          busy={busy}
          message={
            <>
              确定删除选中的 <b>{batchDelList.length}</b> 个零件？
              <div className="mt-1.5 text-[11px] text-[var(--color-fg-muted)]">
                若其中任一被 BOM 引用，整个操作会中断，且已删除的不会回滚。
              </div>
            </>
          }
          onConfirm={doBatchDelete}
          onCancel={() => setBatchDelList(null)}
        />
      ) : null}

      {deleting ? (
        <ConfirmDialog
          title="确认删除"
          danger
          busy={busy}
          message={
            <>
              确定删除零件 <b>{deleting.code}</b>（{deleting.name}）？
              <div className="mt-1.5 text-[11px] text-[var(--color-fg-muted)]">
                若该零件已被 BOM 引用，删除会被拒绝。
              </div>
            </>
          }
          onConfirm={doDelete}
          onCancel={() => setDeleting(null)}
        />
      ) : null}

      {blocked ? (
        <Modal
          title="无法删除零件"
          width={460}
          onClose={() => setBlocked(null)}
          footer={
            <Btn primary onClick={() => setBlocked(null)}>
              知道了
            </Btn>
          }
        >
          {blocked.kind === 'bom' ? (
            <>
              <p className="text-[12.5px]">
                零件 <b>{blocked.partCode}</b> 仍被以下产品的 BOM 引用，删除被拒绝：
              </p>
              <ul
                aria-label="引用该零件的产品"
                className="mt-2 max-h-[220px] overflow-auto rounded-[6px] border border-[var(--color-border)] bg-[var(--color-canvas)] p-2 text-[12px]"
              >
                {blocked.products.map((p) => (
                  <li key={p.code} className="py-0.5">
                    <span className="font-semibold">{p.code}</span> · {p.name}
                  </li>
                ))}
              </ul>
              <p className="mt-2 text-[11.5px] text-[var(--color-fg-muted)]">
                请先在「BOM 管理」里移除这些产品对该零件的引用，再删除本零件。
              </p>
            </>
          ) : (
            <>
              <p className="text-[12.5px]">
                零件 <b>{blocked.partCode}</b> 有 <b>{blocked.records}</b> 条生产投料记录（涉及{' '}
                {blocked.batches.length} 个批次），追溯记录不能删除：
              </p>
              <ul
                aria-label="引用该零件的批次"
                className="mt-2 max-h-[220px] overflow-auto rounded-[6px] border border-[var(--color-border)] bg-[var(--color-canvas)] p-2 text-[12px]"
              >
                {blocked.batches.map((b) => (
                  <li key={b.batch_no} className="py-0.5">
                    <span className="font-semibold">{b.batch_no}</span> · {b.records} 条
                  </li>
                ))}
              </ul>
              <p className="mt-2 text-[11.5px] text-[var(--color-fg-muted)]">
                追溯记录用于质量回溯，不能删除。如不再使用该零件，请把它改为「停用」状态。
              </p>
            </>
          )}
        </Modal>
      ) : null}
    </>
  )
}
