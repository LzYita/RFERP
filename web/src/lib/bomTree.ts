import type { BOMItem, Part } from './catalog'

/** BOM 树的一个叶子：一条 BOM 明细 + 它引用的零件主数据（可能已不存在）。 */
export interface BomLeaf {
  item: BOMItem
  part: Part | null
}

/** BOM 树的一层：零件分类 + 该分类下的明细。 */
export interface BomGroup {
  name: string
  leaves: BomLeaf[]
}

/** `part_type` 为空的零件在树里的归组名。 */
export const UNTYPED = '未分类'

/**
 * 把 BOM 明细按零件分类分组，供 BOM 屏的树状表格使用。
 *
 * 分类取自**零件主数据**的 `part_type`：BOMItem 只冗余了编码与名称，
 * 没有分类字段；屏上本来就加载了全部零件，按 part_id 现查即可，不必动后端。
 *
 * 两个边界：
 * - `part_type` 为空（或全空白）的零件归入「未分类」，而不是消失——
 *   分类是自由文本，历史数据里空值真实存在，树里找不到人会让用户以为明细丢了。
 * - part_id 查不到主数据（并发删除等）同样按「未分类」兜底，明细行仍要渲染，
 *   编码/名称回落到 BOMItem 冗余的那份。
 *
 * 排序与 PartsScreen 的分类筛选同一约定：中文按 `localeCompare(…, 'zh')`，
 * 不用 Go/JS 默认码点序；「未分类」有意殿后——它是兜底桶，不是业务分类。
 * 组内叶子保持服务端返回顺序，不做二次排序。
 */
export function buildBomGroups(items: readonly BOMItem[], parts: readonly Part[]): BomGroup[] {
  const byId = new Map<number, Part>()
  for (const p of parts) byId.set(p.id, p)

  const buckets = new Map<string, BomLeaf[]>()
  for (const item of items) {
    const part = byId.get(item.part_id) ?? null
    const name = (part?.part_type ?? '').trim() || UNTYPED
    const bucket = buckets.get(name)
    const leaf: BomLeaf = { item, part }
    if (bucket) bucket.push(leaf)
    else buckets.set(name, [leaf])
  }

  return [...buckets.entries()]
    .map(([name, leaves]) => ({ name, leaves }))
    .sort((a, b) => {
      if (a.name === UNTYPED) return 1
      if (b.name === UNTYPED) return -1
      return a.name.localeCompare(b.name, 'zh')
    })
}
