import {
  cloneElement,
  isValidElement,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from 'react'

/**
 * 列表屏的输入与弹层基元。
 *
 * Round 3 之前没有这些：`Btn` 之外一切控件都是各屏内联的 Tailwind 字符串，
 * 于是同一个「26px 输入框」在 PartsScreen、AuditScreen、LoginScreen 里各存一份
 * 副本，已经漂移出三种高度（26 / 30 / 无固定高度）。三张编辑表单要复用同一套
 * 控件，再散落副本只会更乱。
 *
 * 密度是刻意的两档，不要互相借用：
 *  - **26px / 11.5px**：列表屏与弹窗内的控件（筛选、表格、编辑表单）
 *  - 30px / 13px：登录与首启向导这类独立全屏表单
 * 弹窗属于列表屏语境，因此走 26px 一档。
 */

const inputCls =
  'h-[26px] w-full rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-2.5 text-[11.5px] outline-none focus:border-[var(--color-primary)] disabled:bg-[var(--color-canvas)] disabled:text-[var(--color-fg-subtle)]'

/**
 * 自绘下拉的面板与选项样式。
 *
 * SearchSelect 与 ComboInput 共用这两条，不各写一份：这两个组件的分歧只该是
 * 「怎么选值」（对象 vs 自由文本），面板长相不该有两套，否则改样式时必然漏掉
 * 其中一个——分类下拉就是这么变成孤例的。
 */
const menuCls =
  'absolute z-50 mt-1 max-h-[180px] w-full overflow-auto rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] py-0.5 shadow-md'
const menuItemCls =
  'block w-full px-2.5 py-1 text-left text-[11.5px] hover:bg-[var(--color-primary-subtle)]'

export function TextInput({
  value,
  onChange,
  placeholder,
  mono,
  disabled,
  autoFocus,
  maxLength,
  onEnter,
  'aria-label': ariaLabel,
}: {
  value: string
  onChange: (v: string) => void
  placeholder?: string
  mono?: boolean
  disabled?: boolean
  autoFocus?: boolean
  // 与后端字段长度上限对齐时用（如入库备注 200 字），浏览器原生截断。
  maxLength?: number
  onEnter?: () => void
  /** FormRow 会注入；替换 placeholder 作为可读名称。 */
  'aria-label'?: string
}) {
  return (
    <input
      className={inputCls}
      aria-label={ariaLabel}
      style={mono ? { fontFamily: 'Consolas, monospace' } : undefined}
      value={value}
      placeholder={placeholder}
      disabled={disabled}
      autoFocus={autoFocus}
      maxLength={maxLength}
      onChange={(e) => onChange(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === 'Enter' && onEnter) {
          e.preventDefault()
          onEnter()
        }
      }}
    />
  )
}

export function Select<T extends string>({
  value,
  onChange,
  options,
  disabled,
  'aria-label': ariaLabel,
}: {
  value: T
  onChange: (v: T) => void
  options: readonly (T | { value: T; label: string })[]
  disabled?: boolean
  /** FormRow 会注入；原生 select 没有可读名称时读屏只能念选项值。 */
  'aria-label'?: string
}) {
  return (
    <select
      className={inputCls}
      aria-label={ariaLabel}
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value as T)}
    >
      {options.map((o) => {
        const v = typeof o === 'string' ? o : o.value
        const label = typeof o === 'string' ? o : o.label
        return (
          <option key={v} value={v}>
            {label}
          </option>
        )
      })}
    </select>
  )
}

/**
 * 可输入的下拉框，对应 Fyne 的 widget.NewSelectEntry。
 *
 * 用于「单位」「分类」这类「给了几个常用值，但允许自定义」的字段。纯 <select>
 * 做不到：库里已有的值不在常用列表里时，用户会发现自己无法保持原值——
 * 打开表单看到的是别的选项，一保存就把数据改了。
 *
 * options 只是候选项，**不限制**输入。
 *
 * 这里原来用的是 `<datalist>`，实测在 WebView2 里弹不出来（`input.list`
 * 绑定成功、选项也在，但原生弹层不出现），于是分类字段只显示当前那一个
 * 值、点不开也选不了。datalist 的弹层是浏览器原生 UI，不受页面样式控制，
 * 在 WebView2 里表现不稳定——它不该被用来承载一个必须能用的控件。
 *
 * 改为自绘下拉：样式与其它控件一致，行为可预测，也不依赖宿主浏览器的
 * datalist 实现。代价是多了约 40 行，以及 Esc / 点击外部 / ↑↓ 这几件
 * 本来就该做的事。
 */
