// 把 vite 的产物复制到 Go 的 embed 目录。
//
// 为什么需要这一步：go:embed 在编译期把 internal/webassets/dist 打进 exe，
// 而 vite 默认输出到 web/dist。两者不是同一个目录；不复制的话单 exe 里
// 永远只有占位页——一个能启动、但打开显示「前端资源尚未构建」的 exe。
//
// 保留 .gitkeep：go:embed 要求目录非空，前端未构建时 Go 侧也要能编译。
// 因此这里清空目录内容但不删目录本身，也不碰 .gitkeep。
import { cp, mkdir, readdir, readFile, rm, stat, writeFile } from 'node:fs/promises'
import { existsSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const from = path.resolve(here, '..', 'dist')
const to = path.resolve(here, '..', '..', 'internal', 'webassets', 'dist')
const keepName = '.gitkeep'

if (!existsSync(from)) {
  console.error(`embed: ${from} 不存在，请先运行 vite build`)
  process.exit(1)
}

await mkdir(to, { recursive: true })

// 记住 .gitkeep 的内容（通常为空），清空后原样写回。
let keepContent = ''
if (existsSync(path.join(to, keepName))) {
  keepContent = await readFile(path.join(to, keepName))
}

for (const entry of await readdir(to)) {
  await rm(path.join(to, entry), { recursive: true, force: true })
}
await writeFile(path.join(to, keepName), keepContent)

await cp(from, to, { recursive: true })

const files = await readdir(to)
let bytes = 0
for (const f of files) {
  const s = await stat(path.join(to, f))
  if (s.isFile()) bytes += s.size
}
console.log(`embed: ${files.length} 个条目, ${bytes} 字节 -> ${to}`)
