import { useMemo } from 'react'
import {
  Modal,
  FormRow,
  FormGrid,
  FormError,
  TextInput,
  ComboInput,
  NumberInput,
  Checkbox,
} from '../components/form'
import { Btn } from '../components/ui'
import { PART_TYPES, opt, type Part, type PartWrite } from '../lib/catalog'
import { parseQuantity, hasTooManyDecimals } from '../lib/num'

/**
 * 零件新增 / 编辑弹窗。
 *
 * 与 Fyne 版（internal/ui/part.go:293-421）的两处**刻意不同**：
 *
 * 1. **库存不能在这里改，也不能在这里填**（2026-10 起）。库存只走「入库」
 *    和「盘点调整」两个独立动作，因为每一次库存变动都该留下原因与操作人；
 *    一个可以随手把 500 改成 0 的文本框会把这个约束废掉。编辑时库存只读展示，
 *    新增则从 0 起步——入库时会自动生成批次号，在零件详情里可查。
 *
 * 2. **停用状态不再被悄悄改回启用**。Fyne 的编辑框里那个「启用」复选框被
 *    创建之后直接丢掉了（product.go:196-198，part.go 同理），于是编辑一个
 *    已停用的零件会把它重新启用。这里把状态做成一个显式选择器。
 *
 * 数量校验与 Fyne 逐字对齐（part.go:324-334 / 393-397）：
 *   - 预警库存解析失败 / 为负 → 对应提示
 * 另加两条前端校验（后端也有，重复拦一次只为不必往返）：
 *   - 后端只支持两位小数（service.go:95），超过就直接报错，不如当场说清
 *   - 编码/名称为空没有后端校验会拦（CreatePart 只验数量），但空编码会
 *     立刻撞上 parts.code 的唯一约束，提示会变成一句莫名其妙的「零件编码已存在」
 */

export interface PartDraft {
  code: string
  name: string
  spec: string
  partType: string
  warn: string
  supplier: string
  unit: string
  status: number
}

export function emptyPartDraft(): PartDraft {
  return {
    code: '',
    name: '',
    spec: '',
    // Fyne 的默认值「电子」不在它的候选列表里（part.go:300-301），候选是
    // 塑料/五金/硅胶/包装辅料/其他。保留「电子」作默认是因为它是库里最常见的
    // 分类，但候选列表补齐为「与默认值一致」——否则用户选「电子」时发现下拉
    // 里根本没有这一项，还得手动敲。
    partType: '电子',
    warn: '0',
    supplier: '',
    unit: '个',
    status: 1,
  }
}

export function partToDraft(p: Part): PartDraft {
  return {
    code: p.code,
    name: p.name,
    spec: p.spec ?? '',
    partType: p.part_type ?? '',
    // 两位小数，与 Fyne 的 fmt.Sprintf("%.2f") 一致；整数不必显示成 3.00
    warn: Number.isInteger(p.warn_qty) ? String(p.warn_qty) : p.warn_qty.toFixed(2),
    supplier: p.supplier ?? '',
    unit: p.unit ?? '个',
    status: p.status,
  }
}

/** 小数位超过 2 位一律拒绝，与后端 representableQuantity 同规则。 */
export function tooManyDecimals(t: string): boolean {
  return hasTooManyDecimals(t)
}

export function validatePartDraft(d: PartDraft): { ok: true } | { ok: false; msg: string } {
  if (!d.code.trim()) return { ok: false, msg: '请填写零件编码' }
  if (!d.name.trim()) return { ok: false, msg: '请填写零件名称' }

  // 预警值在新增与编辑时都要校验
  const warn = parseQuantity(d.warn, '预警库存')
  if (!warn.ok) {
    return {
      ok: false,
      msg: warn.msg.includes('只能填数字') ? '预警库存必须是非负有效数值' : warn.msg,
    }
  }
  if (warn.value < 0) return { ok: false, msg: '预警库存必须是非负有效数值' }

  // 库存不在这里校验：新增从 0 起步，编辑只读，数量走「入库 / 盘点」。
  return { ok: true }
}

