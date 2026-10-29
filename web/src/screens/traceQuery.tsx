import { useMemo, useState } from 'react'
import { Modal, FormRow, TextInput, Select, FormError } from '../components/form'
import { Btn, fmtQty } from '../components/ui'
import { traces, statusText, type Batch, type BatchTrace } from '../lib/batch'
import type { Part } from '../lib/catalog'

/**
 * 双向追溯查询：四种入口，对应四类查找路径。
 *
 *  | 模式           | 输入                 | 服务端路径                    |
 *  |----------------|----------------------|-------------------------------|
 *  | 产品批次号     | 精确批次号           | /api/traces/by-product        |
 *  | 产品名称/编码  | 模糊匹配产品         | 前端在批次列表里筛选再按批查   |
 *  | 零件编码/名称  | 精确编码或模糊名称   | /api/traces/by-part（编码）+ 前端按名称过滤 |
 *  | 零件批次号     | 入库批次号 RK-…      | /api/traces/by-part-lot       |
 *
 * 产品/零件的"模糊"部分由前端做：批次列表与零件表本来就在内存里，
 * 再造一套模糊查询端点只会多一种口径。结果统一按生产批次分组展示。
 */

type Mode = 'product' | 'productFuzzy' | 'part' | 'partLot'

const MODES: { value: Mode; label: string; input: string; placeholder: string }[] = [
  { value: 'product', label: '产品批次号', input: '产品批次号', placeholder: '输入完整的生产批次号，如 BATCH20261029…' },
  { value: 'productFuzzy', label: '产品名称/编码', input: '产品名称或编码', placeholder: '支持部分匹配，列出该产品的所有批次' },
  { value: 'part', label: '零件编码/名称', input: '零件编码或名称', placeholder: '编码精确匹配；名称支持部分匹配' },
  { value: 'partLot', label: '零件批次号', input: '零件批次号', placeholder: '入库批次号，如 RK-20261029-0001' },
]

/** 按生产批次分组的结果：组头是批次信息，行是投料记录。 */
interface Group {
  batch?: Batch
  rows: BatchTrace[]
}

function groupByBatch(rows: BatchTrace[], byId: Map<number, Batch>): Group[] {
  const order: number[] = []
  const groups = new Map<number, Group>()
  for (const r of rows) {
    let g = groups.get(r.batch_id)
    if (!g) {
      g = { batch: byId.get(r.batch_id), rows: [] }
      groups.set(r.batch_id, g)
      order.push(r.batch_id)
    }
    g.rows.push(r)
  }
  return order.map((id) => groups.get(id)!)
}

