import type { BatchTraceAlloc } from './batch'
import type { PartInbound } from './catalog'

/**
 * 记录投料的批次分配：把本批这个零件的用量分到各入库批次上。
 *
 * 四条口径（与服务端扣料一致，改这里之前先看 internal/repository/part_inbound.go）：
 *  1. 默认按入库时间**由远到近**排列，按顺序摊：前一批扣满才轮到下一批；
 *  2. 每行可用 = 该批剩余 + 本批已有分配 − 全部在产占用（占用里含本批自己，
 *     所以要先加回本批已有的，否则改一次数量就少一次可用量）；
 *  3. 各行数量可以手改，但合计必须等于用量；库存不够时允许把可用量分完，
 *     余下部分完成时按先进先出扣——界面明说，不静默；
 *  4. 已保存过的记录会带出当时的顺序与数量（用量没变的前提下）。
 */

/** 一个可分配的入库批次。 */
export interface AllocCandidate {
  inboundId: number
  batchNo: string
  /** 本次还能分配给这个批次的量（已扣掉别的在产批次占用）。 */
  available: number
  /** 入库时间（ISO），决定默认的先后顺序。 */
  createdAt: string
}

/** 分配表里的一行：候选批次 + 本次分配的数量。 */
export interface AllocRow extends AllocCandidate {
  qty: number
}

/** 一行的分配结果：合计是否等于用量。 */
export type PartCheck =
  | { level: 'ok'; sum: number; msg: string }
  | { level: 'short'; sum: number; msg: string }
  | { level: 'mismatch'; sum: number; msg: string }

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

/** 分配合计。 */
export function allocatedTotal(rows: AllocRow[]): number {
  return round2(rows.reduce((acc, r) => acc + r.qty, 0))
}

/** 全部候选的可用量合计（判断"库存不够分"时用）。 */
export function availableTotal(rows: AllocRow[]): number {
  return round2(rows.reduce((acc, r) => acc + r.available, 0))
}

/**
 * 把输入框里的文字变成合法数量：负号/字母按 0，超过可用量按可用量封顶。
 * 单批零件只有一个候选时，封顶就是唯一合法值，不会再写出负数或超量。
 */
export function clampQty(text: string, available: number): number {
  const raw = Number(text.trim())
  if (!Number.isFinite(raw) || raw <= 0) return 0
  return round2(Math.min(raw, Math.max(0, available)))
}

/** 修改某一行的本次数量（越界时按可用量封顶）。 */
export function setRowQty(rows: AllocRow[], index: number, text: string): AllocRow[] {
  return rows.map((r, i) => (i === index ? { ...r, qty: clampQty(text, r.available) } : r))
}

/**
 * 一行零件的分配结论：
 *  - ok：合计 == 用量；
 *  - short：库存不够分（合计 == 全部可用 < 用量），完成时余量走先进先出；
 *  - mismatch：既不等于用量、也没把可用量分完 —— 必须改到一致才能保存。
 */
export function checkPart(usage: number, rows: AllocRow[]): PartCheck {
  const use = round2(Math.max(0, usage))
  const sum = allocatedTotal(rows)
  const avail = availableTotal(rows)
  if (Math.abs(sum - use) < 0.005) {
    return { level: 'ok', sum, msg: `合计 ${fmt(sum)} / 用量 ${fmt(use)}` }
  }
  if (use > avail + 0.005 && Math.abs(sum - avail) < 0.005) {
    return {
      level: 'short',
      sum,
      msg: `合计 ${fmt(sum)} / 用量 ${fmt(use)}：库存不够分，余下 ${fmt(round2(use - sum))} 完成时按先进先出扣`,
    }
  }
  const diff = round2(use - sum)
  return {
    level: 'mismatch',
    sum,
    msg:
      diff > 0
        ? `合计 ${fmt(sum)} / 用量 ${fmt(use)}：还差 ${fmt(diff)}，请补齐或点「按顺序消耗」`
        : `合计 ${fmt(sum)} / 用量 ${fmt(use)}：超出 ${fmt(round2(-diff))}`,
  }
}

function fmt(n: number): string {
  return Number.isInteger(n) ? String(n) : String(Number(n.toFixed(2)))
}

/**
 * 某零件的候选批次：本批已保存过的分配按原顺序在前，其余按入库时间由远到近。
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
  const rest = [...inbounds].sort((a, b) => {
    const ta = Date.parse(a.created_at)
    const tb = Date.parse(b.created_at)
    if (ta !== tb) return ta - tb
    return a.id - b.id
  })
  for (const row of rest) {
    if (!seen.has(row.id)) {
      ordered.push(row)
      seen.add(row.id)
    }
  }
  const out: AllocCandidate[] = []
  for (const row of ordered) {
    const available = round2(row.remaining + (own.get(row.id) ?? 0) - (row.occupied ?? 0))
    if (available <= 0) continue
    out.push({
      inboundId: row.id,
      batchNo: row.batch_no,
      available,
      createdAt: row.created_at,
    })
  }
  return out
}

/**
 * 打开弹窗时每行的初始状态：
 * 用量没变就沿用保存过的数量（手改过的分配不该被重算掉），否则按顺序重摊。
 */
export function initialAllocations(
  usage: number,
  candidates: AllocCandidate[],
  existing?: BatchTraceAlloc[],
): AllocRow[] {
  const saved = [...(existing ?? [])].sort((a, b) => a.position - b.position)
  if (saved.length === 0) return fillAllocations(usage, candidates)
  const savedSum = round2(saved.reduce((acc, a) => acc + a.qty, 0))
  if (Math.abs(savedSum - round2(Math.max(0, usage))) >= 0.005) {
    return fillAllocations(usage, candidates)
  }
  const byId = new Map(saved.map((a) => [a.inbound_id, a]))
  return candidates.map((c) => {
    const a = byId.get(c.inboundId)
    const qty = a ? Math.min(a.qty, c.available) : 0
    return { ...c, qty: round2(qty) }
  })
}
