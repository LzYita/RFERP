import type { BatchTraceAlloc } from './batch'
import type { PartInbound } from './catalog'

/**
 * 记录投料的批次分配：本次用量按用户排的顺序摊到各入库批次上。
 *
 * 三条口径（与服务端扣料一致，改这里之前先看 internal/repository/part_inbound.go）：
 *  1. 摊法是「前面扣满才轮到后面」，不是平均分；
 *  2. 每行可用 = 该批剩余 + 本批已有分配 − 全部在产占用（占用里含本批自己，
 *     所以要先加回本批已有的，否则改一次用量就少一次可用量）；
 *  3. 摊不完的部分不分配，完成生产时按先进先出兜底——界面明说，不静默。
 */

/** 一个可分配的入库批次。 */
export interface AllocCandidate {
  inboundId: number
  batchNo: string
  /** 本次还能分配给这个批次的量（已扣掉别的在产批次占用）。 */
  available: number
}

/** 分配表里的一行：候选批次 + 本次摊到的数量。 */
export interface AllocRow extends AllocCandidate {
  qty: number
}

function round2(n: number): number {
  return Math.round(n * 100) / 100
}

/** 把 usage 按顺序摊到候选批次上；候选不够时余量不分配。 */
export function fillAllocations(usage: number, candidates: AllocCandidate[]): AllocRow[] {
  let left = round2(Math.max(0, usage))
  return candidates.map((c) => {
    const available = round2(Math.max(0, c.available))
    const qty = round2(Math.min(left, available))
    left = round2(left - qty)
    return { ...c, available, qty }
  })
}

/** 分配行的消耗后剩余。 */
export function remainingAfter(row: AllocRow): number {
  return round2(row.available - row.qty)
}

/** 拖动/上下移动一行；越界或原地不动时返回原数组。 */
export function moveRow<T>(rows: T[], from: number, to: number): T[] {
  if (from === to || from < 0 || to < 0 || from >= rows.length || to >= rows.length) {
    return rows
  }
  const next = rows.slice()
  const [row] = next.splice(from, 1)
  next.splice(to, 0, row)
  return next
}

/**
 * 某零件的候选批次：顺序先按本批已有分配（position），其余按入库先后（id 升序）。
 * 可用量 = 剩余 + 本批已有分配 − 全部在产占用；已经没可用量的批次不列。
 */
export function candidatesFor(
  inbounds: PartInbound[],
  existing?: BatchTraceAlloc[],
): AllocCandidate[] {
  const own = new Map<number, number>()
  const saved = [...(existing ?? [])].sort((a, b) => a.position - b.position)
  for (const a of saved) {
    own.set(a.inbound_id, round2((own.get(a.inbound_id) ?? 0) + a.qty))
  }
  const byId = new Map(inbounds.map((r) => [r.id, r]))
  const ordered: PartInbound[] = []
  const seen = new Set<number>()
  for (const a of saved) {
    const row = byId.get(a.inbound_id)
    if (row && !seen.has(row.id)) {
      ordered.push(row)
      seen.add(row.id)
    }
  }
  for (const row of [...inbounds].sort((a, b) => a.id - b.id)) {
    if (!seen.has(row.id)) {
      ordered.push(row)
      seen.add(row.id)
    }
  }
  const out: AllocCandidate[] = []
  for (const row of ordered) {
    const available = round2(row.remaining + (own.get(row.id) ?? 0) - (row.occupied ?? 0))
    if (available <= 0) continue
    out.push({ inboundId: row.id, batchNo: row.batch_no, available })
  }
  return out
}

/** 摊到各批次的合计；界面用它判断"还差多少要走先进先出兜底"。 */
export function allocatedTotal(rows: AllocRow[]): number {
  return round2(rows.reduce((acc, r) => acc + r.qty, 0))
}
