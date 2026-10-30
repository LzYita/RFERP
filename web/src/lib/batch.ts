/**
 * 生产批次：类型、接口、状态机、消耗量计算。
 *
 * 这一屏与前三屏最大的不同是：**服务端在校验上比界面宽松**。四条必须记住的：
 *
 * 1. `GET /api/batches` 零行时返回 **null 而不是 []**（Go 的 `var list []T`
 *    从不初始化）。同理 `/api/batches/skipped`。
 *
 * 2. `POST /api/batches` 的响应**不能当权威**。服务层把请求体解到结构体、
 *    只回填了 `id`，于是 `version` 是 0、`created_at` 是零值时间、
 *    `produced_qty` 是请求里的回显。必须重新拉列表。
 *
 * 3. `CreateBatch` **不校验 status**。直接 POST `status:2` 能造出一个
 *    "已完成" 但没有冻结消耗记录的批次，而它之后就撤销不了
 *    （`批次缺少冻结的库存消耗记录，无法安全撤销`）。所以表单永远发
 *    `status: 0`，界面也不让用户选状态。
 *
 * 4. **完成生产时库存不足，服务端会拒绝**（决策 B，2026-10-13 用户定）。
 *    此前是 `actualDeduct = min(请求量, 现有库存)` 的静默夹取：扣到 0、批次照样
 *    标记完成、接口返回成功，账实不符而调用方毫无察觉。服务端现在一次列全缺料
 *    并中止。界面仍在完成**之前**再查一次（BatchScreen）——那是提示层不是安全
 *    边界：服务端拒绝的文案是纯文本拼接，界面能对齐格式并列出全部缺料项。
 *
 * 另：相同状态的更新 API 不拦（Fyne 在界面拦），所以状态机判断放在前端。
 */

import { api } from './api'
import type { BOMItem, Part, Product } from './catalog'

export interface Batch {
  id: number
  batch_no: string
  product_id: number
  plan_qty: number
  produced_qty: number
  status: number
  version: number
  created_at?: string
  updated_at?: string
  operator?: string
  customer?: string
  consumption_recorded: number
  /** 关联字段，列表接口才有；新建响应里是 undefined */
  product_name?: string
  product_code?: string
}

/** 一条投料分配：从哪笔入库批次扣、扣多少、第几顺位（0 起）。 */
export interface BatchTraceAlloc {
  inbound_id: number
  batch_no: string
  qty: number
  position: number
}

/**
 * 一个批次的投料记录：同一 (batch_id, part_id) 只有一条，再记录就是覆盖。
 * allocations 是完成生产时的扣料顺序与数量；part_batch_no 是 v18 之前的
 * 自由文本批次号，只在历史记录里出现。
 */
export interface BatchTrace {
  id: number
  batch_id: number
  part_id: number
  part_batch_no?: string
  used_qty: number
  supplier?: string
  created_at?: string
  operator?: string
  allocations?: BatchTraceAlloc[]
}

/* ---------------- 状态 ---------------- */

/** 状态常量在 Go 侧没有命名常量，硬编码在三处。别新增第四处。 */
export const BATCH_STATUS = {
  pending: 0,
  running: 1,
  done: 2,
  paused: 3,
  revoked: 4,
} as const

export type BatchStatus = (typeof BATCH_STATUS)[keyof typeof BATCH_STATUS]

export function statusText(s: number): string {
  switch (s) {
    case 0:
      return '待生产'
    case 1:
      return '生产中'
    case 2:
      return '已完成'
    case 3:
      return '已暂停'
    case 4:
      return '已撤销'
    default:
      return '未知'
  }
}

/** 徽章色沿用 Fyne 版的主题表（internal/ui/util.go:286-300）。 */
export function statusKind(s: number): 'ok' | 'warn' | 'neutral' | 'danger' {
  switch (s) {
    case 1:
      return 'neutral'
    case 2:
      return 'ok'
    case 3:
      return 'warn'
    case 4:
      return 'danger'
    default:
      return 'neutral'
  }
}

