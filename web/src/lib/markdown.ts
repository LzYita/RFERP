/**
 * 内置帮助文档的最小 Markdown 分块器。
 *
 * 为什么不引第三方库：internal/help/permissions_test.go 已用静态检查把帮助内容
 * 限制在一个很小的子集里——标题、分隔线、有序/无序列表、段落、**粗体**、
 * `ASCII 行内代码`；没有表格、没有引用块、没有代码块。为这个子集引一个完整的
 * markdown 库，等于把「内容受约束」这条前提换成「库能处理任意输入」，
 * 平白多一个解析器与它的一串转义面。
 *
 * 项目符号除 `-`/`*`/`+` 外还认 `·`：更新说明里的二级条目就是这么写的
 * （见 internal/help/changelog.json），不认的话它们会被并成一段跑在一行里。
 *
 * 分块器与渲染器分开：这一层是纯函数（不依赖 React），渲染在
 * components/Markdown.tsx。两者分开也让这个文件可以被直接断言，
 * 而不必挂载组件。
 */
export type Block =
  | { kind: 'heading'; level: number; text: string }
  | { kind: 'hr' }
  | { kind: 'p'; text: string }
  | { kind: 'ul'; items: string[] }
  | { kind: 'ol'; items: string[] }

const UL = /^(?:[-*+]|·)\s+(.*)$/
const OL = /^(\d+)[.)]\s+(.*)$/
const HEADING = /^(#{1,6})\s+(.*)$/

/**
 * 行尾是中文/全角字符时，软换行处不插空格。
 *
 * 帮助文档是中文写的，段落里的换行只是排版需要。用空格连接会在句号后留出
 * 一个可见的缝（"…的角色。 新建账号…"）；而英文段落挤在一起又会把
 * "the batch" 变成 "thebatch"。按行尾字符决定，两种都不出错。
 */
const CJK_TAIL = /[\u3000-\u303f\u4e00-\u9fff\uff00-\uffef]/

function joinSoft(lines: string[]): string {
  return lines.reduce((acc, line) => {
    if (acc === '') return line
    return acc + (CJK_TAIL.test(acc) ? '' : ' ') + line
  }, '')
}

/** 把 Markdown 原文切成分块。 */
export function parseBlocks(source: string): Block[] {
  const lines = source.replace(/\r\n/g, '\n').split('\n')
  const blocks: Block[] = []
  let para: string[] = []

  const flushPara = () => {
    if (para.length > 0) {
      blocks.push({ kind: 'p', text: joinSoft(para) })
      para = []
    }
  }

  for (let i = 0; i < lines.length; i++) {
    const t = lines[i].trim()

    if (t === '') {
      flushPara()
      continue
    }
    if (/^-{3,}$/.test(t)) {
      flushPara()
      blocks.push({ kind: 'hr' })
      continue
    }
    const h = HEADING.exec(t)
    if (h) {
      flushPara()
      blocks.push({ kind: 'heading', level: h[1].length, text: h[2].trim() })
      continue
    }
    const ul = UL.exec(t)
    if (ul) {
      flushPara()
      const items = [ul[1]]
      while (i + 1 < lines.length) {
        const next = UL.exec(lines[i + 1].trim())
        if (!next) break
        items.push(next[1])
        i++
      }
      blocks.push({ kind: 'ul', items })
      continue
    }
    const ol = OL.exec(t)
    if (ol) {
      flushPara()
      const items = [ol[2]]
      while (i + 1 < lines.length) {
        const next = OL.exec(lines[i + 1].trim())
        if (!next) break
        items.push(next[2])
        i++
      }
      blocks.push({ kind: 'ol', items })
      continue
    }
    // 其余一律当段落文字：包括表格行与引用块。
    para.push(t)
  }
  flushPara()
  return blocks
}
