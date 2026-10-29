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

/** 高亮匹配片段：查询值在结果里出现的位置标黄，命中原因一目了然。
 *  拆分大小写不敏感；查询值为空或文本不含它时原样返回。 */
function Highlight({ text, query }: { text: string; query: string }) {
  const q = query.trim()
  if (!q) return <>{text}</>
  const lower = text.toLowerCase()
  const ql = q.toLowerCase()
  const parts: { text: string; hit: boolean }[] = []
  let i = 0
  while (i < text.length) {
    const at = lower.indexOf(ql, i)
    if (at < 0) {
      parts.push({ text: text.slice(i), hit: false })
      break
    }
    if (at > i) parts.push({ text: text.slice(i, at), hit: false })
    parts.push({ text: text.slice(at, at + q.length), hit: true })
    i = at + q.length
  }
  return (
    <>
      {parts.map((p, idx) =>
        p.hit ? (
          <mark
            key={idx}
            style={{ background: 'var(--color-warn-bg)', color: 'inherit', borderRadius: 2, padding: '0 1px' }}
          >
            {p.text}
          </mark>
        ) : (
          <span key={idx}>{p.text}</span>
        ),
      )}
    </>
  )
}

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
  /** 查询命中的"对象"数：批次（按产品查）或零件（按零件查）。
      与记录条数分开：命中了对象但对象还没有投料记录，是另一种空态。 */
  const [hits, setHits] = useState(0)
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
      let hitObjects = 0
      if (mode === 'product') {
        found = await traces.byProduct(value)
        // 精确批次号：命中与否以"批次列表里存在这个批次号"为准。
        hitObjects = batches.some((b) => b.batch_no === value) ? 1 : 0
      } else if (mode === 'productFuzzy') {
        // 产品名称/编码在前端匹配批次列表，再按命中的批次逐个取投料。
        const q = value.toLowerCase()
        const matched = batches.filter(
          (b) =>
            (b.product_name ?? '').toLowerCase().includes(q) ||
            (b.product_code ?? '').toLowerCase().includes(q),
        )
        hitObjects = matched.length
        const lists = await Promise.all(matched.map((b) => traces.byBatch(b.id)))
        found = lists.flat()
      } else if (mode === 'partLot') {
        found = await traces.byPartLot(value)
        hitObjects = found.length > 0 ? 1 : 0
      } else {
        // 零件：编码精确走服务端；名称匹配的零件补一轮 by-batch 过滤。
        const ids = partIDsMatching(value)
        hitObjects = ids.size
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
      setHits(hitObjects)
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
            setHits(0)
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
      {rows?.length === 0 ? (
        <p role="status">
          {hits > 0
            ? // 命中了批次/零件，但它们还没有投料记录——这是数据状态，不是查不到。
              '命中的批次还没有投料记录（在「记录投料」保存后即可查到）'
            : '未找到追溯记录：请核对查询值与查询方式是否匹配'}
        </p>
      ) : null}
      {groups.length > 0 ? (
        <div className="max-h-[420px] overflow-auto rounded-[6px] border border-[var(--color-border)]">
          {groups.map((g) => (
            <div key={g.batch?.id ?? g.rows[0].batch_id} className="border-b border-[var(--color-border-subtle)] last:border-b-0">
              <div className="flex items-center gap-2 bg-[var(--color-canvas)] px-3 py-1.5 text-[11.5px]">
                <span style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>
                  {g.batch ? <Highlight text={g.batch.batch_no} query={query} /> : `批次 #${g.rows[0].batch_id}`}
                </span>
                <span className="text-[var(--color-fg-muted)]">
                  {g.batch ? (
                    <>
                      <Highlight text={g.batch.product_name ?? ''} query={query} />
                      {' ['}
                      <Highlight text={g.batch.product_code ?? ''} query={query} />
                      {'] · '}
                      {statusText(g.batch.status)}
                    </>
                  ) : ''}
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
                            {part ? <Highlight text={part.code} query={query} /> : `#${r.part_id}`}
                          </span>
                          <span className="ml-1 text-[11px] text-[var(--color-fg-muted)]">
                            {part ? <Highlight text={part.name ?? ''} query={query} /> : ''}
                          </span>
                        </td>
                        <td className="num">{fmtQty(r.used_qty)}</td>
                        <td>
                          {r.allocations && r.allocations.length > 0 ? (
                            <div className="flex flex-col">
                              {r.allocations.map((a) => (
                                <span key={a.inbound_id} style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>
                                  <Highlight text={a.batch_no} query={query} /> × {fmtQty(a.qty)}
                                </span>
                              ))}
                            </div>
                          ) : (
                            <span style={{ color: 'var(--color-fg-muted)' }}>
                              {r.part_batch_no ? <Highlight text={r.part_batch_no} query={query} /> : '—'}
                            </span>
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
