// 三分类结局的呈现规则。
//
// 这是 §Round 5 里最不能含糊的一段：三种情况的正确处置完全相反，界面按
// 同一套文案渲染就会让用户在数据可能已损坏时再点一次恢复。所以这里的断言
// 锁的是**行为**，不是样式。
import { describe, it, expect } from 'vitest'
import { outcomeLead, outcomeTitle, outcomeTone } from '../src/screens/backupOutcome'

const OP = '整库恢复'

describe('三分类：标题必须一眼可分', () => {
  it('三个标题互不相同', () => {
    const a = outcomeTitle(OP, 'rolled_back')
    const b = outcomeTitle(OP, 'unknown')
    const c = outcomeTitle(OP, 'applied')
    expect(a).not.toBe(b)
    expect(b).not.toBe(c)
    expect(a).not.toBe(c)
  })

  it('rolled_back 说清「数据未改动」', () => {
    expect(outcomeTitle(OP, 'rolled_back')).toContain('未改动')
  })

  it('unknown 说清「结果不明，须核对」', () => {
    expect(outcomeTitle(OP, 'unknown')).toContain('结果不明')
    expect(outcomeTitle(OP, 'unknown')).toContain('核对')
  })

  it('applied 说清「已生效」', () => {
    expect(outcomeTitle(OP, 'applied')).toContain('已生效')
  })
})

describe('三分类：主文案必须直接写明能不能重试', () => {
  it('rolled_back 鼓励重试', () => {
    const lead = outcomeLead(OP, 'rolled_back')
    expect(lead).toContain('可以重新执行')
    expect(lead).not.toContain('不要重试')
  })

  // unknown 与 applied 的主文案必须自带「不要重试」——
  // 不能指望服务端那句话被完整读��。
  it('unknown 明确写「不要重试」', () => {
    const lead = outcomeLead(OP, 'unknown')
    expect(lead).toContain('不要重试')
    expect(lead).toContain('核对')
  })

  it('applied 明确写「不要重试」并说明重试的后果', () => {
    const lead = outcomeLead(OP, 'applied')
    expect(lead).toContain('不要重试')
    expect(lead).toContain('重复')
  })
})

describe('三分类：配色', () => {
  // unknown 必须黄色，不能红色。红色会诱导人"赶紧补救"，
  // 而 unknown 的正确处置恰恰是"先别动"。
  it('unknown 是 warn（黄），不是 danger（红）', () => {
    expect(outcomeTone('unknown')).toBe('warn')
    expect(outcomeTone('unknown')).not.toBe('danger')
  })

  it('applied 是 danger（红）', () => {
    expect(outcomeTone('applied')).toBe('danger')
  })

  it('rolled_back 是中性（ok）', () => {
    expect(outcomeTone('rolled_back')).toBe('ok')
  })
})
