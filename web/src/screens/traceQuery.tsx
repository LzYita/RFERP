import { useState } from 'react'
import { Modal, FormRow, TextInput, Select, FormError } from '../components/form'
import { Btn, fmtQty } from '../components/ui'
import { traces, type Batch, type BatchTrace } from '../lib/batch'
import type { Part } from '../lib/catalog'

export function TraceQuery({ batches, parts, onClose }: { batches: Batch[]; parts: Part[]; onClose: () => void }) {
  const [kind, setKind] = useState('product')
  const [query, setQuery] = useState('')
  const [rows, setRows] = useState<BatchTrace[] | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  async function search() {
    const value = query.trim()
    setRows(null)
    if (!value) { setError('请输入查询值'); return }
    setBusy(true)
    setError('')
    try { setRows(await (kind === 'product' ? traces.byProduct(value) : traces.byPart(value))) }
    catch (e) { setError(e instanceof Error ? e.message : '查询失败') }
    finally { setBusy(false) }
  }
  return <Modal title="双向追溯查询" onClose={onClose} width={820}
    footer={<Btn primary disabled={busy} onClick={() => void search()}>{busy ? '查询中…' : '查询'}</Btn>}>
    <FormRow label="查询方式"><Select value={kind} disabled={busy} onChange={(v) => { setKind(v); setRows(null); setError('') }} options={[{ value: 'product', label: '产品批次号' }, { value: 'part', label: '零件编码' }]} /></FormRow>
    <FormRow label={kind === 'product' ? '产品批次号' : '零件编码'}><TextInput value={query} disabled={busy} onChange={(v) => { setQuery(v); setRows(null) }} placeholder="输入完整批次号或零件编码" /></FormRow>
    <FormError text={error} />
    {rows?.length === 0 ? <p role="status">未找到追溯记录</p> : null}
    {rows && rows.length > 0 ? <div className="overflow-auto"><table className="data-table"><thead><tr><th>产品批次</th><th>零件</th><th>供应批次</th><th>用量</th><th>供应商</th></tr></thead><tbody>
      {rows.map((r) => <tr key={r.id}><td>{batches.find((b) => b.id === r.batch_id)?.batch_no ?? `批次 #${r.batch_id}`}</td><td>{parts.find((p) => p.id === r.part_id)?.code ?? `零件 #${r.part_id}`}</td><td>{r.part_batch_no ?? '—'}</td><td className="num">{fmtQty(r.used_qty)}</td><td>{r.supplier ?? '—'}</td></tr>)}
    </tbody></table></div> : null}
  </Modal>
}
