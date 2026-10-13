/**
 * 审计记录的中文标签、字段级变更与摘要。
 *
 * 这是 internal/ui/audit.go 的**逐字移植**（`tableLabel`/`actionLabel`
 * `auditFieldLabels`/`auditFieldOrder`/`auditValue`/`auditFields`/`auditDiff`/
 * `auditChanges`/`changeText`/`objectFromMap`/`auditObject`/`shortSummary`）。
 *
 * 为什么不放后端：表名与动作已经在审计行里原样返回，后端加一个展示字段只会
 * 让同一份语义有两处可改。但也不能在前端各屏各写一份——键是复数表名
 * （products / bom_items / product_batches ...）与全大写动作（INSERT / STOCK_DEDUCT ...），
 * 按单数或小写凭直觉写会全部落到 default，同一条记录在不同屏显示不同中文，
 * 用户会以为是两套数据。故集中在此，Dashboard 与 Audit 两屏共用。
 *
 * 移植时踩过的坑，别再踩回去：
 *
 * 1. **列表的「变更明细」用的是 changeText(auditChanges(a))，不是 shortSummary。**
 *    shortSummary 只有工作台在用。曾经把这两者当成同一个东西，结果审计列表
 *    退化成"只认得 code/name/batch_no 的身份摘要"——出入库、盘点、状态变更、
 *    整库操作一律显示 `#记录id`，而 `old_data`/`new_data` 里明明有全部信息。
 * 2. **对象列（auditObject）与类型列（tableLabel）是两件事。** 前者是"这条记录
 *    是谁"（[编码] 名称 / 批次号 / 零件批次号 / 批次#x 零件#y），后者是"落在哪张表"。
 *    合并成"只显示表名"会让 BOM、投料这类按 id 关联的记录无法辨认。
 * 3. `auditValue` 对 status 分两套：批次表走五态，其余表 1=启用/否则停用。
 *    统一成启用/停用会把批次的「已撤销」显示成「停用」。
 */

import { statusText } from './batch'
import { fmtQty } from './num'

/** 审计行里 old_data/new_data 的形状：后端直接落库的 JSON 对象。 */
export type AuditData = Record<string, unknown> | null | undefined

export function tableLabel(t: string): string {
  const map: Record<string, string> = {
    products: '产品',
    parts: '零件',
    bom_items: 'BOM',
    product_batches: '批次',
    batch_trace: '批次追溯',
    batch_skip_parts: '跳过零件',
    database: '整库操作',
  }
  return map[t] ?? t
}

export function actionLabel(a: string): string {
  const map: Record<string, string> = {
    INSERT: '新增',
    UPDATE: '修改',
    DELETE: '删除',
    STOCK_IN: '入库',
    STOCK_DEDUCT: '出库',
    STOCK_ADJUST: '盘点',
    REVOKE: '撤销',
    UPDATE_STATUS: '状态变更',
    RESTORE: '整库恢复',
    CLEAR: '清空业务数据',
  }
  return map[a] ?? a
}

/** 字段中文标签（audit.go:491-500）。缺省回退键名，与旧版一致。 */
const auditFieldLabels: Record<string, string> = {
  code: '编码',
  name: '名称',
  spec: '规格',
  unit: '单位',
  part_type: '分类',
  stock_qty: '库存',
  warn_qty: '预警',
  supplier: '供应商',
  status: '状态',
  quantity: '用量',
  loss_rate: '损耗率',
  remark: '备注',
  replaceable: '可替换',
  use_mode: '用量模式',
  batch_no: '批次号',
  plan_qty: '计划数量',
  produced_qty: '完成数量',
  customer: '客户',
  part_batch_no: '零件批次号',
  used_qty: '使用数量',
  in_qty: '入库数量',
  deduct: '消耗数量',
  old_stock: '原库存',
  new_stock: '新库存',
  diff: '差异',
  batch_id: '批次ID',
  part_id: '零件ID',
  product_id: '产品ID',
}

export function auditFieldLabel(k: string): string {
  return auditFieldLabels[k] ?? k
}

/**
 * 字段显示顺序（audit.go:502-509）。
 *
 * 顺序影响可读性：按业务上"先身份、后属性、最后状态"排，用户扫一行就知道
 * 改的是哪个零件的哪一项。键是复数表名，与 tableLabel 同源。
 */
const auditFieldOrder: Record<string, readonly string[]> = {
  products: ['code', 'name', 'spec', 'unit', 'status'],
  parts: ['code', 'name', 'spec', 'unit', 'part_type', 'stock_qty', 'warn_qty', 'status', 'supplier'],
  bom_items: ['product_id', 'part_id', 'quantity', 'loss_rate', 'replaceable', 'use_mode', 'remark'],
  product_batches: ['batch_no', 'product_id', 'plan_qty', 'produced_qty', 'status', 'customer'],
  batch_trace: ['part_batch_no', 'used_qty', 'supplier'],
  batch_skip_parts: ['batch_id', 'part_id'],
}

