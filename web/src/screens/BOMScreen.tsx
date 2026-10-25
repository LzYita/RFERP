import { Fragment, useEffect, useMemo, useState } from 'react'
import {
  Badge,
  Btn,
  ScreenError,
  classifyLoadError,
} from '../components/ui'
import {
  Checkbox,
  ConfirmDialog,
  FormError,
  FormRow,
  Modal,
  NumberInput,
  SearchSelect,
  resolvePick,
  TextInput,
} from '../components/form'
import { usePermissions } from '../lib/api'
import { canWrite } from '../lib/permissions'
import { buildBomGroups } from '../lib/bomTree'
import {
  bom as bomApi,
  parts as partsApi,
  products as productsApi,
  pickLabel,
  opt,
  type BOMItem,
  type Part,
  type Product,
} from '../lib/catalog'

/**
 * BOM 管理。
 *
 * 这一屏最难的地方不是明细表，而是**两套产品选择机制必须双向同步**，
 * Fyne 版为此花了两个互为镜像的函数（bom.go:259-397）和一条锁定测试
 * （bom_test.go:13-61）。这里的对应关系：
 *
 *   名称 → 编码 → 规格   级联三级（Fyne 的三个 Select）
 *   输入框               搜索选择器（Fyne 的 searchSelect）
 *
 * 任一处的选择写进另一处；清空任一处也清掉另一处。**不用其一就清掉另一个**
 * 是 Fyne 版明确写下的规则（bom.go:378-397），因为两个都留着会让人以为
 * 当前生效的是另一个。
 *
 * 用产品列表直接做一个下拉是不够的：产品重名与同系列不同规格是这套数据里
 * 真实存在的形态，只有级联才区分得开。
 *
 * 明细表是树状：根 = 产品，子层 = 零件分类（零件主数据的 part_type——
 * BOMItem 不冗余它，按 part_id 从 allParts 现查），叶子 = 明细行。
 * 分组是纯函数，在 lib/bomTree.ts，可单测。
 *
 * 本屏只改**明细**：往 BOM 里挂零件、删明细、改用量/模式/损耗率/可替换。
 * 零件主数据（编码、名称、分类、库存…）的新增与编辑留在「零件管理」屏——
 * 这里是"这个产品用多少什么料"的地方，不是维护物料档案的地方。
 */

type Picker = { id: number; label: string; code: string; name: string; spec: string }
type PartPicker = { id: number; label: string }

function toPicker(p: Product): Picker {
  const spec = p.spec ?? ''
  return { id: p.id, code: p.code, name: p.name, spec, label: pickLabel(p.code, p.name, spec) }
}

interface Draft {
  part: PartPicker | null
  partText: string
  useMode: string
  qty: string
  loss: string
  replaceable: boolean
  remark: string
}

/** 行内编辑的草稿：只有这四个字段（与 PUT /api/bom 的契约一致）。 */
interface EditDraft {
  useMode: string
  qty: string
  loss: string
  replaceable: boolean
}

const emptyDraft = (): Draft => ({
  part: null,
  partText: '',
  useMode: '0',
  qty: '1',
  loss: '0',
  replaceable: false,
  remark: '',
})

const USE_MODES = [
  { value: '0', label: '每台用N个零件' },
  { value: '1', label: '每M台用1个零件（如包装箱）' },
] as const

