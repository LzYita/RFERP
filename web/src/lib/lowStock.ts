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