export function TraceQuery({
  batches,
  parts,
  onClose,
}: {
  batches: Batch[]
  parts: Part[]
  onClose: () => void
}) {
  const [mode, setMode] = useState<Mode>('product')
  const [query, setQuery] = useState('')
  const [rows, setRows] = useState<BatchTrace[] | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const batchById = useMemo(() => new Map(batches.map((b) => [b.id, b])), [batches])
  const partById = useMemo(() => new Map(parts.map((p) => [p.id, p])), [parts])

  const modeInfo = MODES.find((m) => m.value === mode)!

  function partIDsMatching(value: string): Set<number> {
    const q = value.toLowerCase()
    return new Set(
      parts
        .filter((p) => p.code === value || (p.name ?? '').toLowerCase().includes(q))
        .map((p) => p.id),
    )
  }

  async function search() {
    const value = query.trim()
    setRows(null)
    if (!value) {
      setError('请输入查询值')
      return
    }
    setBusy(true)
    setError('')
    try {
      let found: BatchTrace[]
      if (mode === 'product') {
        found = await traces.byProduct(value)
      } else if (mode === 'productFuzzy') {
        // 产品名称/编码在前端匹配批次列表，再按命中的批次逐个取投料。
        const q = value.toLowerCase()
        const hits = batches.filter(
          (b) =>
            (b.product_name ?? '').toLowerCase().includes(q) ||
            (b.product_code ?? '').toLowerCase().includes(q),
        )
        const lists = await Promise.all(hits.map((b) => traces.byBatch(b.id)))
        found = lists.flat()
      } else if (mode === 'partLot') {
        found = await traces.byPartLot(value)
      } else {
        // 零件：编码精确走服务端；名称匹配的零件补一轮 by-batch 过滤。
        const ids = partIDsMatching(value)
        const named = [...ids].filter((id) => partById.get(id)?.code !== value)
        const byCode = await traces.byPart(value)
        const seen = new Set(byCode.map((r) => r.id))
        const extra: BatchTrace[] = []
        if (named.length > 0) {
          const lists = await Promise.all(
            batches.map(async (b) => {
              const all = await traces.byBatch(b.id)
              return all.filter((r) => named.includes(r.part_id) && !seen.has(r.id))
            }),
          )
          extra.push(...lists.flat())
        }
        found = [...byCode, ...extra]
      }
      setRows(found)
    } catch (e) {
      setError(e instanceof Error ? e.message : '查询失败')
    } finally {
      setBusy(false)
    }
  }

  const groups = rows ? groupByBatch(rows, batchById) : []

  return (
    <Modal
      title="双向追溯查询"
      onClose={onClose}
      width={880}
      footer={
        <>
          <Btn onClick={onClose}>关闭</Btn>
          <Btn primary disabled={busy} onClick={() => void search()}>
            {busy ? '查询中…' : '查询'}
          </Btn>
        </>
      }
    >
      <FormRow label="查询方式">
        <Select
          value={mode}
          disabled={busy}
          onChange={(v) => {
            setMode(v as Mode)
            setRows(null)
            setError('')
          }}
          options={MODES.map((m) => ({ value: m.value, label: m.label }))}
        />
      </FormRow>
      <FormRow label={modeInfo.input}>
        <TextInput
          value={query}
          disabled={busy}
          onChange={(v) => {
            setQuery(v)
            setRows(null)
          }}
          placeholder={modeInfo.placeholder}
        />
      </FormRow>
      <FormError text={error} />
      {rows?.length === 0 ? <p role="status">未找到追溯记录</p> : null}
      {groups.length > 0 ? (
        <div className="max-h-[420px] overflow-auto rounded-[6px] border border-[var(--color-border)]">
          {groups.map((g) => (
            <div key={g.batch?.id ?? g.rows[0].batch_id} className="border-b border-[var(--color-border-subtle)] last:border-b-0">
              <div className="flex items-center gap-2 bg-[var(--color-canvas)] px-3 py-1.5 text-[11.5px]">
                <span style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>
                  {g.batch?.batch_no ?? `批次 #${g.rows[0].batch_id}`}
                </span>
                <span className="text-[var(--color-fg-muted)]">
                  {g.batch ? `${g.batch.product_name ?? ''} [${g.batch.product_code ?? ''}] · ${statusText(g.batch.status)}` : ''}
                </span>
                <span className="ml-auto text-[var(--color-fg-subtle)]">{g.rows.length} 条</span>
              </div>
              <table className="data-table is-narrow is-compact">
                <colgroup>
                  <col style={{ width: 110 }} />
                  <col className="num" style={{ width: 84 }} />
                  <col style={{ width: 190 }} />
                  <col />
                </colgroup>
                <thead>
                  <tr>
                    <th>零件</th>
                    <th className="num">用量</th>
                    <th>零件批次（按顺序扣）</th>
                    <th>供应商</th>
                  </tr>
                </thead>
                <tbody>
                  {g.rows.map((r) => {
                    const part = partById.get(r.part_id)
                    return (
                      <tr key={r.id}>
                        <td>
                          <span style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>
                            {part?.code ?? `#${r.part_id}`}
                          </span>
                          <span className="ml-1 text-[11px] text-[var(--color-fg-muted)]">{part?.name ?? ''}</span>
                        </td>
                        <td className="num">{fmtQty(r.used_qty)}</td>
                        <td>
                          {r.allocations && r.allocations.length > 0 ? (
                            <div className="flex flex-col">
                              {r.allocations.map((a) => (
                                <span key={a.inbound_id} style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>
                                  {a.batch_no} × {fmtQty(a.qty)}
                                </span>
                              ))}
                            </div>
                          ) : (
                            <span style={{ color: 'var(--color-fg-muted)' }}>{r.part_batch_no || '—'}</span>
                          )}
                        </td>
                        <td>{r.supplier ?? '—'}</td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          ))}
        </div>
      ) : null}
    </Modal>
  )
}
