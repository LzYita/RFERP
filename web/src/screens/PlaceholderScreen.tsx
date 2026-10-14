/**
 * 未知屏幕的兜底。
 *
 * Round 1–6 期间它是「尚未迁移」的显式说明，逐屏交付完成后已无业务用途；
 * 保留它是为了兜住「导航项与注册表不一致」（新增了模块却忘了在 registry
 * 里注册）这一类失误：那种情况下白屏会被当成加载失败或权限问题来排查，
 * 而这里会直接把那个键名摆在屏幕上。
 */
export function PlaceholderScreen({ title }: { title: string }) {
  return (
    <div className="grid flex-1 place-items-center">
      <div className="text-center">
        <div className="mb-1.5 text-[14px] font-semibold">{title}</div>
        <div className="text-[12px] text-[var(--color-fg-muted)]">
          没有这个屏幕——导航项与屏幕注册表不一致。
        </div>
      </div>
    </div>
  )
}
