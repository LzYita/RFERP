/**
 * 帮助文档的渲染器：把 lib/markdown.ts 切好的分块画成元素。
 *
 * 只认内置文档用到的子集（见 lib/markdown.ts 的说明），其余写法按**字面量**
 * 渲染——渲染成文本而不是删掉：万一内容越界，用户看到的是 "| a | b |" 而不是
 * 空白，一眼就知道是内容没跟上约束。
 *
 * 输出是 React 元素而不是 HTML 字符串，所以文档内容**无法注入标签**，
 * 不需要、也不允许用 dangerouslySetInnerHTML。
 */
import { Fragment, type ReactNode } from 'react'
import { parseBlocks } from '../lib/markdown'

/**
 * 渲染行内标记：**粗体** 与 `行内代码`。
 *
 * 两者都不含嵌套（内容子集保证了这一点），所以扫一遍就够。
 * 未闭合的标记按字面量渲染，不吞字符。
 */
function inline(text: string, keyBase: string): ReactNode[] {
  const parts = text.split(/(\*\*[^*]+\*\*|`[^`]+`)/g)
  return parts
    .filter((p) => p !== '')
    .map((p, i) => {
      const key = `${keyBase}-${i}`
      if (p.startsWith('**') && p.endsWith('**') && p.length > 4) {
        return (
          <strong key={key} className="font-semibold">
            {p.slice(2, -2)}
          </strong>
        )
      }
      if (p.startsWith('`') && p.endsWith('`') && p.length > 2) {
        return (
          <code key={key} className="rounded-[3px] bg-[var(--color-canvas)] px-1 font-mono text-[11px]">
            {p.slice(1, -1)}
          </code>
        )
      }
      return <Fragment key={key}>{p}</Fragment>
    })
}

const HEADING_CLASS: Record<number, string> = {
  1: 'mt-0 text-[15px] font-bold',
  2: 'mt-3.5 text-[13.5px] font-bold',
  3: 'mt-3 text-[12.5px] font-bold',
  4: 'mt-2.5 text-[12px] font-semibold',
}

export function Markdown({ source }: { source: string }) {
  const blocks = parseBlocks(source)
  return (
    <div className="flex flex-col">
      {blocks.map((b, i) => {
        const key = `b${i}`
        switch (b.kind) {
          case 'heading': {
            const level = Math.min(4, Math.max(1, b.level))
            const Tag = `h${level}` as 'h1' | 'h2' | 'h3' | 'h4'
            return (
              <Tag key={key} className={HEADING_CLASS[level]}>
                {inline(b.text, key)}
              </Tag>
            )
          }
          case 'hr':
            return <hr key={key} className="my-3 border-t border-[var(--color-border)]" />
          case 'p':
            return (
              <p key={key} className="mt-1 text-[12px] leading-relaxed">
                {inline(b.text, key)}
              </p>
            )
          case 'ul':
            return (
              <ul key={key} className="mt-1 list-disc space-y-0.5 pl-[18px] text-[12px] leading-relaxed">
                {b.items.map((it, k) => (
                  <li key={k}>{inline(it, `${key}-${k}`)}</li>
                ))}
              </ul>
            )
          case 'ol':
            return (
              <ol key={key} className="mt-1 list-decimal space-y-0.5 pl-[18px] text-[12px] leading-relaxed">
                {b.items.map((it, k) => (
                  <li key={k}>{inline(it, `${key}-${k}`)}</li>
                ))}
              </ol>
            )
        }
      })}
    </div>
  )
}
