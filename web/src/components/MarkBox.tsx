import mark from '../assets/rferp-mark.png'

/**
 * MarkBox 只画标的线条：用 alpha 遮罩上色，**从不带底板**。
 *
 * 颜色由调用方给（`bg-white` 用于青底、`bg-[var(--color-primary)]` 用于画布或侧栏），
 * 因为 mask 只取 alpha：换色就是换一个 background-color，不会在边缘留下第二种颜色。
 */
export function MarkBox({ className }: { className: string }) {
  return (
    <div
      aria-hidden
      className={className}
      style={{
        aspectRatio: '912 / 679',
        maskImage: `url(${mark})`,
        maskSize: 'contain',
        maskRepeat: 'no-repeat',
        maskPosition: 'center',
        WebkitMaskImage: `url(${mark})`,
        WebkitMaskSize: 'contain',
        WebkitMaskRepeat: 'no-repeat',
      }}
    />
  )
}
