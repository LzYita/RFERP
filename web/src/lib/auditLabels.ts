/**
 * 审计记录的中文标签。
 *
 * 这是 internal/ui/audit.go 里 tableLabel/actionLabel 的**逐字移植**。
 *
 * 为什么不放后端：表名与动作已经在审计行里原样返回，后端加一个展示字段只会
 * 让同一份语义有两处可改。但也不能在前端各屏各写一份——键是复数表名
 * （products / bom_items / product_batches ...）与全大写动作（INSERT / STOCK_DEDUCT ...），
 * 按单数或小写凭直觉写会全部落到 default，同一条记录在不同屏显示不同中文，
 * 用户会以为是两套数据。故集中在此，Dashboard 与 Audit 两屏共用。
 */

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

/**
 * 审计行的「变更摘要」。
 *
 * 移植 audit.go 的 shortSummary 语义：优先取新值里的关键字段，缺失时退回记录 ID。
 */
export function shortSummary(log: {
  table_name: string
  record_id: number
  new_data?: Record<string, unknown> | null
  old_data?: Record<string, unknown> | null
}): string {
  const data = log.new_data ?? log.old_data
  if (!data) return `#${log.record_id}`
  // 这些字段最能代表一条记录的身份，按优先级取第一个非空的。
  for (const key of ['code', 'name', 'batch_no', 'username']) {
    const v = data[key]
    if (typeof v === 'string' && v !== '') return v
  }
  return `#${log.record_id}`
}