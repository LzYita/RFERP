/**
 * 库存档位（工作台与零件页共用）。
 *
 * 档位是**派生值**，不落库：`status` 只表达「启用/停用」（用户手动勾选，
 * `internal/api/dto.go` 的 validStatus 只接受 1 和 2），而「有没有货」是
 * `stock_qty` 推出来的。把「无库存」做成 status 的第三个取值会同时踩三个坑：
 *   - 表达不了「已停用且无库存」；
 *   - 补货后 status 得靠入库逻辑自动改回启用，否则与库存长期不一致；
 *   - API 校验、CSV 导出、审计标签、���部 Fyne 版全都要跟着改一遍，
 *     而它们默认「非 1 即停用」，新值会被一律说成「停用」。
 * 所以拆成两个正交维度：status 管启停，本模块管库存档位，渲染时叠加。
 *
 * 三档的判定必须与 Fyne 版 `stockLevel`（internal/ui/part.go:35）逐字一致：
 *
 *   1. `out`（零库存）优先于一切 —— `stock_qty <= 0`。没货是比「快没」更硬的
 *      事实，用户看到红色「无库存」比看到橙色「低库存」更清楚该做什么。
 *   2. `low`（跌破预警线）需要两条：`warn_qty > 0` 守卫 —— 预警值设为 0 的
 *      含义是「不预警」（表单里就写着 hint="0=不预警"，partForm.tsx:232），
 *      少了守卫，判定会退化成 `stock_qty <= 0`，所有未设预警线的零件都被
 *      算成断货；以及严格小于 —— 库存正好等于预警线是「已到线」不是「已跌破」。
 *   3. 其余为 `normal`。
 */

/** 只依赖判定真正需要的两个字段，便于任何含这两个字段的行对象直接传入。 */
export interface StockJudgable {
  stock_qty: number
  warn_qty: number
}

export type StockLevel = 'out' | 'low' | 'normal'

export function stockLevel(p: StockJudgable): StockLevel {
  if (p.stock_qty <= 0) return 'out'
  if (p.warn_qty > 0 && p.stock_qty < p.warn_qty) return 'low'
  return 'normal'
}

/**
 * 预警口径：**零库存也算预警**。
 *
 * 零件页的筛选、工作台的预警列表与卡片计数都用这一份。曾经三处各写各的，
 * 后果是「工作台报 15 条、零件页勾选后一条都查不出来」。
 */
export function isStockAlert(p: StockJudgable): boolean {
  return stockLevel(p) !== 'normal'
}

/** 各档的徽章文案。Fyne 版用的是「库存紧张」，此处沿用 Web 既有的「低库存」。 */
export const STOCK_LEVEL_TEXT: Record<StockLevel, string> = {
  out: '无库存',
  low: '低库存',
  normal: '正常',
}

/* ---------------- 与「在产占用」合并后的一览状态 ---------------- */

export type StockViewKind =
  | 'disabled' // 停用：优先于一切库存结论
  | 'out' // 现在就没货
  | 'will_short' // 全部在产批次完成后为负
  | 'low' // 现在已经低于预警线
  | 'will_low' // 全部在产批次完成后低于预警线
  | 'normal'

export interface StockView {
  kind: StockViewKind
  text: string
  tone: 'ok' | 'warn' | 'neutral' | 'danger'
  /** 虚线 = 预计（还没发生）；实心 = 当前事实。 */
  dashed: boolean
  /** 在产占用（未完成批次将消耗的量），无投影时为 0。 */
  committed: number
  /** 全部在产批次完成后的余量，无投影时等于当前库存。 */
  after: number
  /** 悬停说明：用数字讲清「为什么是这个状态」。 */
  hint: string
}

/**
 * 把「当前库存」与「在产占用」合成一个可直接渲染的状态。
 *
 * 优先级（自上而下命中即止）决定同一列里谁能覆盖谁：
 *   停用 → 无库存 → 将缺料 → 低库存 → 将低于预警线 → 正常
 *
 * 两条容易写错、已在测试里钉住：
 *   - 红压黄、**当下压未来**：现在没货比"做完会缺"更急；
 *   - 预警线为 0 表示"不预警"，但"将缺料"仍然成立——不预警 ≠ 允许欠料。
 *
 * 没有投影数据时（接口失败 / 尚未加载）退化为原有三档，绝不因为拿不到
 * 预测就把零件显示成"正常"。
 */