/**
 * 「这个操作现在能不能做」。
 *
 * 前端承担这个判断是因为 API 不承担：
 *  - 相同状态重复提交：API 会成功，还写一条状态没变的审计
 *  - 已撤销/已完成改状态：API 会拒，但文案是内部字符串
 *  - 已完成/已撤销改跳过零件：API 会拒，同样是内部字符串
 *
 * Fyne 在界面上拦（batch.go:170-172 / 526-529），这里保持一致，
 * 只是把文案改成说清楚是哪一种，不复用 Fyne 那句对已撤销批次不准确的
 * 「已完成的批次不能更改状态」。
 */
export function canChangeStatus(b: Batch, to: number): { ok: true } | { ok: false; msg: string } {
  if (b.status === BATCH_STATUS.revoked) {
    return { ok: false, msg: '已撤销的批次不能更改状态' }
  }
  if (b.status === BATCH_STATUS.done) {
    return { ok: false, msg: '已完成的批次不能更改状态；如需撤销请使用「撤销」' }
  }
  if (b.status === to) {
    return { ok: false, msg: `批次已处于【${statusText(to)}】状态` }
  }
  return { ok: true }
}

/** 完成/撤销后不能记录投料：投料只服务于未完成批次，改历史要撤销重开。 */
export function canRecordTrace(b: Batch): { ok: true } | { ok: false; msg: string } {
  if (b.status === BATCH_STATUS.done) {
    return { ok: false, msg: '已完成的批次不能记录投料；如需修改请撤销后重新建批' }
  }
  if (b.status === BATCH_STATUS.revoked) {
    return { ok: false, msg: '已撤销的批次不能记录投料' }
  }
  return { ok: true }
}

export function canManageParts(b: Batch): { ok: true } | { ok: false; msg: string } {
  if (b.status === BATCH_STATUS.done) {
    return { ok: false, msg: '已完成的批次不能再修改零件' }
  }
  if (b.status === BATCH_STATUS.revoked) {
    return { ok: false, msg: '已撤销的批次不能再修改零件' }
  }
  return { ok: true }
}

/* ---------------- 消耗量 ---------------- */

/**
 * 计划数量下某个 BOM 项的零件消耗量。
 *
 * 与 `internal/service/service.go` 的 `bomConsume` **逐行等价** ——
 * 界面用它预警，服务端用它扣库存，两者不一致的话预警就是假的。
 * 注意服务端扣之前还会 `quantizeQuantity`（两位小数取整），所以这里是
 * 一个下界：真实扣减可能因为取整比这个略小一点。
 *
 *   use_mode=0  每台用 N 个   → 计划数 × N × (1+损耗率%)
 *   use_mode=1  每 M 台用 1 个 → ceil(计划数/M) × (1+损耗率%)，再向上取整
 */
export function bomConsume(planQty: number, b: Pick<BOMItem, 'quantity' | 'loss_rate' | 'use_mode'>): number {
  let base = planQty * b.quantity
  if (b.use_mode === 1) {
    const m = b.quantity > 0 ? b.quantity : 1
    base = Math.ceil(planQty / m)
  }
  let consume = base * (1 + b.loss_rate / 100)
  if (b.use_mode === 1) consume = Math.ceil(consume)
  return consume
}

export interface Shortage {
  partId: number
  name: string
  code: string
  needed: number
  have: number
}

/**
 * 找出库存不足的零件。
 *
 * 与 Fyne build.go:352-361 逐条对应：
 *  - `PartID` 在零件表里找不到 → **跳过**，不报缺料（Fyne batch.go:354-356）
 *  - 判据是 `needed > stock`，严格大于：正好等于不算缺
 *  - 跳过的零件（skippedPartIds）不参与——完成时不扣它们
 *
 * Fyne 在建批次时查一次。这里在**完成前**也查一次，因为库存会变，
 * 而服务端的静默夹取不会告诉任何人。
 */