const DEFAULT_FIELD_ORDER = ['code', 'name', 'batch_no', 'part_batch_no'] as const

export function auditFieldOrderFor(table: string): readonly string[] {
  return auditFieldOrder[table] ?? DEFAULT_FIELD_ORDER
}

/** 把 JSON 里的数值统一成 number（对齐 Go 侧 toFloat64 的宽容度）。 */
function toNum(v: unknown): number {
  if (typeof v === 'number') return v
  if (typeof v === 'string') {
    const n = Number.parseFloat(v)
    return Number.isFinite(n) ? n : 0
  }
  return 0
}

/**
 * 值渲染（audit.go:525-569）。
 *
 * 整数不带小数点、非整数保留两位；status/replaceable/use_mode 走枚举文案。
 * Go 侧默认分支是 `%v`，对复合值会打出 `map[...]` 这种读不了的东西；
 * 这里改用 JSON——同样不常见，但至少是能看的。
 */
export function auditValue(table: string, field: string, v: unknown): string {
  if (v === null || v === undefined) return ''
  if (field === 'status') {
    const n = Math.trunc(toNum(v))
    // 批次是五态，其余表是二态。混用会把「已撤销」说成「停用」。
    if (table === 'product_batches') return statusText(n)
    return n === 1 ? '启用' : '停用'
  }
  if (field === 'replaceable') return Math.trunc(toNum(v)) === 1 ? '是' : '否'
  if (field === 'use_mode') {
    return Math.trunc(toNum(v)) === 1 ? '每M台用1个' : '每台用N个'
  }
  switch (typeof v) {
    case 'number':
      return Number.isInteger(v) ? String(v) : v.toFixed(2)
    case 'string':
      return v
    case 'boolean':
      return v ? '是' : '否'
    default:
      return JSON.stringify(v)
  }
}

export interface ChangeItem {
  Field: string
  Old: string
  New: string
}

/** 新增/删除：按字段顺序列出非空字段（audit.go:615-629）。 */
export function auditFields(table: string, m: AuditData): ChangeItem[] {
  if (!m) return []
  const out: ChangeItem[] = []
  for (const k of auditFieldOrderFor(table)) {
    const v = m[k]
    if (v === null || v === undefined) continue
    out.push({ Field: auditFieldLabel(k), Old: '', New: auditValue(table, k, v) })
  }
  return out
}

/** 修改：按字段顺序比较，只输出真正变了的（audit.go:631-652）。 */
export function auditDiff(table: string, oldM: AuditData, newM: AuditData): ChangeItem[] {
  if (!oldM || !newM) return []
  const out: ChangeItem[] = []
  for (const k of auditFieldOrderFor(table)) {
    const hasOld = oldM[k] !== null && oldM[k] !== undefined
    const hasNew = newM[k] !== null && newM[k] !== undefined
    if (!hasOld && !hasNew) continue
    const os = auditValue(table, k, oldM[k])
    const ns = auditValue(table, k, newM[k])
    if (hasOld && hasNew && os === ns) continue
    out.push({ Field: auditFieldLabel(k), Old: os, New: ns })
  }
  return out
}

/**
 * 按动作分派变更明细（audit.go:654-702）。
 *
 * 库存类动作的 new_data 里没有 code/name，只有 in_qty/deduct/old_stock/new_stock——
 * 这正是"只看身份字段的摘要"完全失效的地方。
 */
export function auditChanges(log: {
  table_name: string
  action: string
  new_data?: AuditData
  old_data?: AuditData
}): ChangeItem[] {
  const { table_name: table, action, new_data: nd, old_data: od } = log
  if (action === 'STOCK_IN' || action === 'STOCK_DEDUCT' || action === 'STOCK_ADJUST') {
    const out: ChangeItem[] = []
    if (nd) {
      for (const k of ['in_qty', 'deduct']) {
        const v = nd[k]
        if (v !== null && v !== undefined) {
          out.push({ Field: auditFieldLabel(k), Old: '', New: auditValue(table, k, v) })
        }
      }
      const hasOld = nd.old_stock !== null && nd.old_stock !== undefined
      const hasNew = nd.new_stock !== null && nd.new_stock !== undefined
      if (hasOld && hasNew) {
        out.push({
          Field: '库存',
          Old: auditValue(table, 'old_stock', nd.old_stock),
          New: auditValue(table, 'new_stock', nd.new_stock),
        })
      }
    }
    return out
  }
  if (action === 'UPDATE_STATUS') {
    return [
      {
        Field: '状态',
        Old: od ? auditValue(table, 'status', od.status) : '',
        New: nd ? auditValue(table, 'status', nd.status) : '',
      },
    ]
  }
  if (action === 'INSERT') return auditFields(table, nd)
  if (action === 'DELETE') return auditFields(table, od)
  if (action === 'UPDATE') return auditDiff(table, od, nd)
  if (od && nd) {
    const d = auditDiff(table, od, nd)
    if (d.length > 0) return d
  }
  if (nd) return auditFields(table, nd)
  if (od) return auditFields(table, od)
  return []
}

