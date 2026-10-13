/**
 * 数量的唯一入口。
 *
 * 之前每个表单各写一份「Number.isFinite + 正则判精度」，那个判据问的是
 * 「这个字符串能不能解析成数」，而不是「这个字符串该不该被接受」。于是：
 *
 *   "1e5"    → 100000   通过   一个数量字段里出现 100000
 *   "0x10"   → 16       通过   十六进制
 *   "0b101"  → 5        通过
 *   "+5"     → 5        通过
 *
 * 后端只会拿这些数字去改库存，而校验最该守的正是这一条路径。所以闸门换成
 * **字面量**：只接受十进制数字与一个小数点，其余一律拒绝。这样「能解析」
 * 与「允许」变成同一件事，不再需要两道判断，也就不会漏。
 *
 * 精度限制同在正则里：超过两位小数在这里就被拒，而不是等后端
 * representableQuantity 兜底 —— 那时用户已经填完一整张表了。
 */

// 允许前导负号：「数量不能为负」这句提示才有机会出现。否则 -1 会被
// 「只能填数字」拦掉，用户看到的不是真正的原因。
const NUM = /^(-?)(\d*)(?:\.(\d*))?$/

/**
 * 数量显示：整数不带小数点，小数最多两位且去掉尾随零（3.50 → 3.5）。
 *
 * 放在这里而不是某个组件文件，是因为它和 parseQuantity 是同一条规则的
 * 两端——显示与解析对不齐时，用户会看到「填 3.50，存成 3.5，再打开变了」。
 * 审计摘要也要用同一套，否则同一条入库记录在列表和操作记录里数字不一样。
 */
export function fmtQty(n: number): string {
  return Number.isInteger(n) ? String(n) : String(Number(n.toFixed(2)))
}

export type NumResult = { ok: true; value: number } | { ok: false; msg: string }

export function parseQuantity(text: string, label: string): NumResult {
  const t = text.trim()
  if (t === '' || t === '-') {
    return { ok: false, msg: `请填写${label}` }
  }
  const m = NUM.exec(t)
  if (!m) {
    // 走到这里的是字母、指数、十六进制、千分位、全角数字、空格……
    return { ok: false, msg: `${label}只能填数字，不能有字母或特殊符号` }
  }
  // 小数位超限在这里拦。末尾多余的 0 不算精度：「1.500」就是 1.5。
  if ((m[3] ?? '').replace(/0+$/, '').length > 2) {
    return { ok: false, msg: `${label}最多支持两位小数` }
  }
  const value = Number(t)
  if (!Number.isFinite(value)) {
    return { ok: false, msg: `${label}超出可表示范围` }
  }
  return { ok: true, value }
}

/** 精度判断单列出来，供零件表单复用（它不关心负负，只关心小数位）。 */
export function hasTooManyDecimals(text: string): boolean {
  const m = NUM.exec(text.trim())
  if (!m) return false
  return (m[3] ?? '').replace(/0+$/, '').length > 2
}