export function findShortages(
  planQty: number,
  bomItems: BOMItem[],
  parts: Part[],
  skippedPartIds: number[] = [],
): Shortage[] {
  const partMap = new Map(parts.map((p) => [p.id, p]))
  const skip = new Set(skippedPartIds)
  const out: Shortage[] = []
  for (const bi of bomItems) {
    if (skip.has(bi.part_id)) continue
    const part = partMap.get(bi.part_id)
    if (!part) continue
    const needed = bomConsume(planQty, bi)
    if (needed > part.stock_qty) {
      out.push({
        partId: bi.part_id,
        name: bi.part_name ?? '',
        code: bi.part_code ?? '',
        needed,
        have: part.stock_qty,
      })
    }
  }
  return out
}

/** Fyne 的文案格式：`%s [%s]: 需要 %.2f，当前 %.2f`（batch.go:359-360）。 */
export function shortageLine(s: Shortage): string {
  return `${s.name} [${s.code}]: 需要 ${s.needed.toFixed(2)}，当前 ${s.have.toFixed(2)}`
}

/* ---------------- 接口 ---------------- */

/**
 * 批次上的临时零件：不在产品 BOM 里、但这一批实际要用。
 * `qty` 是本批总量，完成生产时整笔扣，撤销时随消耗记录回退。
 * 派生三件套（use_mode/quantity/loss_rate）是添加表单按 BOM 行方式算总量时
 * 留下的依据；手填总量时为空，扣减永远以 `qty` 为准。
 */
export interface TempPart {
  batch_id: number
  part_id: number
  qty: number
  code: string
  name: string
  unit: string
  use_mode?: number
  quantity?: number
  loss_rate?: number
}

/** 添加临时零件时随总量一起提交的推导依据；手填总量时不传。 */
export interface TempPartDerivation {
  useMode: number
  quantity: number
  lossRate: number
}

export const batches = {
  /** 零行时后端返回 null，这里统一成 []。 */
  list: async (): Promise<Batch[]> => (await api.get<Batch[] | null>('/api/batches')) ?? [],

  /** 响应不可靠（version=0、时间零值），调用方应重新 list()。 */
  create: (b: {
    batch_no: string
    product_id: number
    plan_qty: number
    status: number
    customer?: string
  }) => api.post<Batch>('/api/batches', b),

  setStatus: (id: number, status: number) =>
    api.post<{ status: string }>('/api/batches/status', { id, status }),

  revoke: (id: number) => api.post<{ status: string }>('/api/batches/revoke', { id }),

  skipped: async (batchId: number): Promise<number[]> =>
    (await api.get<number[] | null>(`/api/batches/skipped?batch_id=${batchId}`)) ?? [],

  // 注意不对称：新增用 body，删除用查询参数。后端就是这样，不是笔误。
  addSkip: (batchId: number, partId: number) =>
    api.post<{ status: string }>('/api/batches/skip', { batch_id: batchId, part_id: partId }),
  removeSkip: (batchId: number, partId: number) =>
    api.del<{ status: string }>(`/api/batches/skip?batch_id=${batchId}&part_id=${partId}`),

  tempParts: async (batchId: number): Promise<TempPart[]> =>
    (await api.get<TempPart[] | null>(`/api/batches/temp-parts?batch_id=${batchId}`)) ?? [],
  // 同一零件重复添加 = 改数量（后端 upsert）；库存不够时后端 400 并说明缺口。
  addTempPart: (batchId: number, partId: number, qty: number, derivation?: TempPartDerivation) =>
    api.post<{ status: string }>('/api/batches/temp-parts', {
      batch_id: batchId,
      part_id: partId,
      qty,
      ...(derivation
        ? { use_mode: derivation.useMode, quantity: derivation.quantity, loss_rate: derivation.lossRate }
        : {}),
    }),
  removeTempPart: (batchId: number, partId: number) =>
    api.del<{ status: string }>(`/api/batches/temp-parts?batch_id=${batchId}&part_id=${partId}`),
}

/** 三个反查都走部分匹配：输整串时包含匹配天然只命中那一条。 */
const PARTIAL = '&match=partial'