export function ComboInput({
  value,
  onChange,
  options,
  placeholder,
  mono,
  autoFocus,
  allowCustomValue = true,
  'aria-label': ariaLabel,
}: {
  value: string
  onChange: (v: string) => void
  options: readonly string[]
  placeholder?: string
  mono?: boolean
  autoFocus?: boolean
  /**
   * 允许把输入原文当成一条"库中原值"候选。
   *
   * 默认 true（编辑已有字段时，库里可能存着不在候选里的值，必须能看见）。
   * 纯搜索框要设 false：输入 "P-1" 时多出一条 "P-1（库中原值）" 会让人
   * 以为能选，点了却选不中任何实体。
   */
  allowCustomValue?: boolean
  // FormRow 会把这个注进来（包裹式 label 不足以给出稳定的可访问名称）
  'aria-label'?: string
}) {
  const [open, setOpen] = useState(false)
  const boxRef = useRef<HTMLDivElement>(null)
  // 给 aria-controls 一个稳定 id。（datalist 那版也用过 useId，
  // 但问题出在 WebView2 的原生弹层，与这个 id 无关。）
  const listId = useId()

  // 已填的值不在候选里时，要先把它列出来，否则用户会以为自己填错了。
  // allowCustomValue=false 的纯搜索框不列——那条不是一个可选项。
  const items = useMemo(
    () => (allowCustomValue && value && !options.includes(value) ? [value, ...options] : [...options]),
    [allowCustomValue, options, value],
  )

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (boxRef.current && !boxRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  return (
    <div className="relative" ref={boxRef}>
      <input
        className={inputCls + ' pr-7'}
        style={mono ? { fontFamily: 'Consolas, monospace' } : undefined}
        value={value}
        placeholder={placeholder}
        autoFocus={autoFocus}
        aria-label={ariaLabel}
        role="combobox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-autocomplete="list"
        onFocus={() => setOpen(true)}
        // 展开必须也绑 onClick：选中后面板关了、输入框却仍有焦点，
        // 只靠 onFocus 的话用户「改主意再点一次」不会有任何反应。
        onClick={() => setOpen(true)}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown') {
            e.preventDefault()
            setOpen(true)
          }
          // Esc 优先关下拉，且不许再往上冒。
          //
          // Modal 也监听 document 上的 Escape，两个监听器挂在同一个节点上，
          // stopPropagation 拦不住同节点的另一个监听器（要 stopImmediate-
          // Propagation，而那依赖注册顺序）。React 的合成事件挂在根容器上，
          // 早于 document 冒泡，在这里停住传播才稳定：下拉关掉，弹窗和已填
          // 内容都留着。
          if (e.key === 'Escape' && open) {
            e.stopPropagation()
            setOpen(false)
          }
        }}
        onChange={(e) => onChange(e.target.value)}
      />
      <button
        type="button"
        // 纯装饰：焦点与展开都由输入框承担，别让读屏多念一个按钮
        aria-hidden="true"
        tabIndex={-1}
        onMouseDown={(e) => e.preventDefault()}
        onClick={(e) => {
          // preventDefault 不能省：FormRow 是包裹式 <label>，label 会把内部
          // 点击「激活」到它的控件上。不拦的话，小三角 toggle 关闭之后立刻
          // 又被转发来的一次 click 打开，看起来就是永远关不上。
          e.preventDefault()
          e.stopPropagation()
          setOpen((v) => !v)
        }}
        className="absolute top-1/2 right-1 flex h-[20px] w-6 -translate-y-1/2 items-center justify-center rounded-[3px] text-[9px] text-[var(--color-fg-subtle)] hover:bg-[var(--color-canvas)] hover:text-[var(--color-fg)]"
      >
        {open ? '▲' : '▼'}
      </button>

      {open ? (
        <ul id={listId} role="listbox" className={menuCls}>
          {items.map((o) => (
            <li
              key={o}
              role="option"
              aria-selected={o === value}
              // preventDefault：不抢输入框焦点，否则失焦后 onFocus 把面板开回来，用户点不干净。
              // 它同时拦掉 label 的点击转发——否则选中后面板会被转发来的
              // click 立刻重新打开，用户看到的是「选了就弹回来」。
              onMouseDown={(e) => e.preventDefault()}
              onClick={(e) => {
                e.preventDefault()
                onChange(o)
                setOpen(false)
              }}
              className={[
                menuItemCls,
                'cursor-pointer',
                // 选中项不该只靠 hover 撑着：面板可能一直开着，
                // 始终要能一眼看出当前值是哪条。
                o === value ? 'bg-[var(--color-primary-subtle)] font-medium text-[var(--color-primary)]' : '',
              ].join(' ')}
            >
              {o}
              {/* 当前库里存的值不在候选里时标出来，免得被当成脏数据删掉 */}
              {o === value && !options.includes(value) ? (
                <span className="ml-1.5 text-[10px] text-[var(--color-fg-subtle)]">（库中原值）</span>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  )
}

export function Checkbox({
  checked,
  onChange,
  label,
  disabled,
}: {
  checked: boolean
  onChange?: (v: boolean) => void
  label: string
  disabled?: boolean
}) {
  return (
    <label
      className="flex h-[26px] cursor-pointer select-none items-center gap-1.5 text-[11.5px]"
      style={disabled ? { opacity: 0.55, cursor: 'default' } : undefined}
    >
      <input
        type="checkbox"
        className="m-0 accent-[var(--color-primary)]"
        checked={checked}
        // 只读展示（disabled）时 onChange 可以不给——它本来就点不动
        disabled={disabled}
        onChange={(e) => onChange?.(e.target.checked)}
      />
      {label}
    </label>
  )
}

/**
 * 口令可见性图标。
 *
 * 原本定义在 LoginScreen 里，SetupScreen 各自复制过一份。Round 6 收进
 * components/form.tsx —— 三处不同步就会是「登录页是眼睛、用户管理页是
 * 闭眼」这种只有对比才发现得了的问题。
 *
 * **hidden 的语义是反的**：传 true 画闭眼、false 画睁眼。
 * 曾经把 showPwd 直接传进去，于是点「显示密码」后图标变成闭眼，看着像
 * "现在被遮住了"，与实际情况相反。所以调用方一律传 `!shown`。
 *
 * 无障碍标签描述**动作**（点了会发生什么），图标描述**状态**，两者分工不同。
 */
export function EyeIcon({ hidden }: { hidden: boolean }) {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {hidden ? (
        <>
          <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94" />
          <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" />
          <path d="M14.12 14.12a3 3 0 1 1-4.24-4.24" />
          <line x1="2" y1="2" x2="22" y2="22" />
        </>
      ) : (
        <>
          <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
          <circle cx="12" cy="12" r="3" />
        </>
      )}
    </svg>
  )
}

/**
 * 口令输入。
 *
 * Round 6 之前没有这个基元，于是 LoginScreen、SetupScreen 各手写了一份
 * <input type="password">，用的是自己复制的 className——与 TextInput 的
 * 样式已经开始漂移。
 *
 * 要点：
 *  - 眼睛按钮是 `type="button"`，且**不包在 <label> 里**：包进去点一下会
 *    连带切换焦点，而用户点它多半是想直接操作输入框。
 *  - Edge/WebView2 在字段有内容时会自己画一个 `::-ms-reveal`，与我们的眼睛
 *    并排且随焦点闪烁。`index.css` 用 `::-ms-reveal { display:none }` 屏蔽，
 *    这里不要再重复处理。
 */
export function PasswordInput({
  value,
  onChange,
  placeholder,
  autoFocus,
  onEnter,
}: {
  value: string
  onChange: (v: string) => void
  placeholder?: string
  autoFocus?: boolean
  onEnter?: () => void
}) {
  const [shown, setShown] = useState(false)
  return (
    <div className="relative">
      <input
        type={shown ? 'text' : 'password'}
        className={inputCls + ' pr-8'}
        value={value}
        placeholder={placeholder}
        autoFocus={autoFocus}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && onEnter) {
            e.preventDefault()
            onEnter()
          }
        }}
      />
      <button
        type="button"
        onClick={() => setShown((v) => !v)}
        aria-label={shown ? '隐藏密码' : '显示密码'}
        aria-pressed={shown}
        title={shown ? '隐藏密码' : '显示密码'}
        className="absolute top-1/2 right-1 flex h-[22px] w-[26px] -translate-y-1/2 items-center justify-center rounded-[4px] text-[var(--color-fg-subtle)] hover:bg-[var(--color-canvas)] hover:text-[var(--color-primary)]"
      >
        <EyeIcon hidden={!shown} />
      </button>
    </div>
  )
}

/** 数字输入：保留自由文本，失焦/提交时才校验，避免边打边报错。 */
export function NumberInput({
  value,
  onChange,
  placeholder,
  step,
  autoFocus,
  disabled,
  ariaLabel,
}: {
  value: string
  onChange: (v: string) => void
  placeholder?: string
  step?: string
  autoFocus?: boolean
  disabled?: boolean
  /** 表格里成排的数字输入没有可见 label，用可访问名区分（逐批次盘点）。 */
  ariaLabel?: string
}) {
  return (
    <input
      className={inputCls + ' tabular-nums'}
      inputMode="decimal"
      step={step}
      autoFocus={autoFocus}
      value={value}
      placeholder={placeholder}
      disabled={disabled}
      aria-label={ariaLabel}
      onChange={(e) => onChange(e.target.value)}
    />
  )
}

/**
 * 弹窗。
 *
 * 与 Fyne 的 dialog 对应，但不做「拖到边缘关掉」——桌面应用里点击遮罩关闭足够，
 * 拖拽行为在小窗口下容易把表单半路丢掉。
 *
 * Esc 关闭、上锁遮罩、打开时锁 body 滚动。三个都不是装饰：编辑表单可能有很多行，
 * 锁滚动能避免底层表格跟着一起滚。
 */
export function Modal({
  title,
  onClose,
  children,
  footer,
  width = 440,
  dismissible = true,
  zIndex = 50,
  topMost = true,
}: {
  title: string
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
  width?: number
  /**
   * 层级。二级弹窗（在弹窗里再开一个）要盖住父弹窗，传更大的值。
   */
  zIndex?: number
  /**
   * 是否响应 Esc 与遮罩点击。二级弹窗打开时父弹窗要传 false，
   * 否则一次 Esc 会把两层一起关掉——用户以为自己只取消了一层。
   */
  topMost?: boolean
  /**
   * false 时隐藏右上角的 ✕、Esc 与点遮罩都不再关闭。
   *
   * 留着一个按了没反应的 ✕ 比没有它更糟——用户会以为程序卡住了。不可取消
   * 的弹窗就只给一个出口。
   */
  dismissible?: boolean
}) {
  useEffect(() => {
    if (!dismissible || !topMost) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose, dismissible, topMost])

  useEffect(() => {
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = prev
    }
  }, [])

  return (
    <div
      className="fixed inset-0 flex items-center justify-center bg-[rgba(15,23,42,0.42)] p-4"
      style={{ zIndex }}
      role="dialog"
      aria-modal="true"
      aria-label={title}
      onMouseDown={(e) => {
        if (dismissible && topMost && e.target === e.currentTarget) onClose()
      }}
    >
      <div
        className="flex max-h-full w-full flex-col overflow-hidden rounded-[8px] bg-[var(--color-surface)] shadow-lg"
        style={{ maxWidth: width }}
      >
        <header className="flex items-center justify-between border-b border-[var(--color-border)] px-4 py-2.5">
          <h2 className="text-[13px] font-semibold">{title}</h2>
          {dismissible ? (
            <button
              type="button"
              aria-label="关闭"
              className="text-[14px] leading-none text-[var(--color-fg-subtle)] hover:text-[var(--color-fg)]"
              onClick={onClose}
            >
              ✕
            </button>
          ) : (
            <span className="w-[14px]" />
          )}
        </header>
        <div className="min-h-0 flex-1 overflow-auto px-4 py-3.5">{children}</div>
        {footer ? (
          <footer className="flex justify-end gap-2 border-t border-[var(--color-border)] bg-[#fafbfd] px-4 py-2.5">
            {footer}
          </footer>
        ) : null}
      </div>
    </div>
  )
}

