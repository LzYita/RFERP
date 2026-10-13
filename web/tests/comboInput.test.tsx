// @vitest-environment jsdom
//
// ComboInput 的交互关口。
//
// 这类测试是被两次真事故逼出来的：
//   1. 展开只绑 onFocus → 选完再点输入框没反应，必须去够那个小三角
//   2. Esc 同时被下拉和 Modal 响应 → 填了一半的表单被整个丢掉
// 两者都不是「逻辑写错了」，纯函数测试看不见；只能真的挂载、真的敲键盘。
//
// 浏览器关口（web/combo-e2e.cjs）覆盖真 WebView2 行为（datalist 事故只有它
// 抓得到）；这份负责把同样的不变量钉进 CI——CI 里没有浏览器。
import { describe, it, expect, afterEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { ComboInput, FormRow, Modal } from '../src/components/form'

const TYPES = ['电子', '五金', '塑料', '硅胶', '包装辅料', '其他']

afterEach(cleanup)

/**
 * 用真实的 FormRow 组合，而不是自己套一层 label。
 * FormRow 是包裹式 <label>，下拉面板渲染在它内部——这个结构本身
 * 就影响可访问名称，绕开它就测不到。
 */
function Harness({ initial = '' }: { initial?: string }) {
  const [v, setV] = useState(initial)
  return (
    <>
      <FormRow label="其它字段">
        <input defaultValue="已填内容" />
      </FormRow>
      <FormRow label="分类">
        <ComboInput value={v} onChange={setV} options={TYPES} />
      </FormRow>
    </>
  )
}

const combo = () => screen.getByRole('combobox', { name: '分类' })
const panel = () => screen.queryByRole('listbox')

describe('ComboInput', () => {
  it('展开后列出全部候选', async () => {
    const user = userEvent.setup()
    render(<Harness />)
    await user.click(combo())
    expect(panel()).not.toBeNull()
    expect(screen.getAllByRole('option').map((o) => o.textContent)).toEqual(TYPES)
  })

  // 事故 1：展开原本只绑 onFocus，选中后输入框仍有焦点，
  // 用户「改主意再点一次」毫无反应，只能去够右边那个小三角。
  it('选过一次之后还能再点开', async () => {
    const user = userEvent.setup()
    render(<Harness />)

    await user.click(combo())
    await user.click(screen.getByRole('option', { name: '塑料' }))
    expect(panel()).toBeNull()

    await user.click(combo())
    expect(panel()).not.toBeNull()
  })

  it('选中后受控值跟着变', async () => {
    const user = userEvent.setup()
    render(<Harness />)
    await user.click(combo())
    await user.click(screen.getByRole('option', { name: '硅胶' }))
    expect(combo().value).toBe('硅胶')
  })

  it('允许自由输入（这是它区别于 <select> 的地方）', async () => {
    const user = userEvent.setup()
    render(<Harness />)
    await user.click(combo())
    await user.clear(combo())
    await user.type(combo(), '自定义分类')
    expect(combo().value).toBe('自定义分类')
  })

  it('库里已有的值不在候选里时仍然列出来并标注', async () => {
    const user = userEvent.setup()
    render(<Harness initial="库里的旧分类" />)
    await user.click(combo())
    expect(screen.getByRole('option', { name: /库里的旧分类/ })).toBeTruthy()
    expect(screen.getByText(/库中原值/)).toBeTruthy()
  })

  it('点击外部关闭', async () => {
    const user = userEvent.setup()
    render(<Harness />)
    await user.click(combo())
    expect(panel()).not.toBeNull()
    await user.click(screen.getByRole('textbox', { name: '其它字段' }))
    expect(panel()).toBeNull()
  })

  // 事故 2：下拉与 Modal 都监听 Escape，同一按键两处响应，
  // 结果是「关掉下拉」顺带把整个表单和已填内容一起丢掉。
  it('Esc 只关下拉，不连带关掉弹窗、不清空已填内容', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    render(
      <Modal title="零件" onClose={onClose} dismissible>
        <Harness />
      </Modal>,
    )

    await user.click(combo())
    expect(panel()).not.toBeNull()

    await user.keyboard('{Escape}')
    expect(panel()).toBeNull()
    expect(onClose).not.toHaveBeenCalled()
    expect(combo()).toBeTruthy()
    expect(screen.getByRole('textbox', { name: '其它字段' }).value).toBe('已填内容')
  })

  it('面板关闭时 Esc 仍交给 Modal（用户想放弃整张表单）', () => {
    const onClose = vi.fn()
    render(
      <Modal title="零件" onClose={onClose} dismissible>
        <Harness />
      </Modal>,
    )
    fireEvent.keyDown(combo(), { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  // FormRow 是包裹式 <label>，面板又渲染在它内部；若不给输入框钉一个固定的
  // 可访问名称，读屏念出来的名字会随面板开关变成「分类 电子 五金 塑料 …」。
  it('可访问名称不随面板开关漂移', async () => {
    const user = userEvent.setup()
    render(<Harness />)
    expect(combo().getAttribute('aria-label')).toBe('分类')
    await user.click(combo())
    expect(panel()).not.toBeNull()
    expect(screen.getByRole('combobox', { name: '分类' })).toBeTruthy()
  })
})