export function stockView(
  p: StockJudgable & { status?: number; unit?: string },
  projection?: { committed: number; after: number } | null,
): StockView {
  const committed = projection?.committed ?? 0
  // 没有投影时 after 用当前库存：既有的三档判断不受影响。
  const after = projection?.after ?? p.stock_qty
  const unit = p.unit ? ` ${p.unit}` : ''
  const fmt = (n: number) => (Number.isInteger(n) ? String(n) : n.toFixed(2))
  // "可用"就是全部在产批次完成后的余量：用户补货/排产看的是这个数，
  // 而不只是"现在有多少"。
  const hint =
    committed > 0
      ? `现有 ${fmt(p.stock_qty)}${unit}，在产占用 ${fmt(committed)}${unit}，可用 ${fmt(after)}${unit}（预警线 ${fmt(p.warn_qty)}${unit}）`
      : `现有库存 ${fmt(p.stock_qty)}${unit}（预警线 ${fmt(p.warn_qty)}${unit}）`

  if (p.status !== undefined && p.status !== 1) {
    return { kind: 'disabled', text: '停用', tone: 'neutral', dashed: false, committed, after, hint }
  }
  if (p.stock_qty <= 0) {
    return { kind: 'out', text: '无库存', tone: 'danger', dashed: false, committed, after, hint }
  }
  if (after < 0) {
    return { kind: 'will_short', text: '将缺料', tone: 'danger', dashed: true, committed, after, hint }
  }
  if (p.warn_qty > 0 && p.stock_qty < p.warn_qty) {
    return { kind: 'low', text: '低库存', tone: 'warn', dashed: false, committed, after, hint }
  }
  if (p.warn_qty > 0 && after < p.warn_qty) {
    return { kind: 'will_low', text: '将预警', tone: 'warn', dashed: true, committed, after, hint }
  }
  return { kind: 'normal', text: '正常', tone: 'ok', dashed: false, committed, after, hint }
}

/** 「在产后将预警」筛选口径：预测维度的两档（缺料也算）。 */
export function isProjectedAlert(v: StockView): boolean {
  return v.kind === 'will_short' || v.kind === 'will_low'
}

/**
 * 「只看现在」的状态：停用 / 无库存 / 低库存 / 正常。
 *
 * 工作台的「现状预警」必须用它，而不是合并后的 stockView：
 * 一个零件现在 40 个（低于预警线 100）、做完在产后会缺 30 个，
 * 在"现状"这一栏里应当显示**低库存**。若显示"将缺料"，这一栏就名不副实——
 * 用户看到的是未来结论，却以为在说眼下。
 */
export function currentStockView(
  p: StockJudgable & { status?: number; unit?: string },
): StockView {
  const unit = p.unit ? ` ${p.unit}` : ''
  const fmt = (n: number) => (Number.isInteger(n) ? String(n) : n.toFixed(2))
  const hint = `现有库存 ${fmt(p.stock_qty)}${unit}（预警线 ${fmt(p.warn_qty)}${unit}）`
  const base = { committed: 0, after: p.stock_qty, hint }
  if (p.status !== undefined && p.status !== 1) {
    return { kind: 'disabled', text: '停用', tone: 'neutral', dashed: false, ...base }
  }
  if (p.stock_qty <= 0) {
    return { kind: 'out', text: '无库存', tone: 'danger', dashed: false, ...base }
  }
  if (p.warn_qty > 0 && p.stock_qty < p.warn_qty) {
    return { kind: 'low', text: '低库存', tone: 'warn', dashed: false, ...base }
  }
  return { kind: 'normal', text: '正常', tone: 'ok', dashed: false, ...base }
}

/**
 * 「只看以后」的状态：将缺料 / 将预警；两者都不成立时返回 null。
 *
 * 工作台的「未来预警」用它：这一栏里的每一行都必须带"将"字与虚线，
 * 不能混进"低库存"这类现在时的结论。
 */
export function projectedStockView(
  p: StockJudgable & { status?: number; unit?: string },
  projection: { committed: number; after: number },
): StockView | null {
  const unit = p.unit ? ` ${p.unit}` : ''
  const fmt = (n: number) => (Number.isInteger(n) ? String(n) : n.toFixed(2))
  const hint = `现有 ${fmt(p.stock_qty)}${unit}，在产占用 ${fmt(projection.committed)}${unit}，可用 ${fmt(projection.after)}${unit}（预警线 ${fmt(p.warn_qty)}${unit}）`
  const base = { committed: projection.committed, after: projection.after, hint }
  if (projection.after < 0) {
    return { kind: 'will_short', text: '将缺料', tone: 'danger', dashed: true, ...base }
  }
  if (p.warn_qty > 0 && projection.after < p.warn_qty) {
    return { kind: 'will_low', text: '将预警', tone: 'warn', dashed: true, ...base }
  }
  return null
}