/** 弹窗内的一行标签 + 控件。 */
export function FormRow({
  label,
  children,
  hint,
}: {
  label: string
  children: ReactNode
  hint?: string
}) {
  // 包裹式 <label> 下，控件的可访问名称等于整段 label 文本。ComboInput 的候选
  // 面板也渲染在这段 label 里面，一展开名称就会变成「分类 电子 五金 塑料 …」，
  // 读屏用户听到的是一串候选值。这里显式钉一个 aria-label，名称不再漂移。
  const child = isValidElement(children) ? (
    cloneElement(children as ReactElement<{ 'aria-label'?: string }>, {
      'aria-label': (children.props as { 'aria-label'?: string })['aria-label'] ?? label,
    })
  ) : (
    children
  )
  return (
    <label className="mb-2.5 flex flex-col gap-[3px]">
      <span className="text-[11px] text-[var(--color-fg-muted)]">{label}</span>
      {child}
      {hint ? <span className="text-[10.5px] text-[var(--color-fg-subtle)]">{hint}</span> : null}
    </label>
  )
}

export function FormError({ text }: { text: string }) {
  if (!text) return null
  return (
    <div
      role="alert"
      className="mb-2.5 rounded-[6px] border border-[var(--color-danger)]/30 bg-[var(--color-danger-bg)] px-2.5 py-1.5 text-[11.5px] text-[var(--color-danger)]"
    >
      {text}
    </div>
  )
}