/** 列表列文本：「字段：旧 → 新；…」，缺一侧只显示另一侧（audit.go:704-722）。 */
export function changeText(items: readonly ChangeItem[]): string {
  if (items.length === 0) return '—'
  return items
    .map((it) => {
      if (it.Old === '' && it.New === '') return it.Field
      if (it.Old === '') return `${it.Field}：${it.New}`
      if (it.New === '') return `${it.Field}：${it.Old}`
      return `${it.Field}：${it.Old} → ${it.New}`
    })
    .join('；')
}

/** 从 old/new_data 里推断记录身份（audit.go:580-613）。 */
export function objectFromMap(m: AuditData): string {
  if (!m) return ''
  const code = typeof m.code === 'string' ? m.code : ''
  const name = typeof m.name === 'string' ? m.name : ''
  if (code !== '' || name !== '') {
    if (code !== '' && name !== '') return `[${code}] ${name}`
    return name !== '' ? name : code
  }
  if (typeof m.batch_no === 'string' && m.batch_no !== '') return m.batch_no
  if (typeof m.part_batch_no === 'string' && m.part_batch_no !== '') return m.part_batch_no
  const pid = m.part_id
  if (pid !== null && pid !== undefined) {
    const bid = m.batch_id
    if (bid !== null && bid !== undefined) return `批次#${bid} / 零件#${pid}`
    return `零件#${pid}`
  }
  const prodId = m.product_id
  if (prodId !== null && prodId !== undefined) return `产品#${prodId}`
  return ''
}

/** 对象列：new 优先，其次 old，兜底表名（audit.go:571-578）。 */
export function auditObject(log: {
  table_name: string
  new_data?: AuditData
  old_data?: AuditData
}): string {
  for (const m of [log.new_data, log.old_data]) {
    const s = objectFromMap(m)
    if (s !== '') return s
  }
  return tableLabel(log.table_name)
}

// ---------------------------------------------------------------- 摘要（工作台用）

function getFloat(m: Record<string, unknown>, key: string): number {
  const v = m[key]
  if (v === undefined) return 0
  return toNum(v)
}

function fmtNum(n: number): string {
  // 用全站统一的 fmtQty，而不是照抄旧版的 %.0f：旧版会把 0.5 显示成 1，
  // 在**审计**里四舍五入等于抹掉证据。
  return fmtQty(n)
}

