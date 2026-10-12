import { useEffect, useMemo, useState } from 'react'
import {
  Badge,
  Btn,
  Field,
  Pagination,
  ScreenError,
  classifyLoadError,
  shortTime,
} from '../components/ui'
import {
  Checkbox,
  ConfirmDialog,
  FormError,
  FormGrid,
  FormRow,
  Modal,
  ComboInput,
  TextInput,
} from '../components/form'
import { usePermissions } from '../lib/api'
import { canWrite } from '../lib/permissions'
import { products as api, PRODUCT_UNITS, opt, type Product, type ProductWrite } from '../lib/catalog'

const DEFAULT_PAGE_SIZE = 20

/**
 * 产品管理。
 *
 * 与零件屏的差别是**产品编码允许重复** —— 那是系列号，迁移 v4 明确
 * 删掉了 products.code 上的唯一索引（migrate.go:260-267，注释写着
 * 「产品编码是系列号，允许重复」）。所以这里**不能**加重复校验，
 * 加了就是给用户设一个数据库并不关心的规矩。
 *
 * 反过来，编辑时必须显式带上 status。Fyne 版的产品编辑框里那个「启用」
 * 复选框被创建后直接丢掉（product.go:196-198），加上 `st := 1` 写死，
 * 于是编辑一个已停用的产品会把它悄悄启用。HTTP 层的 status 是 *int，
 * 不传就是「保留原值」（dto.go:165-179）—— 这里沿用后者的正确语义。
 */

interface Draft {
  code: string
  name: string
  spec: string
  unit: string
  status: number
}

const emptyDraft = (): Draft => ({ code: '', name: '', spec: '', unit: '个', status: 1 })

function toDraft(p: Product): Draft {
  return { code: p.code, name: p.name, spec: p.spec ?? '', unit: p.unit ?? '个', status: p.status }
}

function validate(d: Draft): string {
  if (!d.code.trim()) return '请填写产品编码'
  if (!d.name.trim()) return '请填写产品名称'
  if (!d.unit.trim()) return '请填写单位'
  return ''
}

function toWrite(d: Draft, editing: Product | null): ProductWrite {
  const base: ProductWrite = {
    code: d.code.trim(),
    name: d.name.trim(),
    spec: opt(d.spec),
    unit: d.unit.trim(),
    // status 一直显式传：编辑时要保留用户的选择，不是恢复成 1
    status: d.status,
  }
  if (editing) {
    base.id = editing.id
    base.version = editing.version
  }
  return base
}