/**
 * 二次确认。删除是不可撤销的，不能只靠一个「删除」按钮就执行。
 *
 * `onCancel` 可选：省略时只有一个确认按钮，且 Esc 与点遮罩都不再关闭。
 * 会话失效就是这种情形——会话已经没了，「取消」没有可返回的状态，
 * 摆一个关不掉的按钮只会让人反复去按它。
 */
export function ConfirmDialog({
  title,
  message,
  danger,
  confirmLabel = '确定',
  busy,
  error,
  onConfirm,
  onCancel,
}: {
  title: string
  message: ReactNode
  danger?: boolean
  confirmLabel?: string
  busy?: boolean
  /**
   * 确认动作失败时的错误文案。
   *
   * 没有这一格时，失败只把错误塞进调用方的 state，而那个 state 只在别的弹窗里
   * 渲染——用户点了「确定」看到弹窗还在、什么都没发生，会以为程序卡住。
   */
  error?: string
  onConfirm: () => void
  onCancel?: () => void
}) {
  const dismiss = () => onCancel?.()
  return (
    <Modal
      title={title}
      onClose={dismiss}
      dismissible={Boolean(onCancel)}
      width={360}
      footer={
        <>
          {onCancel ? (
            <button
              type="button"
              disabled={busy}
              className="inline-flex h-[26px] items-center rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-3 text-[11.5px] hover:bg-[var(--color-canvas)]"
              onClick={onCancel}
            >
              取消
            </button>
          ) : null}
          <button
            type="button"
            disabled={busy}
            autoFocus
            className={
              'inline-flex h-[26px] items-center rounded-[6px] px-3 text-[11.5px] text-white disabled:opacity-60 ' +
              (danger
                ? 'border border-[var(--color-danger)] bg-[var(--color-danger)]'
                : 'border border-[var(--color-primary)] bg-[var(--color-primary)]')
            }
            onClick={onConfirm}
          >
            {busy ? '处理中…' : confirmLabel}
          </button>
        </>
      }
    >
      {error ? (
        <div
          role="alert"
          className="mb-2 max-h-[180px] overflow-auto whitespace-pre-wrap rounded-[6px] border border-[var(--color-danger)]/30 bg-[var(--color-danger-bg)] px-2.5 py-1.5 text-[11.5px] text-[var(--color-danger)]"
        >
          {error}
        </div>
      ) : null}
      <div className="text-[12px] leading-relaxed text-[var(--color-fg)]">{message}</div>
    </Modal>
  )
}

