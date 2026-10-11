/**
 * 尚未迁移的屏幕占位。
 *
 * Round 1 只交付外壳与一个真实列表屏（零件管理），其余屏幕按 Round 2 起
 * 逐屏迁移。这里显式说明「还没做」，而不是渲染一个空白页——
 * 空白页会被当成加载失败或权限问题来排查。
 */
export function PlaceholderScreen({ title }: { title: string }) {
  return (
    <div className="grid flex-1 place-items-center">
      <div className="text-center">
        <div className="mb-1.5 text-[14px] font-semibold">{title}</div>
        <div className="text-[12px] text-[var(--color-fg-muted)]">
          该屏尚未迁移，按计划在后续轮次逐屏交付。
        </div>
      </div>
    </div>
  )
}
