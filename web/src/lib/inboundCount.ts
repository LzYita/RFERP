import type { PartInbound } from './catalog'
import { parseQuantity } from './num'

/**
 * 逐批次盘点的纯逻辑：把批次台账变成可编辑的行，并校验/汇总成请求体。
 *
 * 单独放在 lib 里而不是跟着弹窗走：这些函数要被 vitest 直接测，
 * 混在 .tsx 里会让该文件无法只导出组件（Fast Refresh 失效，oxlint 也会警告）。
 */
export interface CountRow {
  inbound: PartInbound
  /** 输入框里的原文，空串表示未改动。 */
  raw: string
}

function fmt2(n: number): string {
  return Number.isInteger(n) ? String(n) : n.toFixed(2)
}

/** 旧批次在前：与"先进先出"的扣料顺序一致，清点也按先进先清。 */
export function countRowsOf(rows: ReadonlyArray<PartInbound>): CountRow[] {
  return [...rows]
    .sort((a, b) => a.id - b.id)
    .map((inbound) => ({ inbound, raw: fmt2(inbound.remaining) }))
}

export type CountCheck =
  | { ok: true; items: { inbound_id: number; remaining: number }[]; delta: number }
  | { ok: false; msg: string }

/**
 * 校验并汇总：只提交改动过的批次，差异取各批之和。
 *
 * "没有改动"被明确拒掉而不是发空请求——服务端会回一句"没有要盘点的批次"，
 * 那不如当场说清。
 */
export function validateCounts(rows: ReadonlyArray<CountRow>): CountCheck {
  const items: { inbound_id: number; remaining: number }[] = []
  let delta = 0
  for (const row of rows) {
    const n = parseQuantity(row.raw, `批次 ${row.inbound.batch_no} 的实盘数量`)
    if (!n.ok) return n
    if (n.value < 0) return { ok: false, msg: '实盘数量不能为负' }
    if (n.value !== row.inbound.remaining) {
      delta += n.value - row.inbound.remaining
      items.push({ inbound_id: row.inbound.id, remaining: n.value })
    }
  }
  if (items.length === 0) return { ok: false, msg: '没有改动，无需提交' }
  return { ok: true, items, delta: Math.round(delta * 100) / 100 }
}
