/**
 * BOM 屏「上次选中的产品」的短期记忆。
 *
 * 为什么是模块级变量而不是 state / localStorage：
 * - 屏幕一离开就卸载，state 必然丢；提升到 App 层只为一屏的便利不值得，
 *   还会让 App 持有目录数据。
 * - 需求是「短暂保留」：点出去再点回来不用重选，但重开应用应当回到初始态。
 *   模块级变量正好是这个生命周期，localStorage 会跨会话留下来。
 *
 * 只记 id + 展示字段：恢复时不必等产品列表加载完就能把级联与明细拉起来。
 * 产品若已被删除，屏幕在拿到产品列表后会丢弃这份记忆（见 BOMScreen）。
 */
export interface BomSelection {
  id: number
  code: string
  name: string
  spec: string
}

let last: BomSelection | null = null

export function rememberBomSelection(s: BomSelection | null): void {
  last = s
}

export function recallBomSelection(): BomSelection | null {
  return last
}

/** 测试与「清空选择」共用：把记忆恢复到初始态。 */
export function clearBomSelection(): void {
  last = null
}