export function ProductsScreen() {
  const [rows, setRows] = useState<Product[] | null>(null)
  const [error, setError] = useState<{ kind: 'forbidden' | 'server'; text: string } | null>(null)
  const [q, setQ] = useState('')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE)
  const [formOpen, setFormOpen] = useState(false)
  const [editing, setEditing] = useState<Product | null>(null)
  const [draft, setDraft] = useState<Draft>(emptyDraft())
  const [formErr, setFormErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [deleting, setDeleting] = useState<Product | null>(null)

  // products 的写权限**只有 admin**：warehouse / production / viewer 都只有读。
  // 零件屏是 admin+warehouse，两者不能共用一个判断。
  const { modules } = usePermissions()
  const canEdit = canWrite(modules, 'products')

  useEffect(() => {
    let alive = true
    api
      .list()
      .then((r) => {
        if (alive) {
          setRows(r)
          setError(null)
        }
      })
      .catch((e) => {
        if (alive) setError(classifyLoadError(e, '产品'))
      })
    return () => {
      alive = false
    }
  }, [])

  const filtered = useMemo(() => {
    const list = rows ?? []
    if (!q.trim()) return list
    const k = q.trim().toLowerCase()
    return list.filter(
      (p) => p.code.toLowerCase().includes(k) || p.name.toLowerCase().includes(k),
    )
  }, [rows, q])

  const pages = Math.max(1, Math.ceil(filtered.length / pageSize))
  const current = Math.min(page, pages)
  const shown = filtered.slice((current - 1) * pageSize, current * pageSize)

  async function reload() {
    setRows(await api.list())
  }

  async function submit() {
    const msg = validate(draft)
    if (msg) {
      setFormErr(msg)
      return
    }
    setBusy(true)
    setFormErr('')
    try {
      if (editing) await api.update(toWrite(draft, editing) as never)
      else await api.create(toWrite(draft, null))
      await reload()
      setFormOpen(false)
    } catch (e) {
      setFormErr(e instanceof Error ? e.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  async function doDelete() {
    if (!deleting) return
    setBusy(true)
    try {
      await api.remove(deleting.id)
      setDeleting(null)
      await reload()
    } catch (e) {
      setFormErr(e instanceof Error ? e.message : '删除失败')
      setDeleting(null)
    } finally {
      setBusy(false)
    }
  }

  if (error) return <ScreenError kind={error.kind} text={error.text} />

  return (
    <>
      <div className="flex items-center gap-2.5">
        <h1 className="text-[15px] font-bold">产品管理</h1>
        <div className="ml-auto flex gap-2">
          {canEdit ? (
            <Btn
              primary
              onClick={() => {
                setEditing(null)
                setDraft(emptyDraft())
                setFormErr('')
                setFormOpen(true)
              }}
            >
              + 新增产品
            </Btn>
          ) : null}
        </div>
      </div>

      <section className="rounded-[8px] border border-[var(--color-border)] bg-[var(--color-surface)] p-2.5">
        <div className="flex flex-wrap items-end gap-2.5">
          <Field label="编码 / 名称">
            <input
              className="h-[26px] w-[180px] rounded-[6px] border border-[var(--color-border)] px-2.5 text-[11.5px] outline-none focus:border-[var(--color-primary)]"
              value={q}
              onChange={(e) => {
                setQ(e.target.value)
                setPage(1)
              }}
              placeholder="按编码或名称筛选"
            />
          </Field>
        </div>
      </section>

      <section className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-[8px] border border-[var(--color-border)] bg-[var(--color-surface)]">
        <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-2.5 py-1.5">
          <span className="text-[12px] font-semibold">产品列表</span>
          <span className="text-[11.5px] text-[var(--color-fg-subtle)]">共 {filtered.length} 个</span>
        </div>
        <div className="min-h-0 flex-1 overflow-auto">
          <table className="data-table">
            <colgroup>
              <col style={{ width: 150 }} />
              <col />
              <col style={{ width: 160 }} />
              <col style={{ width: 60 }} />
              <col style={{ width: 76 }} />
              <col style={{ width: 110 }} />
              <col style={{ width: 90 }} />
            </colgroup>
            <thead>
              <tr>
                <th>编码</th>
                <th>名称</th>
                <th>规格</th>
                <th className="mid">单位</th>
                <th className="mid">状态</th>
                <th>更新时间</th>
                <th className="mid col-fix">操作</th>
              </tr>
            </thead>
            <tbody>
              {shown.map((p) => (
                <tr key={p.id}>
                  <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{p.code}</td>
                  <td>{p.name}</td>
                  <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{p.spec ?? '—'}</td>
                  <td className="mid">{p.unit}</td>
                  <td className="mid">
                    <Badge kind={p.status === 1 ? 'ok' : 'neutral'}>
                      {p.status === 1 ? '启用' : '停用'}
                    </Badge>
                  </td>
                  <td style={{ color: 'var(--color-fg-muted)' }}>{shortTime(p.updated_at)}</td>
                  <td className="mid col-fix">
                    {canEdit ? (
                      <>
                        <button
                          className="mr-2 text-[var(--color-primary)]"
                          onClick={() => {
                            setEditing(p)
                            setDraft(toDraft(p))
                            setFormErr('')
                            setFormOpen(true)
                          }}
                        >
                          编辑
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
              ))}
              {shown.length === 0 && (
                <tr>
                  <td colSpan={7} className="py-8 text-center text-[12px] text-[var(--color-fg-subtle)]">
                    没有符合条件的产品
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        <Pagination
          total={filtered.length}
          shown={shown.length}
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
        <Modal
          title={editing ? '编辑产品' : '新增产品'}
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
          <FormRow label="编码">
            <TextInput
              value={draft.code}
              onChange={(v) => setDraft({ ...draft, code: v })}
              placeholder="产品编码"
              mono
              autoFocus
              onEnter={submit}
            />
          </FormRow>
          <FormRow label="名称" hint={editing ? undefined : '编码允许重复（系列号）'}>
            <TextInput
              value={draft.name}
              onChange={(v) => setDraft({ ...draft, name: v })}
              placeholder="产品名称"
              onEnter={submit}
            />
          </FormRow>
          <FormGrid>
            <FormRow label="规格">
              <TextInput
                value={draft.spec}
                onChange={(v) => setDraft({ ...draft, spec: v })}
                placeholder="规格型号"
              />
            </FormRow>
            <FormRow label="单位">
              <ComboInput
                value={draft.unit}
                onChange={(v) => setDraft({ ...draft, unit: v })}
                options={PRODUCT_UNITS}
              />
            </FormRow>
          </FormGrid>
          <FormRow label="状态">
            <div className="pt-1">
              <Checkbox
                checked={draft.status === 1}
                onChange={(c) => setDraft({ ...draft, status: c ? 1 : 2 })}
                label="启用"
              />
            </div>
          </FormRow>
        </Modal>
      ) : null}

      {deleting ? (
        <ConfirmDialog
          title="确认删除"
          danger
          busy={busy}
          message={
            <>
              确定删除产品 <b>{deleting.code}</b>（{deleting.name}）？
              <div className="mt-1.5 text-[11px] text-[var(--color-fg-muted)]">
                若该产品已有 BOM 明细或批次，删除会被拒绝。
              </div>
            </>
          }
          onConfirm={doDelete}
          onCancel={() => setDeleting(null)}
        />
      ) : null}
    </>
  )
}