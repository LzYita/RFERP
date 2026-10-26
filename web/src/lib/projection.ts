/**
 * 库存投影的接口与派生数据。
 *
 * 投影回答的是"这些未完成批次做完之后还剩多少"，与「当前库存」是两个时间维度。
 * 数字由服务端计算（复用与完成扣料相同的 bomConsume），前端只做展示与筛选，
 * 避免两套公式给出不同结论。
 */
import { api } from './api'

export interface ProjectionHolder {
  /** 在产批次 id；建批预览里"计划中"的那一批为 0。 */
  batch_id?: number
  batch_no: string
  qty: number
  planned?: boolean
}

export interface PartProjection {
  part_id: number
  stock: number
  warn: number
  committed: number
  after: number
  holders?: ProjectionHolder[]
}

export interface PartReserveBrief {
  part_id: number
  code: string
  name: string
  after: number
  warn: number
}

export type ReserveLevel = 'ok' | 'low' | 'short'

export interface BatchReserve {
  batch_id: number
  level: ReserveLevel
  parts?: PartReserveBrief[]
}

export interface StockProjectionResult {
  parts: PartProjection[]
  batches: BatchReserve[]
  summary: { willWarn: number; willShort: number }
}

export const projection = {
  /** 可选地并入一批"还没创建的计划"，用于建批预览。 */
  get: async (planned?: { productId: number; planQty: number }): Promise<StockProjectionResult> => {
    const q = planned
      ? `?product_id=${planned.productId}&plan_qty=${planned.planQty}`
      : ''
    const res = await api.get<StockProjectionResult | null>(`/api/stock/projection${q}`)
    return {
      parts: res?.parts ?? [],
      batches: res?.batches ?? [],
      summary: res?.summary ?? { willWarn: 0, willShort: 0 },
    }
  },
}

/** 建批预览的一行：这一批会用到的零件 + 现有/占用/本批/完成后。 */
export interface PreviewRow {
  partId: number
  code: string
  name: string
  unit: string
  stock: number
  /** 其它在产批次已占用的量（不含本批）。 */
  others: number
  /** 本批（计划中）需要的量。 */
  planned: number
  /** 全部在产（含本批）完成后的余量。 */
  after: number
  warn: number
  level: 'ok' | 'low' | 'short'
}

/**
 * 从投影响应里挑出"本批会用到的零件"，组成预览行。
 *
 * 服务端把计划中的那一批标成 `planned: true`，所以不必再拉一次 BOM：
 * 有 planned 占用项的零件，就是这一批的用料。零件编码/名称/单位来自
 * 调用方已有的零件列表（投影只回数字）。
 */
export function buildPreviewRows(
  res: StockProjectionResult,
  parts: readonly { id: number; code: string; name: string; unit?: string }[],
): PreviewRow[] {
  const byId = new Map(parts.map((p) => [p.id, p]))
  const rows: PreviewRow[] = []
  for (const p of res.parts) {
    const plannedHolders = (p.holders ?? []).filter((h) => h.planned)
    if (plannedHolders.length === 0) continue
    const planned = plannedHolders.reduce((n, h) => n + h.qty, 0)
    const meta = byId.get(p.part_id)
    const level: PreviewRow['level'] =
      p.after < 0 ? 'short' : p.warn > 0 && p.after < p.warn ? 'low' : 'ok'
    rows.push({
      partId: p.part_id,
      code: meta?.code ?? `#${p.part_id}`,
      name: meta?.name ?? '',
      unit: meta?.unit ?? '',
      stock: p.stock,
      others: Math.max(0, p.committed - planned),
      planned,
      after: p.after,
      warn: p.warn,
      level,
    })
  }
  // 缺料在前，其次按完成后余量升序；同档内按编码稳定排序。
  const rank = { short: 0, low: 1, ok: 2 }
  rows.sort((a, b) => rank[a.level] - rank[b.level] || a.after - b.after || a.code.localeCompare(b.code))
  return rows
}

/** 批次列表角标的文案与色调。 */
export const RESERVE_TEXT: Record<ReserveLevel, string> = {
  ok: '充足',
  low: '将预警',
  short: '不足',
}