export const traces = {
  /** 按批次号反查；部分匹配，输片段即可。 */
  byProduct: async (batchNo: string): Promise<BatchTrace[]> =>
    (await api.get<BatchTrace[] | null>(
      `/api/traces/by-product?batch_no=${encodeURIComponent(batchNo)}${PARTIAL}`,
    )) ?? [],
  /** 按零件编码或名称反查；服务端同时比对两者，部分匹配。 */
  byPart: async (value: string): Promise<BatchTrace[]> =>
    (await api.get<BatchTrace[] | null>(
      `/api/traces/by-part?part_code=${encodeURIComponent(value)}${PARTIAL}`,
    )) ?? [],
  byBatch: async (batchId: number): Promise<BatchTrace[]> =>
    (await api.get<BatchTrace[] | null>(`/api/traces/by-batch?batch_id=${batchId}`)) ?? [],
  /** 按零件入库批次号反查用了它的生产批次（RK-…），部分匹配。 */
  byPartLot: async (batchNo: string): Promise<BatchTrace[]> =>
    (await api.get<BatchTrace[] | null>(
      `/api/traces/by-part-lot?batch_no=${encodeURIComponent(batchNo)}${PARTIAL}`,
    )) ?? [],

  /**
   * 整批提交，一个事务：任一条失败则全部回滚。
   * 同一 (batch_id, part_id) 再次提交是覆盖（含批次分配），不是追加。
   */
  recordBatch: (rows: {
    batch_id: number
    part_id: number
    used_qty: number
    part_batch_no?: string
    /** 供应商不在这里传：服务端按零件台账的 supplier 写入。 */
    allocations?: { inbound_id: number; qty: number }[]
  }[]) => api.post<{ status: string }>('/api/traces/batch', rows),
}

/* ---------------- 选产品 ---------------- */

/**
 * 产品级联：名称 → 编码 → 规格。
 *
 * 与 Fyne batch.go:217-274 同构。同一套级联在 BOM 屏也用着，
 * 但两处的启用规则不同：BOM 屏不选就不能加零件，批次屏不选就不能建批次。
 */

export interface CascadeOptions {
  names: string[]
  codes: string[]
  specs: string[]
}

export function cascadeFor(products: Product[], name: string, code: string): CascadeOptions {
  const names = [...new Set(products.map((p) => p.name).filter(Boolean))].sort()
  if (!name) return { names, codes: [], specs: [] }

  const codes = [
    ...new Set(products.filter((p) => p.name === name).map((p) => p.code)),
  ]
    .filter(Boolean)
    .sort()
  if (!code) return { names, codes, specs: [] }

  const specs = [
    ...new Set(
      products
        .filter((p) => p.name === name && p.code === code && p.spec)
        .map((p) => p.spec as string),
    ),
  ].sort()
  return { names, codes, specs }
}

/**
 * 把三级选择解析成一个产品。
 *
 * 规格只在**有多个可选规格时**才参与：若某编码下存在规格，说明这几个产品
 * 同名同编码，只能靠规格区分，选第一个就是选错。
 */
export function resolveProduct(
  products: Product[],
  name: string,
  code: string,
  spec: string,
): Product | null {
  if (!name || !code) return null
  const cands = products.filter((p) => p.name === name && p.code === code)
  if (!cands.length) return null
  const specs = [
    ...new Set(cands.filter((p) => p.spec).map((p) => p.spec as string)),
  ]
  if (specs.length > 0) {
    if (!spec) return null
    return cands.find((p) => p.spec === spec) ?? null
  }
  return cands[0]
}

/** Fyne 的默认批次号：`BATCH` + `20060102150405` 格式（batch.go:277）。 */
export function defaultBatchNo(d = new Date()): string {
  const p = (x: number) => String(x).padStart(2, '0')
  return (
    'BATCH' +
    d.getFullYear() +
    p(d.getMonth() + 1) +
    p(d.getDate()) +
    p(d.getHours()) +
    p(d.getMinutes()) +
    p(d.getSeconds())
  )
}
