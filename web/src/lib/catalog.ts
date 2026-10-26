/**
 * 目录（产品 / 零件 / BOM）的类型与请求封装。
 *
 * 三条必须记住的约定，写错就是 400 或静默丢字段：
 *
 * 1. **POST/PUT 的 body 有 `DisallowUnknownFields`**（internal/api/dto.go）。
 *    多传一个键、少拼一个字母都直接 400，所以这里显式列出全部字段，
 *    不做「把对象整个扔过去」。
 *
 * 2. **PUT 必须带 `version`**，且要带 GET 读回来的那个值。后端用
 *    `WHERE id=? AND version=?` 做乐观锁（repository.go:513），不传就是
 *    version=0，除非这行恰好还是第 0 版，否则必然 400。用户会看到
 *    「并发版本冲突」，但真正的原因是前端没传。
 *
 * 3. **`status` 是 `*int`**。不传 = 新建时默认启用、更新时保留原值。
 *    这就是 Fyne 版「编辑产品会把停用的产品悄悄启用」那个 bug 在 HTTP 层的
 *    正确写法（internal/api/dto.go:165-179）。
 *
 * `POST /api/bom` 是例外：它直接反序列化 `model.BOMItem`，**没有**
 * DisallowUnknownFields，字段拼错会被静默丢弃。所以下面同样写全字段名。
 */

import { api } from './api'

export interface Product {
  id: number
  code: string
  name: string
  spec?: string
  unit: string
  status: number
  version: number
  created_at?: string
  updated_at?: string
  operator?: string
}

export interface Part {
  id: number
  code: string
  name: string
  spec?: string
  unit: string
  part_type?: string
  stock_qty: number
  warn_qty: number
  status: number
  version: number
  created_at?: string
  updated_at?: string
  operator?: string
  supplier?: string
}

export interface BOMItem {
  id: number
  product_id: number
  part_id: number
  quantity: number
  loss_rate: number
  remark?: string
  replaceable: number
  use_mode: number
  version: number
  part_code?: string
  part_name?: string
}

/** 空串 → undefined，让后端落 NULL 而不是空字符串。 */
function opt(s: string): string | undefined {
  const t = s.trim()
  return t === '' ? undefined : t
}

export interface ProductWrite {
  code: string
  name: string
  spec?: string
  unit: string
  status?: number
  id?: number
  version?: number
}

export interface PartWrite {
  code: string
  name: string
  spec?: string
  unit: string
  part_type?: string
  stock_qty: number
  warn_qty: number
  status?: number
  supplier?: string
  id?: number
  version?: number
}

export const products = {
  list: () => api.get<Product[]>('/api/products'),
  get: (id: number) => api.get<Product>(`/api/products/get?id=${id}`),
  create: (b: ProductWrite) => api.post<Product>('/api/products', b),
  update: (b: ProductWrite & { id: number; version: number }) =>
    api.put<Product>('/api/products', b),
  remove: (id: number) => api.del(`/api/products?id=${id}`),
}

export const parts = {
  list: () => api.get<Part[]>('/api/parts'),
  get: (id: number) => api.get<Part>(`/api/parts/get?id=${id}`),
  create: (b: PartWrite) => api.post<Part>('/api/parts', b),
  update: (b: PartWrite & { id: number; version: number }) => api.put<Part>('/api/parts', b),
  remove: (id: number) => api.del(`/api/parts?id=${id}`),
  stockIn: (part_id: number, qty: number) =>
    api.post<Part>('/api/parts/stock-in', { part_id, qty }),
  adjustStock: (part_id: number, new_qty: number) =>
    api.post<Part>('/api/parts/adjust-stock', { part_id, new_qty }),
}

export const bom = {
  list: async (product_id: number) => (await api.get<BOMItem[] | null>(`/api/bom?product_id=${product_id}`)) ?? [],
  add: (b: {
    product_id: number
    part_id: number
    quantity: number
    loss_rate: number
    remark?: string
    replaceable: number
    use_mode: number
  }) => api.post<BOMItem>('/api/bom', b),
  /**
   * 编辑一条 BOM 明细。**只有四个字段可改**：用量、用量模式、损耗率、可替换。
   *
   * 服务端用 DisallowUnknownFields，多传 product_id/part_id/remark 会 400 而不是
   * 被忽略——把「改数量」与「换零件」分成两种操作，是这个约束存在的意义。
   * version 取自 GET 读回的那一行（乐观锁）。
   */
  update: (b: {
    id: number
    quantity: number
    loss_rate: number
    use_mode: number
    replaceable: number
    version: number
  }) => api.put<BOMItem>('/api/bom', b),
  remove: (id: number) => api.del(`/api/bom?id=${id}`),
  validate: (product_id: number) =>
    api.get<{ valid: boolean }>(`/api/bom/validate?product_id=${product_id}`),
}

export { opt }

/** 下拉候选的展示文本：编码  名称[  规格]，与 Fyne 版一致（双空格分隔）。 */
export function pickLabel(code: string, name: string, spec?: string): string {
  return spec ? `${code}  ${name}  ${spec}` : `${code}  ${name}`
}

export const PART_TYPES = ['电子', '五金', '塑料', '硅胶', '包装辅料', '其他'] as const
export const PRODUCT_UNITS = ['个', '台', '套', '只'] as const