export function BOMScreen() {
  const [products, setProducts] = useState<Product[] | null>(null)
  const [allParts, setAllParts] = useState<Part[] | null>(null)
  const [items, setItems] = useState<BOMItem[] | null>(null)
  const [error, setError] = useState<{ kind: 'forbidden' | 'server'; text: string } | null>(null)

  // 级联三级
  const [cName, setCName] = useState('')
  const [cCode, setCCode] = useState('')
  const [cSpec, setCSpec] = useState('')
  // 搜索框
  const [searchPick, setSearchPick] = useState<Picker | null>(null)

  const [selectedRow, setSelectedRow] = useState<number | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [draft, setDraft] = useState<Draft>(emptyDraft())
  const [formErr, setFormErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [deleting, setDeleting] = useState<BOMItem | null>(null)

  // 树的折叠状态：key 是分类名。换产品即重置（新树没有理由继承旧树的折叠）。
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())

  // 零件主数据表单（只用于新增；编辑一条明细不碰零件主数据）
  // 编辑一条 BOM 明细：只有用量、用量模式、损耗率、可替换四个字段。
  const [editItem, setEditItem] = useState<BOMItem | null>(null)
  const [editDraft, setEditDraft] = useState<EditDraft>({
    useMode: '0',
    qty: '1',
    loss: '0',
    replaceable: false,
  })
  const [editErr, setEditErr] = useState('')
  const [editBusy, setEditBusy] = useState(false)

  const { modules } = usePermissions()
  const canEdit = canWrite(modules, 'bom')

  useEffect(() => {
    let alive = true
    Promise.all([productsApi.list(), partsApi.list()])
      .then(([ps, pts]) => {
        if (!alive) return
        setProducts(ps)
        // 零件列表加载期间零件选择器是空的；用空数组而不是 null，
        // 否则 resolvePick 会拿到 null.all 直接抛。
        setAllParts(pts)
        setError(null)
      })
      .catch((e) => {
        if (alive) setError(classifyLoadError(e, 'BOM'))
      })
    return () => {
      alive = false
    }
  }, [])

  const pickers = useMemo(() => (products ?? []).map(toPicker), [products])
  const partPickers = useMemo<PartPicker[]>(
    () => (allParts ?? []).map((p) => ({ id: p.id, label: pickLabel(p.code, p.name, p.spec) })),
    [allParts],
  )

  // 树：分类分组由纯函数完成；零件主数据或明细变化都自动重新分组
  // （比如把某零件的分类从「电子」改成「五金」，树应立刻搬家）。
  const groups = useMemo(
    () => buildBomGroups(items ?? [], allParts ?? []),
    [items, allParts],
  )

  function toggleGroup(name: string) {
    setCollapsed((prev) => {
      const next = new Set(prev)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })
  }

  // 级联的候选：名称 → 编码 → 规格，逐级收窄
  const codesForName = useMemo(() => {
    if (!cName) return []
    const set = new Set<string>()
    for (const p of products ?? []) if (p.name === cName) set.add(p.code)
    return [...set].sort()
  }, [products, cName])

  const specsForCode = useMemo(() => {
    if (!cName || !cCode) return []
    const set = new Set<string>()
    for (const p of products ?? []) {
      if (p.name === cName && p.code === cCode && p.spec) set.add(p.spec)
    }
    return [...set].sort()
  }, [products, cName, cCode])

  const names = useMemo(() => {
    const set = new Set<string>()
    for (const p of products ?? []) if (p.name) set.add(p.name)
    return [...set].sort()
  }, [products])

  /**
   * 级联解析出的产品。
   *
   * 名称与编码都必填。规格为「没有可选值」时同样必填 —— 若该编码下存在规格，
   * 说明这几个产品重名同编码，只能靠规格区分，选第一个就是选错。
   */
  const cascadePick = useMemo<Picker | null>(() => {
    if (!cName || !cCode) return null
    const candidates = (products ?? []).filter((p) => p.name === cName && p.code === cCode)
    if (specsForCode.length > 0) {
      if (!cSpec) return null
      const hit = candidates.find((p) => p.spec === cSpec)
      return hit ? toPicker(hit) : null
    }
    return candidates.length ? toPicker(candidates[0]) : null
  }, [products, cName, cCode, cSpec, specsForCode])

  // 搜索框优先，其次级联 —— 对应 Fyne 的 resolveProductID
  const active = searchPick ?? cascadePick

  // 选了搜索框就把级联填上（Fyne: syncCascadeFromPicker）
  function pickFromSearch(v: Picker | null) {
    setSearchPick(v)
    if (v) {
      setCName(v.name)
      setCCode(v.code)
      setCSpec(v.spec)
    } else {
      setCName('')
      setCCode('')
      setCSpec('')
    }
  }

  // 级联变了就把搜索框填上（Fyne: syncPickerFromCascade）
  useEffect(() => {
    if (!cascadePick) return
    setSearchPick(cascadePick)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cascadePick?.id])

  useEffect(() => {
    if (!active) {
      setItems(null)
      setSelectedRow(null)
      setCollapsed(new Set())
      return
    }
    let alive = true
    bomApi
      .list(active.id)
      .then((r) => {
        if (alive) {
          setItems(r)
          setSelectedRow(null)
          setCollapsed(new Set())
        }
      })
      .catch((e) => {
        if (alive) setError(classifyLoadError(e, 'BOM 明细'))
      })
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active?.id])

  async function reload() {
    if (!active) return
    setItems(await bomApi.list(active.id))
  }

  function openEditItem(item: BOMItem) {
    setEditItem(item)
    setEditDraft({
      useMode: String(item.use_mode),
      qty: String(item.quantity),
      loss: String(item.loss_rate),
      replaceable: item.replaceable === 1,
    })
    setEditErr('')
  }

  /**
   * 保存明细的用量/模式/损耗率。
   *
   * 只提交这三个字段 + id + version：服务端 DisallowUnknownFields，多传
   * 零件归属会被 400 拒绝。校验口径与新增保持一致（后端同一套规则）。
   */
  async function submitEditItem() {
    if (!editItem) return
    const q = Number(editDraft.qty)
    const l = Number(editDraft.loss)
    if (!Number.isFinite(q)) {
      setEditErr('用量必须是有效数值')
      return
    }
    if (q <= 0) {
      setEditErr('BOM用量必须大于0')
      return
    }
    if (!Number.isFinite(l)) {
      setEditErr('损耗率必须是有效数值')
      return
    }
    if (l < 0 || l > 100) {
      setEditErr('损耗率必须在0到100之间')
      return
    }
    setEditBusy(true)
    setEditErr('')
    try {
      await bomApi.update({
        id: editItem.id,
        quantity: q,
        loss_rate: l,
        use_mode: Number(editDraft.useMode),
        replaceable: editDraft.replaceable ? 1 : 0,
        version: editItem.version,
      })
      setEditItem(null)
      await reload()
    } catch (e) {
      setEditErr(e instanceof Error ? e.message : '保存失败')
    } finally {
      setEditBusy(false)
    }
  }

  async function submit() {
    if (!active) return
    const chosen = resolvePick(partPickers, draft.part, draft.partText)
    if (!chosen) {
      setFormErr('请从下拉列表中点选一个零件')
      return
    }
    const q = Number(draft.qty)
    const l = Number(draft.loss)
    if (!Number.isFinite(q) || !Number.isFinite(l)) {
      setFormErr('用量和损耗率必须是有效数值')
      return
    }
    if (q <= 0) {
      setFormErr('BOM用量必须大于0')
      return
    }
    if (l < 0 || l > 100) {
      setFormErr('损耗率必须在0到100之间')
      return
    }
    setBusy(true)
    setFormErr('')
    try {
      await bomApi.add({
        product_id: active.id,
        part_id: chosen.id,
        quantity: q,
        loss_rate: l,
        remark: opt(draft.remark),
        replaceable: draft.replaceable ? 1 : 0,
        use_mode: Number(draft.useMode),
      })
      await reload()
      setFormOpen(false)
    } catch (e) {
      setFormErr(e instanceof Error ? e.message : '添加失败')
    } finally {
      setBusy(false)
    }
  }

  async function doRemove() {
    if (!deleting) return
    setBusy(true)
    try {
      await bomApi.remove(deleting.id)
      setDeleting(null)
      await reload()
    } catch (e) {
      setFormErr(e instanceof Error ? e.message : '移除失败')
      setDeleting(null)
    } finally {
      setBusy(false)
    }
  }

  if (error) return <ScreenError kind={error.kind} text={error.text} />

  const sel = (items ?? []).find((i) => i.id === selectedRow) ?? null
  // 表格总列数：操作列（编辑明细的用量）跟随 bom 写权限，
  // 根行 / 分类行 / 空态行的 colSpan 都跟随它
  const COLS = canEdit ? 9 : 8

  return (
    <>
      <div className="flex items-center gap-2.5">
        <h1 className="text-[15px] font-bold">BOM 管理</h1>
      </div>

      {/* 产品选择：级联三级 + 搜索框，两者双向同步 */}
      <section className="rounded-[8px] border border-[var(--color-border)] bg-[var(--color-surface)] p-2.5">
        <div className="flex flex-wrap items-end gap-2.5">
          <span className="pb-1.5 text-[11px] text-[var(--color-fg-muted)]">选择产品:</span>

          <label className="flex flex-col gap-[3px]">
            <span className="text-[11px] text-[var(--color-fg-muted)]">名称</span>
            <select
              className="h-[26px] w-[130px] rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-2 text-[11.5px] outline-none focus:border-[var(--color-primary)]"
              value={cName}
              onChange={(e) => {
                setCName(e.target.value)
                setCCode('')
                setCSpec('')
              }}
            >
              <option value="">全部</option>
              {names.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </label>

          <label className="flex flex-col gap-[3px]">
            <span className="text-[11px] text-[var(--color-fg-muted)]">编码</span>
            <select
              className="h-[26px] w-[130px] rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-2 text-[11.5px] outline-none focus:border-[var(--color-primary)] disabled:bg-[var(--color-canvas)] disabled:text-[var(--color-fg-subtle)]"
              value={cCode}
              disabled={!cName}
              onChange={(e) => {
                setCCode(e.target.value)
                setCSpec('')
              }}
            >
              <option value="">全部</option>
              {codesForName.map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </select>
          </label>

          <label className="flex flex-col gap-[3px]">
            <span className="text-[11px] text-[var(--color-fg-muted)]">规格</span>
            <select
              className="h-[26px] w-[130px] rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-2 text-[11.5px] outline-none focus:border-[var(--color-primary)] disabled:bg-[var(--color-canvas)] disabled:text-[var(--color-fg-subtle)]"
              value={cSpec}
              disabled={specsForCode.length === 0}
              onChange={(e) => setCSpec(e.target.value)}
            >
              <option value="">全部</option>
              {specsForCode.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </label>

          <div className="flex flex-col gap-[3px]">
            <span className="text-[11px] text-[var(--color-fg-muted)]">或搜索</span>
            <SearchSelect
              all={pickers}
              value={searchPick}
              onChange={(v) => pickFromSearch(v)}
            />
          </div>

          {canEdit ? (
            <div className="ml-auto flex gap-2">
              {/* 本屏只往 BOM 里挂已有零件；零件主数据的新增在「零件管理」屏，
                  两个动作放在同一行按钮里容易被当成同一件事。 */}
              <Btn
                disabled={!active}
                onClick={() => {
                  setDraft(emptyDraft())
                  setFormErr('')
                  setFormOpen(true)
                }}
              >
                + 添加 BOM 零件
              </Btn>
              <Btn danger disabled={!sel} onClick={() => sel && setDeleting(sel)}>
                删除
              </Btn>
            </div>
          ) : null}
        </div>
      </section>

      <section className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-[8px] border border-[var(--color-border)] bg-[var(--color-surface)]">
        <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-2.5 py-1.5">
          <span className="text-[12px] font-semibold">
            {active ? `BOM 明细 · ${active.code} ${active.name}` : 'BOM 明细'}
          </span>
          <span className="text-[11.5px] text-[var(--color-fg-subtle)]">
            {active ? `共 ${(items ?? []).length} 个零件` : ''}
          </span>
        </div>
        <div className="min-h-0 flex-1 overflow-auto">
          {!active ? (
            <div className="py-10 text-center text-[12px] text-[var(--color-fg-subtle)]">
              请先选择产品
            </div>
          ) : (
            <table className="data-table">
              <colgroup>
                <col style={{ width: 40 }} />
                <col style={{ width: 140 }} />
                <col />
                <col style={{ width: 130 }} />
                <col style={{ width: 96 }} />
                <col style={{ width: 80 }} />
                <col style={{ width: 84 }} />
                <col />
                {canEdit ? <col style={{ width: 64 }} /> : null}
              </colgroup>
              <thead>
                <tr>
                  <th />
                  <th>零件编码</th>
                  <th>零件名称</th>
                  <th>用量模式</th>
                  <th className="num">用量</th>
                  <th className="num">损耗率</th>
                  <th className="mid">可替换</th>
                  <th>备注</th>
                  {canEdit ? (
                    <th className="mid col-fix" style={{ width: 64 }}>
                      操作
                    </th>
                  ) : null}
                </tr>
              </thead>
              <tbody>
                {/* 根：产品。树的第一层，永远展开、不可折叠 */}
                <tr data-tree-root>
                  <td colSpan={COLS}>
                    <span className="font-semibold">
                      {active.code} {active.name}
                    </span>
                    <span className="ml-2 text-[11.5px] text-[var(--color-fg-subtle)]">
                      共 {(items ?? []).length} 个零件 · {groups.length} 类
                    </span>
                  </td>
                </tr>
                {groups.map((g) => (
                  <Fragment key={g.name}>
                    {/* 分类层：可折叠。点行切换，整行都是热区 */}
                    <tr data-tree-group className="cursor-pointer" onClick={() => toggleGroup(g.name)}>
                      <td colSpan={COLS} style={{ paddingLeft: 14 }}>
                        <span className="mr-1.5 inline-block w-3 text-center text-[10px] text-[var(--color-fg-muted)]">
                          {collapsed.has(g.name) ? '▶' : '▼'}
                        </span>
                        <span className="font-semibold">{g.name}</span>
                        <span className="ml-2 text-[11.5px] text-[var(--color-fg-subtle)]">
                          {g.leaves.length} 项
                        </span>
                      </td>
                    </tr>
                    {!collapsed.has(g.name) &&
                      g.leaves.map(({ item }) => (
                        <tr
                          key={item.id}
                          data-selected={selectedRow === item.id}
                          onClick={() => setSelectedRow(item.id === selectedRow ? null : item.id)}
                        >
                          <td className="mid" style={{ paddingLeft: 30 }}>
                            <input
                              type="checkbox"
                              checked={selectedRow === item.id}
                              onChange={() => setSelectedRow(item.id === selectedRow ? null : item.id)}
                              aria-label={`选择 ${item.part_code ?? item.id}`}
                            />
                          </td>
                          <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>
                            {item.part_code ?? '—'}
                          </td>
                          <td>{item.part_name ?? '—'}</td>
                          <td>
                            <Badge kind={item.use_mode === 1 ? 'neutral' : 'ok'}>
                              {item.use_mode === 1 ? '每M台用1个' : '每台用N个'}
                            </Badge>
                          </td>
                          {/* 两种模式的单位不同：前者是台，后者是个 */}
                          <td className="num">
                            {item.use_mode === 1
                              ? `${Number(item.quantity).toFixed(0)}台`
                              : `${Number(item.quantity).toFixed(2)}个`}
                          </td>
                          <td className="num">{Number(item.loss_rate).toFixed(1)}%</td>
                          <td className="mid">
                            {item.replaceable === 1 ? (
                              <Badge kind="warn">可替换</Badge>
                            ) : (
                              <span className="text-[var(--color-fg-subtle)]">—</span>
                            )}
                          </td>
                          <td>{item.remark ?? ''}</td>
                          {canEdit ? (
                            <td className="mid col-fix">
                              {/* 行本身点击是选中（供删除），编辑按钮要拦住冒泡 */}
                              <button
                                className="text-[var(--color-primary)]"
                                onClick={(e) => {
                                  e.stopPropagation()
                                  openEditItem(item)
                                }}
                              >
                                编辑
                              </button>
                            </td>
                          ) : null}
                        </tr>
                      ))}
                  </Fragment>
                ))}
                {(items ?? []).length === 0 && (
                  <tr>
                    <td colSpan={COLS} className="py-8 text-center text-[12px] text-[var(--color-fg-subtle)]">
                      该产品还没有 BOM 明细
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          )}
        </div>
      </section>

      {formOpen ? (
        <Modal
          title="添加 BOM 零件"
          onClose={() => setFormOpen(false)}
          width={440}
          footer={
            <>
              <Btn onClick={() => setFormOpen(false)}>取消</Btn>
              <Btn primary disabled={busy} onClick={submit}>
                {busy ? '保存中…' : '保存'}
              </Btn>
            </>
          }
        >
          <FormError text={formErr} />
          <FormRow label="零件（编码/名称）">
            <SearchSelect
              all={partPickers}
              value={draft.part}
              onChange={(v) => setDraft({ ...draft, part: v })}
            />
          </FormRow>
          <FormRow label="用量模式">
            <select
              className="h-[26px] w-full rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-2 text-[11.5px] outline-none focus:border-[var(--color-primary)]"
              value={draft.useMode}
              onChange={(e) =>
                setDraft({ ...draft, useMode: e.target.value, qty: '1' })
              }
            >
              {USE_MODES.map((m) => (
                <option key={m.value} value={m.value}>
                  {m.label}
                </option>
              ))}
            </select>
          </FormRow>
          <FormRow label={draft.useMode === '1' ? '每多少台用1个(M)' : '每台用量(个)'}>
            <NumberInput
              value={draft.qty}
              onChange={(v) => setDraft({ ...draft, qty: v })}
            />
          </FormRow>
          <FormRow label="损耗率%">
            <NumberInput
              value={draft.loss}
              onChange={(v) => setDraft({ ...draft, loss: v })}
            />
          </FormRow>
          <FormRow label="">
            <Checkbox
              checked={draft.replaceable}
              onChange={(v) => setDraft({ ...draft, replaceable: v })}
              label="该零件可被替换（跳过时不消耗库存）"
            />
          </FormRow>
          <FormRow label="备注">
            <TextInput
              value={draft.remark}
              onChange={(v) => setDraft({ ...draft, remark: v })}
            />
          </FormRow>
        </Modal>
      ) : null}

      {editItem ? (
        <Modal
          title="编辑 BOM 明细"
          onClose={() => setEditItem(null)}
          width={420}
          footer={
            <>
              <Btn onClick={() => setEditItem(null)}>取消</Btn>
              <Btn primary disabled={editBusy} onClick={submitEditItem}>
                {editBusy ? '保存中…' : '保存'}
              </Btn>
            </>
          }
        >
          <FormError text={editErr} />
          {/* 只读展示这一行挂的是什么零件：编辑范围不含零件归属，
              标出来才不会让人以为可以在这里换件（换件 = 删掉再加一行）。 */}
          <FormRow label="零件">
            <div className="flex h-[26px] items-center text-[11.5px]">
              <span style={{ fontFamily: 'Consolas, monospace' }}>{editItem.part_code ?? '—'}</span>
              <span className="ml-2 text-[var(--color-fg-muted)]">{editItem.part_name ?? ''}</span>
            </div>
          </FormRow>
          <FormRow label="用量模式">
            <select
              className="h-[26px] w-full rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-2 text-[11.5px] outline-none focus:border-[var(--color-primary)]"
              value={editDraft.useMode}
              onChange={(e) => setEditDraft({ ...editDraft, useMode: e.target.value })}
            >
              {USE_MODES.map((m) => (
                <option key={m.value} value={m.value}>
                  {m.label}
                </option>
              ))}
            </select>
          </FormRow>
          <FormRow label={editDraft.useMode === '1' ? '每多少台用1个(M)' : '每台用量(个)'}>
            <NumberInput
              value={editDraft.qty}
              onChange={(v) => setEditDraft({ ...editDraft, qty: v })}
            />
          </FormRow>
          <FormRow label="损耗率%">
            <NumberInput
              value={editDraft.loss}
              onChange={(v) => setEditDraft({ ...editDraft, loss: v })}
            />
          </FormRow>
          <FormRow label="可替换">
            <Checkbox
              checked={editDraft.replaceable}
              onChange={(v) => setEditDraft({ ...editDraft, replaceable: v })}
              label="该零件可被替换（跳过时不消耗库存）"
            />
          </FormRow>
        </Modal>
      ) : null}

      {deleting ? (
        <ConfirmDialog
          title="确认删除"
          danger
          busy={busy}
          message="确定从BOM中移除该零件？"
          onConfirm={doRemove}
          onCancel={() => setDeleting(null)}
        />
      ) : null}

    </>
  )
}