export function draftToWrite(d: PartDraft, editing: boolean, orig?: Part): PartWrite {
  const base: PartWrite = {
    // 新增固定 0（后端也拒绝带库存创建）；编辑由服务端以库中现值为准，
    // 这里的原值只是让"全量更新"语义下不产生无谓的库存字段噪音。
    stock_qty: editing && orig ? orig.stock_qty : 0,
    code: d.code.trim(),
    name: d.name.trim(),
    spec: opt(d.spec),
    unit: d.unit.trim() || '个',
    part_type: opt(d.partType),
    warn_qty: Number(d.warn),
    status: d.status,
    supplier: opt(d.supplier),
  }
  if (editing && orig) {
    base.id = orig.id
    base.version = orig.version
  }
  return base
}

export function PartFormModal({
  draft,
  setDraft,
  editing,
  orig,
  busy,
  err,
  onSubmit,
  onClose,
}: {
  draft: PartDraft
  setDraft: (d: PartDraft) => void
  editing: boolean
  orig?: Part
  busy: boolean
  err: string
  onSubmit: () => void
  onClose: () => void
}) {
  const set = (patch: Partial<PartDraft>) => setDraft({ ...draft, ...patch })
  const w = useMemo(
    () => (editing ? `当前库存 ${orig?.stock_qty ?? 0} ${orig?.unit ?? ''}` : ''),
    [editing, orig],
  )

  return (
    <Modal
      title={editing ? '编辑零件' : '新增零件'}
      onClose={onClose}
      width={460}
      footer={
        <>
          <Btn onClick={onClose}>取消</Btn>
          <Btn primary disabled={busy} onClick={onSubmit}>
            {busy ? '保存中…' : '保存'}
          </Btn>
        </>
      }
    >
      <FormError text={err} />
      <FormRow label="编码">
        <TextInput
          value={draft.code}
          onChange={(v) => set({ code: v })}
          placeholder="零件编码"
          mono
          autoFocus
          onEnter={onSubmit}
        />
      </FormRow>
      <FormRow label="名称">
        <TextInput
          value={draft.name}
          onChange={(v) => set({ name: v })}
          placeholder="零件名称"
          onEnter={onSubmit}
        />
      </FormRow>
      <FormRow label="规格">
        <TextInput
          value={draft.spec}
          onChange={(v) => set({ spec: v })}
          placeholder="规格型号（选填）"
        />
      </FormRow>
      <FormGrid>
        <FormRow label="分类">
          <ComboInput
            value={draft.partType}
            onChange={(v) => set({ partType: v })}
            options={PART_TYPES}
          />
        </FormRow>
        <FormRow label="单位">
          <TextInput value={draft.unit} onChange={(v) => set({ unit: v })} />
        </FormRow>
      </FormGrid>

      <FormRow
        label="库存"
        hint={editing ? w || undefined : '新零件从 0 起步，创建后通过「入库」添加'}
      >
        {/* 只读展示：入库/盘点才会改库存，每笔都留批次与审计，这里不给直接改。 */}
        <TextInput value={fmt2(orig?.stock_qty ?? 0)} onChange={() => undefined} disabled />
      </FormRow>
      <FormRow label="预警库存" hint="0=不预警">
        <NumberInput value={draft.warn} onChange={(v) => set({ warn: v })} />
      </FormRow>

      <FormRow label="供应商">
        <TextInput
          value={draft.supplier}
          onChange={(v) => set({ supplier: v })}
          placeholder="供应商名称（选填）"
        />
      </FormRow>

      <FormRow label="状态">
        <div className="flex items-center gap-4 pt-1">
          <Checkbox
            checked={draft.status === 1}
            onChange={(c) => set({ status: c ? 1 : 2 })}
            label="启用"
          />
        </div>
      </FormRow>
    </Modal>
  )
}

function fmt2(n: number): string {
  return Number.isInteger(n) ? String(n) : n.toFixed(2)
}