/** 取路径末段：操作记录里显示备份文件名而不是整条路径（audit.go:416-425）。 */
function baseName(p: string): string {
  if (p === '') return ''
  const norm = p.replace(/\//g, '\\')
  const i = norm.lastIndexOf('\\')
  return i >= 0 ? norm.slice(i + 1) : norm
}

function partCodeName(m: AuditData): string {
  if (!m) return ''
  const code = typeof m.code === 'string' ? m.code : ''
  const name = typeof m.name === 'string' ? m.name : ''
  if (code !== '' && name !== '') return `[${code}]${name} `
  if (code !== '') return `[${code}] `
  if (name !== '') return `${name} `
  return ''
}

function partSupplier(m: AuditData): string {
  if (!m) return ''
  const s = typeof m.supplier === 'string' ? m.supplier : ''
  return s !== '' ? `(${s}) ` : ''
}

/**
 * 一行摘要（audit.go:253-414）。工作台的「内容」列用它，审计屏用 changeText。
 *
 * 按 (表, 动作) 分派，共 12 类分支：批次状态变更/新建/撤销、出入库/盘点、
 * 投料、通用增删改、整库恢复与清空。最后两级兜底才是 code/name/batch_no 与表名。
 */
export function shortSummary(log: {
  table_name: string
  record_id: number
  action: string
  new_data?: AuditData
  old_data?: AuditData
}): string {
  const { table_name: table, action, new_data: nd, old_data: od } = log

  if (table === 'product_batches' && action === 'UPDATE_STATUS') {
    let batchPart = ''
    let cust = ''
    if (od) {
      batchPart = `批次 ${od.batch_no}`
      const pc = od.product_code
      if (pc !== null && pc !== undefined && String(pc) !== '') {
        batchPart += ` [${pc}]${od.product_name}`
      }
      if (typeof od.customer === 'string' && od.customer !== '') cust = ` 客户:${od.customer}`
    }
    if (nd) {
      const s = String(nd.status)
      return `${batchPart} → ${statusText(Number(s))}${cust}`
    }
  }

  if (table === 'product_batches' && action === 'INSERT' && nd) {
    const bn = `批次 ${nd.batch_no}`
    const pc = nd.product_code
    const prodInfo =
      pc !== null && pc !== undefined && String(pc) !== ''
        ? ` [${pc}]${nd.product_name}`
        : ''
    const pq = `计划${nd.plan_qty}`
    const cu = nd.customer !== null && nd.customer !== undefined && String(nd.customer) !== ''
      ? `客户:${nd.customer}`
      : ''
    return `${bn}${prodInfo} ${pq} ${cu}`.trim()
  }

  if (table === 'product_batches' && action === 'REVOKE' && nd) {
    const cust = typeof nd.customer === 'string' && nd.customer !== '' ? ` 客户:${nd.customer}` : ''
    return `撤销批次 ${nd.batch_no} [${statusText(Math.trunc(toNum(nd.status)))} 计划${fmtNum(toNum(nd.plan_qty))}]${cust}`
  }

  if (action === 'STOCK_IN' && nd) {
    return `${partCodeName(od)}${partSupplier(od)}入库 ${fmtNum(getFloat(nd, 'in_qty'))}，当前 ${fmtNum(getFloat(nd, 'new_stock'))}`
  }
  if (action === 'STOCK_DEDUCT' && nd) {
    return `${partCodeName(od)}${partSupplier(od)}生产消耗 ${fmtNum(getFloat(nd, 'deduct'))}，库存 ${fmtNum(getFloat(nd, 'old_stock'))}→${fmtNum(getFloat(nd, 'new_stock'))}`
  }
  if (action === 'STOCK_ADJUST' && nd) {
    return `${partCodeName(od)}${partSupplier(od)}${fmtNum(getFloat(nd, 'old_stock'))} → ${fmtNum(getFloat(nd, 'new_stock'))}`
  }

  if (table === 'batch_trace' && action === 'INSERT' && nd) {
    const pbn = String(nd.part_batch_no ?? '')
    const q = fmtNum(getFloat(nd, 'used_qty'))
    if (pbn !== '' && pbn !== '<nil>') return `记录投料 ${pbn}，用量 ${q}`
    return `记录投料，用量 ${q}`
  }

  if (action === 'INSERT' && nd) {
    const sup = partSupplier(nd)
    if (nd.name !== undefined) return `新增: ${nd.name} ${sup}`
    if (nd.code !== undefined) return `新增: ${nd.code} ${sup}`
  }

  if (action === 'DELETE' && od) {
    const sup = partSupplier(od)
    if (od.name !== undefined) return `删除: ${od.name} ${sup}`
    if (od.code !== undefined) return `删除: ${od.code} ${sup}`
  }

  if (action === 'UPDATE' && od && nd) {
    // 与旧版一致的字段顺序与中文标签（stock_qty→库存 等）
    const changes: string[] = []
    for (const k of [
      'code', 'name', 'spec', 'unit', 'part_type',
      'stock_qty', 'warn_qty', 'supplier', 'status',
    ]) {
      const ov = od[k]
      const nv = nd[k]
      if (ov !== undefined && nv !== undefined && String(ov) !== String(nv)) {
        const label =
          k === 'stock_qty' ? '库存'
          : k === 'warn_qty' ? '预警'
          : k === 'part_type' ? '分类'
          : k === 'supplier' ? '供应商'
          : k === 'status' ? '状态'
          : k
        changes.push(`${label} ${ov}→${nv}`)
      }
    }
    if (changes.length > 0) return changes.join(', ')
    if (nd.name !== undefined) return `修改: ${nd.name}`
  }

  // 整库操作：摘要要能回答"恢复到哪一份 / 清空前留了什么"
  if (table === 'database' && nd) {
    if (action === 'RESTORE') {
      let out = '整库恢复'
      if (nd.file !== undefined) out += `自 ${baseName(String(nd.file))}`
      if (nd.statements !== undefined) out += `（${nd.statements} 条记录）`
      return out
    }
    if (action === 'CLEAR') {
      let out = '清空业务数据'
      if (nd.pre_clear !== undefined) out += `，清空前副本 ${baseName(String(nd.pre_clear))}`
      return out
    }
  }

  if (nd) {
    // 兜底键与旧版一致（code/name/batch_no）。web 版曾额外探过 username，
    // 但没有任何被审计的表带这个字段（用户管理不写审计，见 service 侧无
    // `TableName: "users"`），探它只会掩盖真正的兜底失效。
    const vals: string[] = []
    for (const k of ['code', 'name', 'batch_no']) {
      if (nd[k] !== undefined) {
        vals.push(String(nd[k]))
        if (vals.length >= 2) break
      }
    }
    if (vals.length > 0) return vals.join(' ')
  }

  return `${tableLabel(table)}操作`
}