/**
 * 搜索选择器：输入编码/名称过滤，点选一项。
 *
 * 对应 Fyne 的 internal/ui/searchselect.go。保留它的「四级解析阶梯」，
 * 因为老用户的肌肉记忆是「直接把完整编码敲进去」：
 *
 *   1. 明确点选过 → 就是它
 *   2. 输入文本与某项 label 忽略大小写相等 → 是它
 *   3. 过滤后只剩一项 → 自动采纳（敲了个唯一编码，直接回车就能过）
 *   4. 否则 → 未选定，调用方必须报错而不是猜一个
 *
 * 第 4 条是关键：**宁可不选，也不猜**。猜错会让用户把 BOM 挂到别的产品上。
 */
export function SearchSelect<T extends { id: number; label: string }>({
  all,
  value,
  onChange,
  placeholder = '输入编码/名称查询',
  disabled,
}: {
  all: readonly T[]
  value: T | null
  onChange: (v: T | null) => void
  placeholder?: string
  disabled?: boolean
}) {
  const [text, setText] = useState('')
  const [open, setOpen] = useState(false)
  const boxRef = useRef<HTMLDivElement>(null)

  // value 由父级持有（清空选择时要一并清掉输入框），但输入过程是本地的：
  // 受控成 value.label 会让用户没法编辑——每敲一个字都会被回显覆盖。
  // 于是用「本地的上一次 value」做同步触发。
  //
  // 这个 setState-in-effect 是 lint 会报的那一类，属于刻意取舍：要同时满足
  // 「父级能清空」与「用户能自由输入」，就必须有一条从 prop 回灌本地的路径。
  // 纯派生的写法（直接用 value.label）会把输入框锁死。
  const [echo, setEcho] = useState<string | null>(null)
  const seen = useRef<T | null>(null)
  useEffect(() => {
    if (seen.current === value) return
    seen.current = value
    if (value) setEcho(value.label)
    else setText('')
    setOpen(false)
  }, [value])

  const shown = text.trim() === '' ? [] : filter(all, text)

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (boxRef.current && !boxRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  return (
    <div className="relative" ref={boxRef}>
      <input
        className={inputCls}
        value={echo ?? text}
        placeholder={placeholder}
        disabled={disabled}
        onFocus={() => {
          if (echo !== null) {
            setText(echo)
            setEcho(null)
          }
          if (text.trim() !== '') setOpen(true)
        }}
        onChange={(e) => {
          setEcho(null)
          setText(e.target.value)
          setOpen(e.target.value.trim() !== '')
        }}
      />
      {open && shown.length > 0 ? (
        <ul className={menuCls}>
          {shown.map((o) => (
            <li key={o.id}>
              <button
                type="button"
                className={menuItemCls}
                onClick={() => {
                  onChange(o)
                  setText(o.label)
                  setEcho(o.label)
                  setOpen(false)
                }}
              >
                {o.label}
              </button>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  )
}

function filter<T extends { label: string }>(all: readonly T[], q: string): T[] {
  const n = q.trim().toLowerCase()
  return all.filter((o) => o.label.toLowerCase().includes(n))
}

/**
 * 把「输入框里现在是什么」解析成一个确定的对象，或 null。
 *
 * 与 SearchSelect 分开，是因为**解析必须在提交时做**，不能靠组件内部状态：
 * 弹窗可能整段重挂载，组件状态不该是判定的唯一依据。
 */
export function resolvePick<T extends { id: number; label: string }>(
  all: readonly T[],
  picked: T | null,
  text: string,
): T | null {
  if (picked) return picked
  const t = text.trim().toLowerCase()
  if (t === '') return null
  const exact = all.find((o) => o.label.toLowerCase() === t)
  if (exact) return exact
  const m = all.filter((o) => o.label.toLowerCase().includes(t))
  return m.length === 1 ? m[0] : null
}

/** 弹窗内两列栅格：短字段并排，省纵向空间。 */
export function FormGrid({ children }: { children: ReactNode }) {
  return <div className="grid grid-cols-2 gap-x-2.5">{children}</div>
}