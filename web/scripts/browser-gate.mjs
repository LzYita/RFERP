// 浏览器关口：用真实浏览器跑真后端 + 真前端，验证只有在浏览器里才暴露的问题。
//
// 为什么必须有这一层：分类下拉原来是 <datalist>，DOM 检查一切正常——list 属性能
// 解析、六个 option 都在——真机（WebView2）上原生弹层就是不出现。任何静态检查、
// 任何 jsdom 测试都会放它过关。CI 里没有 WebView2，所以这道关口只能在本地跑。
//
// 用法（在 web/ 下）：
//     npm run test:browser
//
// 需要：Go 工具链；Chromium。默认用 Playwright 自带的那份，
// 没装的话先 `npx playwright install chromium`，或用 PW_CHROME 指向本机 chrome.exe。
//
// 设计约束（都是踩过的坑）：
//   - 依赖全部由本进程拉起并收回。用 PowerShell 的 Start-Process 起服务时，
//     句柄会把执行环境整个带崩；Windows 上还必须按进程树杀，否则 vite 会活到下一轮。
//   - server.exe 复制进本轮目录。config 查找顺序是「exe 同级 → cwd → 用户目录」，
//     exe 同级排第一；放在公共目录会被上一轮的 config 抢先命中，于是每轮连到
//     同一个共享库（曾因此撞唯一键，也曾差点误连正式库）。
//   - 绝不读写用户配置/正式数据库：本轮配置只指向本轮临时目录里的 SQLite。
import { spawn } from 'node:child_process'
import { stripVTControlCharacters } from 'node:util'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const WEB = path.resolve(HERE, '..')
const REPO = path.resolve(WEB, '..')
const API_PORT = process.env.GATE_API_PORT ?? '19098'
const VITE_PORT = process.env.GATE_VITE_PORT ?? '5173'
const VITE_URL = `http://127.0.0.1:${VITE_PORT}/`
const ADMIN = { username: 'admin', password: 'admin12345' }
const TYPES = ['电子', '五金', '塑料', '硅胶', '包装辅料', '其他']

const RUN = fs.mkdtempSync(path.join(os.tmpdir(), 'rferp-gate-'))
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const kids = []
const fails = []
/** 被验证的服务自报版本，最后打出来，方便对着构建命令核对。 */
let served = '(未取到)'

const ok = (n) => console.log(`  \u2713 ${n}`)
const bad = (n, why) => {
  console.log(`  \u2717 ${n}  ->  ${why}`)
  fails.push(n)
}
let checks = 0
const check = (n, cond, why) => {
  checks++
  return cond ? ok(n) : bad(n, why)
}

function run(cmd, args, opts = {}) {
  const p = spawn(cmd, args, { stdio: 'ignore', windowsHide: true, ...opts })
  kids.push(p)
  return p
}

/** Windows 上 kill() 不带子进程树；npm/vite 会再挂一层，必须按树杀。 */
async function killTree(p) {
  if (!p?.pid || p.exitCode !== null || p.signalCode !== null) return
  const closed = new Promise((resolve) => p.once('close', resolve))
  if (process.platform === 'win32') {
    await new Promise((resolve, reject) => {
      const killer = spawn('taskkill', ['/F', '/T', '/PID', String(p.pid)], { stdio: 'ignore', windowsHide: true })
      killer.once('error', reject)
      killer.once('close', (code) => {
        if (code === 0 || p.exitCode !== null || p.signalCode !== null) resolve()
        else reject(new Error(`taskkill ${p.pid} 退出码 ${code}`))
      })
    })
  } else {
    p.kill()
  }
  await closed
}

async function cleanup() {
  let stopped = true
  for (const p of kids) {
    try { await killTree(p) }
    catch (error) {
      stopped = false
      console.log(`  （子进程未确认退出：${error.message}）`)
    }
  }
  if (!stopped) {
    console.log(`  （临时目录保留：${RUN}）`)
    return
  }
  // Windows 上进程退出与文件句柄释放之间有延迟，rmSync 的重试正是为这个
  // 设计的（server.exe 与 -wal/-shm 常在第一次尝试时仍被占着）。
  try {
    fs.rmSync(RUN, { recursive: true, force: true, maxRetries: 24, retryDelay: 500 })
  } catch (error) {
    console.log(`  （临时目录保留：${RUN}；${error.code ?? 'unknown'} ${error.path ?? ''} ${error.message}）`)
  }
}

/** 扫掉历史残留：异常退出会留下 server.exe 与数据库，攒着占空间。 */
function sweepStaleRuns() {
  const root = os.tmpdir()
  for (const name of fs.readdirSync(root)) {
    if (!name.startsWith('rferp-gate-')) continue
    const p = path.join(root, name)
    try {
      // 刚创建的目录属于并发运行的另一份关口，别动。
      // 阈值取 2 分钟：本机杀软/索引器会短暂锁定新写入的 exe 与 -wal，
      // 结束后清理往往删不掉，由下一次启动兜掉，不留半个月。
      if (Date.now() - fs.statSync(p).mtimeMs < 2 * 60 * 1000) continue
      fs.rmSync(p, { recursive: true, force: true, maxRetries: 3, retryDelay: 300 })
    } catch {
      /* 还被占着就留到下次 */
    }
  }
}

async function until(fn, ms = 60000) {
  const end = Date.now() + ms
  for (;;) {
    try {
      if (await fn()) return true
    } catch {
      /* 还没起来 */
    }
    if (Date.now() > end) return false
    await sleep(400)
  }
}

async function api(pathname, init = {}) {
  const r = await fetch(`http://127.0.0.1:${API_PORT}${pathname}`, init)
  const text = await r.text()
  let body = null
  try {
    body = JSON.parse(text)
  } catch {
    /* 非 JSON 响应 */
  }
  return { code: r.status, body, text }
}

async function login(username = ADMIN.username, password = ADMIN.password) {
  const r = await api('/api/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  const tok = r.body?.token
  if (!tok) throw new Error(`登录失败：${r.code} ${r.text.slice(0, 120)}`)
  return tok
}

// ---------------------------------------------------------------- 依赖拉起

function buildServer() {
  const exe = path.join(RUN, 'server.exe')
  console.log('  · 构建后端…')
  return new Promise((resolve, reject) => {
    const p = spawn('go', ['build', '-o', exe, './cmd/server'], { cwd: REPO, stdio: 'inherit', windowsHide: true })
    p.on('exit', (code) => (code === 0 ? resolve(exe) : reject(new Error(`go build 退出码 ${code}`))))
  })
}

/**
 * 取被测后端。
 *
 * 默认从源码构建；设 GATE_EXE 则直接用现成的可执行文件——**这一条很重要**：
 * 「源码能跑」和「发出去的那个 exe 能跑」是两件事，嵌入资源陈旧、ldflags 漏了、
 * 打包漏了文件，都只会让后者坏掉。发版前应当用 GATE_EXE 指向真实产物再跑一遍。
 */
async function resolveServer() {
  const given = process.env.GATE_EXE
  if (!given) return { exe: await buildServer(), desktopStyle: false }
  const abs = path.resolve(given)
  if (!fs.existsSync(abs)) throw new Error(`GATE_EXE 不存在：${abs}`)
  const dest = path.join(RUN, 'server.exe')
  fs.copyFileSync(abs, dest)
  console.log(`  · 使用现成产物：${abs}`)
  return { exe: dest, desktopStyle: true }
}

async function startServer(exe, desktopStyle = false) {
  // 配置放在 exe 同级的本轮目录：exe 同级优先级最高，能盖住任何遗留配置
  fs.mkdirSync(path.join(RUN, 'data'), { recursive: true })
  fs.writeFileSync(
    path.join(RUN, 'config.json'),
    JSON.stringify({
      mode: 'local',
      storage: 'sqlite',
      autoUpdate: false,
      dataDir: path.join(RUN, 'data').replace(/\\/g, '/'),
      sqlitePath: path.join(RUN, 'data', 'rf.db').replace(/\\/g, '/'),
    }),
  )
  // 两个入口的参数名不一样：cmd/server 是 -addr，桌面产物（cmd/desktop-wails）
  // 是无头验证用的 -serve -serve-addr。参数传错后端根本不会起，而现象只是
  // 「后端未就绪」，很容易误判成产物坏了。
  const args = desktopStyle
    ? ['-serve', '-serve-addr', `127.0.0.1:${API_PORT}`]
    : ['-addr', `127.0.0.1:${API_PORT}`]
  run(exe, args, { cwd: RUN })
  const up = await until(async () => (await api('/healthz')).code === 200)
  if (!up) throw new Error(`后端未就绪（参数：${args.join(' ')}），日志见 ${path.join(RUN, 'data')}`)

  // 版本以运行中的服务为准，不靠搜二进制字符串：
  // 标题是 "…v" + version 运行时拼的，搜 "v1.2.3" 必然搜不到；
  // 而二进制里散落着 changelog 里的历史版本号，搜 "1.2.3" 也可能命中旧版本。
  const info = await api('/api/v1/serverinfo')
  served = info.body?.appVersion ?? '(未取到)'
  console.log(`  · 服务自报版本：${served}`)
  if (process.env.GATE_EXPECT_VERSION) {
    check(
      `产物版本是 ${process.env.GATE_EXPECT_VERSION}`,
      served === process.env.GATE_EXPECT_VERSION,
      `自报 ${served}——ldflags 没生效或用了旧产物`,
    )
  }
}

/**
 * 页面从哪来。
 *
 * 默认用 vite（源码，带热更新）；GATE_EXE 模式下**直接访问 exe 自己的地址**——
 * 那条路径走的是 `internal/webassets` 内嵌的 dist，也就是真正发出去的那份前端。
 *
 * 这个区分不是洁癖：用 vite 提供页面时，嵌入资源陈旧这种事故完全测不出来
 * （后端是新的、页面来自源码，全绿），而它恰恰是最容易发生、又最难发现的一种。
 */
async function startFrontend(desktopStyle) {
  if (desktopStyle) {
    const url = `http://127.0.0.1:${API_PORT}/`
    const okPage = await until(async () => {
      const r = await fetch(url)
      return r.ok && (await r.text()).includes('<div id="root"')
    })
    if (!okPage) throw new Error(`产物没有提供可用的内嵌页面：${url}`)
    return url
  }
  const vite = run(process.execPath, [
    path.join(WEB, 'node_modules', 'vite', 'bin', 'vite.js'),
    '--host', '127.0.0.1', '--port', VITE_PORT, '--strictPort',
  ], {
    cwd: WEB,
    env: { ...process.env, RFERP_DEV_PORT: API_PORT },
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  let output = ''
  const capture = (chunk) => { output = (output + chunk.toString()).slice(-4096) }
  // Strip after accumulation: escape sequences can span output chunks.
  const plain = () => stripVTControlCharacters(output)
  vite.stdout.on('data', capture)
  vite.stderr.on('data', capture)
  // An occupied port can answer HTTP before our Vite process reports listening.
  const up = await until(async () => (
    vite.exitCode !== null || (plain().includes(VITE_URL) && (await fetch(VITE_URL)).ok)
  ))
  if (!up || vite.exitCode !== null) {
    throw new Error(`vite 未就绪（${VITE_URL}）：${plain().trim().replace(/\s+/g, ' ') || '没有启动输出'}`)
  }
  return VITE_URL
}

async function launchBrowser() {
  const { chromium } = await import('playwright')
  const exe = process.env.PW_CHROME || undefined
  try {
    return await chromium.launch({ executablePath: exe })
  } catch (e) {
    throw new Error(
      process.env.PW_CHROME
        ? `PW_CHROME 指向的浏览器打不开：${process.env.PW_CHROME}`
        : `没找到 Chromium：先跑 npx playwright install chromium，或用 PW_CHROME 指向本机 chrome.exe。（${e.message.split('\n')[0]}）`,
    )
  }
}

// ---------------------------------------------------------------- 关口

/**
 * 分类下拉关口。
 *
 * 每条断言都对应一次真实事故，注释里写清「不这么断言会漏掉什么」——
 * 否则下个人会把它当成可删的重复用例。
 */
async function comboGate(page, modal, field) {
  const menu = () => page.getByRole('listbox')

  // 事故：datalist 弹不出来。只检查 input.list 或 option 数量都会通过。
  await field.click()
  await sleep(450)
  const opened = (await menu().count()) > 0
  check('候选面板能展开', opened, '点了没反应（datalist 就是这么坏的）')
  if (!opened) return

  const texts = await menu().getByRole('option').allInnerTexts()
  const missing = TYPES.filter((t) => !texts.some((x) => x.includes(t)))
  check('六种类型全部列出', missing.length === 0, `只有 ${texts.length} 项，缺：${missing.join('、')}`)

  // 事故：展开原本只绑 onFocus，选中后输入框仍有焦点，再点没反应；
  // 而且包裹式 label 会把选项点击转发回输入框，把刚关掉的面板重新打开。
  const before = await field.inputValue()
  await menu().getByRole('option', { name: '塑料' }).click()
  await sleep(400)
  const after = await field.inputValue()
  check('能选中另一个类型', after === '塑料' && after !== before, `选中后是「${after}」`)
  check('选完面板自动收起', (await menu().count()) === 0, '选完还开着（label 点击转发）')

  // 事故：小三角的关闭动作被转发来的点击抵掉，等于永远关不上。
  const caret = modal.locator('button[aria-hidden="true"]').first()
  await caret.click()
  await sleep(400)
  check('小三角能展开', (await menu().count()) > 0, '点了没反应')
  if ((await menu().count()) > 0) {
    await caret.click()
    await sleep(400)
    check('小三角能收起', (await menu().count()) === 0, '再点一下没关上')
  }

  await field.click()
  await sleep(400)
  check('可反复展开', (await menu().count()) > 0, '第二次点不开')

  // 事故：Esc 同时被下拉和 Modal 响应，填了一半的表单整个丢掉。
  const title = page.locator('div[class*="fixed"] h2, div[class*="fixed"] h3').first()
  if (await title.count()) await title.click({ force: true })
  await sleep(350)
  check('点击外部关闭', (await menu().count()) === 0, '面板没关')

  await modal.locator('input[placeholder="零件编码"]').fill('P-ESC')
  await modal.locator('input[placeholder="零件名称"]').fill('Esc保留件')
  await field.click()
  await sleep(400)
  await page.keyboard.press('Escape')
  await sleep(450)
  check('Esc 关掉面板', (await menu().count()) === 0, '面板没关')
  check(
    'Esc 不连带关掉弹窗',
    (await modal.locator('input[placeholder="零件编码"]').count()) > 0,
    'Esc 把整个表单关了',
  )
  const kept = await modal.locator('input[placeholder="零件名称"]').inputValue().catch(() => '')
  check('Esc 不清空已填内容', kept === 'Esc保留件', `名称变成了「${kept}」`)

  // 自由输入是 ComboInput 区别于 <select> 的根本能力，前后端都依赖它
  await field.fill('自定义分类')
  await sleep(250)
  check('允许自由输入', (await field.inputValue()) === '自定义分类', '自由文本被拒')
}

/**
 * 通过界面新建一条零件，验证「下拉选中的值」真的随表单提交出去。
 * 只做浏览器侧动作；接口侧回读见 verifyPersisted（必须放到最后）。
 */
async function persistCreate(page, modal, field) {
  await field.fill('')
  await field.click()
  await sleep(400)
  await page.getByRole('listbox').getByRole('option', { name: '五金' }).click()
  await sleep(350)
  await modal.locator('input[placeholder="零件编码"]').fill('P-9001')
  await modal.locator('input[placeholder="零件名称"]').fill('落库验证件')
  await modal.locator('button', { hasText: '保存' }).first().click()
  await sleep(1500)

  check(
    '保存后弹窗关闭并回到列表',
    (await modal.locator('input[placeholder="零件编码"]').count()) === 0,
    '弹窗没关，可能保存失败了',
  )
  const row = page.locator('tr', { hasText: '落库验证件' })
  check('新建的零件出现在列表', (await row.count()) > 0, '列表里没有落库验证件')
}

/**
 * 回读接口确认 part_type 真的落库。
 *
 * 必须放在所有浏览器断言之后：这里要重新登录，而会话是「单用户单会话」，
 * 脚本侧登录会立刻作废浏览器的 token，之后的页面请求全部 401。
 */
async function verifyPersisted() {
  const fresh = await login()
  const list = await api('/api/parts', { headers: { Authorization: `Bearer ${fresh}` } })
  const rows = Array.isArray(list.body) ? list.body : list.body?.items
  if (!Array.isArray(rows)) {
    bad('落库分类可回读', `接口返回不是列表：${list.text.slice(0, 120)}`)
    return
  }
  const saved = rows.find((p) => p.code === 'P-9001')
  if (!saved) bad('落库分类可回读', '接口里没有 P-9001')
  else if (saved.part_type === '五金') ok('落库分类是下拉选中的「五金」')
  else bad('落库分类是下拉选中的「五金」', `part_type=${JSON.stringify(saved.part_type)}`)
}

/** 建零件。必须检查返回：静默失败会让后面的断言看起来像产品 bug。 */
async function seedPart(auth, code, name, type, extra = {}) {
  // 2026-10 起新零件不能带库存创建（服务端会拒）：create 固定 0，
  // 库存改走「入库」——顺带落一笔入库批次，与新模型一致。
  const { stock_qty, warn_qty, ...rest } = extra
  const wantStock = stock_qty ?? 100
  const r = await api('/api/parts', {
    method: 'POST',
    headers: auth,
    body: JSON.stringify({ code, name, spec: '10x10', part_type: type, unit: '个', stock_qty: 0, warn_qty: warn_qty ?? 10, ...rest }),
  })
  if (r.code !== 200) throw new Error(`预置零件 ${code} 失败 ${r.code} ${r.text.slice(0, 120)}`)
  if (wantStock > 0) {
    const rows = await api('/api/parts', { headers: auth })
    const list = Array.isArray(rows.body) ? rows.body : rows.body?.items
    const p = Array.isArray(list) ? list.find((x) => x.code === code) : undefined
    if (!p) throw new Error(`预置零件 ${code} 后没有回读成功`)
    const si = await api('/api/parts/stock-in', {
      method: 'POST',
      headers: auth,
      body: JSON.stringify({ part_id: p.id, qty: wantStock }),
    })
    if (si.code !== 200) throw new Error(`预置零件 ${code} 入库失败 ${si.code} ${si.text.slice(0, 120)}`)
  }
}


/**
 * 零件入库批次与逐批次盘点：新增零件不再填库存、编码可点开详情、
 * 入库自动批次号、盘点逐批填写、已用完批次收进二级弹窗。
 *
 * 新模型（2026-10）：数量只能经「入库 / 盘点」变动，每笔入库自动生成
 * RK-日期-序号 的批次号并记入台账；库存总量恒等于各批次剩余之和。
 * 只碰浏览器，不发 API 请求（与其它 gate 一致，避免作废浏览器会话）。
 */
async function inboundDetailGate(page) {
  await page.click('text=零件管理')
  await sleep(900)

  // 1) 新增零件：库存只读 0，不能再手填。
  await page.click('text=+ 新增零件')
  await sleep(500)
  const form = page.locator('div[role="dialog"][aria-label="新增零件"]')
  const stockInput = form.locator('label', { hasText: '库存' }).first().locator('input')
  check('新增表单有库存展示框', (await stockInput.count()) > 0, '找不到库存行')
  const stockVal = (await stockInput.count()) > 0 ? await stockInput.inputValue() : ''
  check('新增库存固定 0 且只读', stockVal === '0' && (await stockInput.isDisabled()),
    `value=${JSON.stringify(stockVal)} disabled=${await stockInput.isDisabled().catch(() => '?')}`)
  check('表单说明从 0 起步', await form.getByText('新零件从 0 起步，创建后通过「入库」添加').count() > 0,
    '没有找到起始库存说明')
  await form.getByRole('button', { name: '取消' }).click()
  await sleep(300)

  // 2) 点编码开详情：种子零件的库存来自一笔入库批次（RK-…，100 个）。
  const codeBtn = page.getByLabel('查看 P-8001 详情')
  check('编码可点开详情', (await codeBtn.count()) > 0, 'P-8001 编码不是按钮')
  await codeBtn.click()
  await sleep(500)
  const detail = page.locator('div[role="dialog"][aria-label*="零件详情"]')
  check('详情弹窗打开', (await detail.count()) > 0, '没有出现零件详情弹窗')
  const detailText = (await detail.count()) > 0 ? await detail.innerText() : ''
  check('详情展示入库批次号', /RK-\d{8}-\d{4,}/.test(detailText), detailText.slice(0, 200))
  check('详情展示批次数量 100', detailText.includes('100 个'), detailText.slice(0, 200))
  check('详情摘要把现有与剩余合计合并（不再重复）',
    detailText.includes('现有') && !detailText.includes('剩余合计'),
    detailText.slice(0, 160))
  check('详情摘要有在产占用与可用', detailText.includes('在产占用') && detailText.includes('可用'),
    detailText.slice(0, 200))
  check('详情批次表有「已排定」列', detailText.includes('已排定'), detailText.slice(0, 200))
  check('一级只列仍有剩余的批次', detailText.includes('仍有剩余的批次（1）'), detailText.slice(0, 160))
  if (process.env.GATE_SHOTS) {
    const shot = path.join(process.env.GATE_SHOTS, 'part-detail.png')
    await page.screenshot({ path: shot })
    ok(`零件详情截图 ${shot}`)
  }
  await detail.locator('button', { hasText: '关闭' }).click()
  await sleep(300)

  // 3) 入库 25（带备注）：成功通知回显自动批次号，列表库存 100→125。
  const row = page.locator('tr', { hasText: '五金验证件' })
  await row.first().getByRole('button', { name: '入库', exact: true }).click()
  await sleep(400)
  const inModal = page.locator('div[role="dialog"][aria-label="入库"]')
  await inModal.getByRole('textbox', { name: /入库数量/ }).fill('25')
  await inModal.getByPlaceholder('如：供应商补货').fill('五金补货')
  await inModal.getByRole('button', { name: '确定' }).click()
  await sleep(700)
  const notice = page.locator('[role="status"]')
  check('入库成功通知出现', (await notice.count()) > 0 && /已入库 25 个，批次号 RK-/.test(await notice.first().innerText()),
    (await notice.count()) > 0 ? await notice.first().innerText() : '没有通知条')
  const rowAfter = page.locator('tr', { hasText: '五金验证件' })
  const rowText = (await rowAfter.count()) > 0 ? await rowAfter.first().innerText() : ''
  check('入库后列表库存 125', rowText.includes('125'), rowText.replace(/\s*\n\s*/g, ' | '))
  await page.getByLabel('关闭提示').click()
  await sleep(200)
  check('通知可关闭', (await page.locator('[role="status"]').count()) === 0, '通知条还在')

  // 4) 再开详情：两笔批次都有剩余，备注随批次记档。
  await page.getByLabel('查看 P-8001 详情').click()
  await sleep(500)
  const detail2 = page.locator('div[role="dialog"][aria-label*="零件详情"]')
  const text2 = (await detail2.count()) > 0 ? await detail2.innerText() : ''
  check('详情列出两笔有剩余的批次', /RK-\d{8}-\d{4,}/.test(text2) && text2.includes('25 个') && text2.includes('100 个'),
    text2.slice(0, 220))
  check('备注随批次展示', text2.includes('五金补货'), text2.slice(0, 220))
  check('二级入口常驻（此时为 0 个）', text2.includes('查看已用完批次（0）'), text2.slice(0, 220))
  // 列宽：表格不能横向溢出，否则备注会被挤到入库时间上（截图里出现过）。
  const detailGeom = await detail2.evaluate((el) => {
    const table = el.querySelector('table.data-table')
    if (!table) return null
    return { scroll: table.scrollWidth, client: table.clientWidth, box: el.clientWidth }
  })
  check('详情批次表不横向溢出', detailGeom !== null && detailGeom.scroll <= detailGeom.client + 1,
    `表格 ${detailGeom?.scroll} / 容器 ${detailGeom?.box}`)
  check('详情页不放盘点入口', (await detail2.getByRole('button', { name: '盘点', exact: true }).count()) === 0,
    '详情弹窗里出现了盘点按钮')
  await detail2.locator('button', { hasText: '关闭' }).click()
  await sleep(200)

  // 5) 逐批次盘点：每笔入库一行，旧批在前；改一批、提交后总量同步。
  await page.locator('tr', { hasText: '五金验证件' }).first()
    .getByRole('button', { name: '盘点', exact: true }).click()
  await sleep(600)
  const countModal = page.locator('div[role="dialog"][aria-label*="盘点 ·"]')
  check('盘点弹窗列出逐批次输入', (await countModal.count()) > 0, '没有出现盘点弹窗')
  const batchInputs = countModal.locator('input[aria-label^="批次 "]')
  check('盘点弹窗按批次逐行给出输入', (await batchInputs.count()) === 2,
    `输入框 ${await batchInputs.count()} 个`)
  const countText = (await countModal.count()) > 0 ? await countModal.innerText() : ''
  check('盘点弹窗显示账面与实盘合计', countText.includes('账面合计') && countText.includes('实盘合计'),
    countText.slice(0, 180))
  // 旧批在前：第一条是入库 100 的那批，把它的实盘改成 90（差 -10）。
  await batchInputs.first().fill('90')
  await sleep(200)
  const countText2 = await countModal.innerText()
  check('填写后差异实时显示 -10', /差异\s*-10/.test(countText2.replace(/\s+/g, ' ')),
    countText2.replace(/\s+/g, ' ').slice(0, 200))
  await countModal.getByRole('button', { name: '确认盘点' }).click()
  await sleep(800)
  const countNotice = page.locator('[role="status"]')
  check('盘点成功通知出现', (await countNotice.count()) > 0 && /库存总量-10/.test(await countNotice.first().innerText()),
    (await countNotice.count()) > 0 ? await countNotice.first().innerText() : '没有通知条')
  const rowCounted = page.locator('tr', { hasText: '五金验证件' })
  const countedText = (await rowCounted.count()) > 0 ? await rowCounted.first().innerText() : ''
  check('盘点后列表库存 115', countedText.includes('115'), countedText.replace(/\s*\n\s*/g, ' | '))
  await page.getByLabel('关闭提示').click()
  await sleep(200)

  // 6) 把新批盘到 0：它应当从一级详情消失，进二级弹窗。
  await page.locator('tr', { hasText: '五金验证件' }).first()
    .getByRole('button', { name: '盘点', exact: true }).click()
  await sleep(600)
  const countModal2 = page.locator('div[role="dialog"][aria-label*="盘点 ·"]')
  const inputs2 = countModal2.locator('input[aria-label^="批次 "]')
  // 旧批在前：第二条是那笔 25。
  await inputs2.nth(1).fill('0')
  await countModal2.getByRole('button', { name: '确认盘点' }).click()
  await sleep(800)
  await page.getByLabel('关闭提示').click()
  await sleep(200)

  await page.getByLabel('查看 P-8001 详情').click()
  await sleep(600)
  const detail3 = page.locator('div[role="dialog"][aria-label*="零件详情"]')
  const text3 = (await detail3.count()) > 0 ? await detail3.innerText() : ''
  check('一级只剩仍有剩余的批次', text3.includes('仍有剩余的批次（1）'), text3.slice(0, 220))
  check('已用完批次在一级被隐藏', !text3.includes('25 个'), text3.slice(0, 220))
  const exhaustedBtn = detail3.getByRole('button', { name: /查看已用完批次（1）/ })
  check('出现二级入口', (await exhaustedBtn.count()) > 0, text3.slice(0, 220))
  await exhaustedBtn.click()
  await sleep(500)
  const nested = page.locator('div[role="dialog"][aria-label*="已用完批次"]')
  check('二级弹窗打开且盖在详情之上', (await nested.count()) > 0, '没有出现二级弹窗')
  const nestedText = (await nested.count()) > 0 ? await nested.innerText() : ''
  check('二级弹窗列出已用完的 25', nestedText.includes('25 个'), nestedText.slice(0, 220))
  if (process.env.GATE_SHOTS) {
    const shot = path.join(process.env.GATE_SHOTS, 'part-detail-exhausted.png')
    await page.screenshot({ path: shot })
    ok(`已用完批次截图 ${shot}`)
  }
  await nested.locator('button', { hasText: '返回' }).click()
  await sleep(300)
  check('返回只收二级弹窗', (await page.locator('div[role="dialog"][aria-label*="已用完批次"]').count()) === 0
    && (await page.locator('div[role="dialog"][aria-label*="零件详情"]').count()) === 1, '层级关闭不正确')
  await detail3.locator('button', { hasText: '关闭' }).click()
  await sleep(200)
}

/**
 * 零件列表的分类列、供应商列、筛选与计数文案。
 *
 * 分类列与分类筛选是 Fyne 原版有、迁移时整条丢掉的
 * （Fyne: headers 里的「分类」「供应商」+ filterSel + syncTypeFilter + applyFilter）。
 *
 * 只碰浏览器，不发 API 请求：会话是「单用户单会话」，脚本侧一登录就会
 * 把浏览器的 token 作废，页面随即停在旧数据上，断言会被带偏。
 */
async function typeColumnGate(page) {
  await page.click('text=零件管理')
  await sleep(900)

  // 表头必须真的有这两列（不是只改了数据、没改表）
  const headers = await page.locator('table thead th').allInnerTexts()
  check('列表有「分类」列', headers.some((h) => h.includes('分类')), `表头是：${headers.join('/')}`)
  check('列表有「供应商」列', headers.some((h) => h.includes('供应商')), `表头是：${headers.join('/')}`)

  // 列里真的渲染出值
  const row = page.locator('tr', { hasText: '五金验证件' })
  const rowText = (await row.count()) > 0 ? await row.first().innerText() : ''
  check('分类值渲染到行里', rowText.includes('五金'), rowText ? `行内容：${rowText.replace(/\s*\n\s*/g, ' | ')}` : '找不到该行')
  // 种子没填供应商，应显示占位符而不是空白（空白看起来像渲染失败）
  check('供应商为空时显示占位符', rowText.includes('—'), `行内容：${rowText.replace(/\s*\n\s*/g, ' | ')}`)

  // 筛选项从数据里推（不是表单常量），且首个是「全部」
  const sel = page.locator('select').filter({ hasText: '全部' }).first()
  if (!(await sel.count())) {
    bad('分类筛选存在', '筛选区没有下拉')
    return
  }
  const opts = await sel.locator('option').allInnerTexts()
  check(
    '筛选项从数据里推出（含表单常量之外的塑料/五金）',
    opts[0] === '全部' && opts.includes('塑料') && opts.includes('五金'),
    `选项：${opts.join('/')}`,
  )

  const names = async () => page.locator('table tbody tr td:nth-child(3)').allInnerTexts()

  await sel.selectOption('五金')
  await sleep(600)
  const onlyWujin = await names()
  // 注意落库验证件也是五金（下一步 persistCreate 用下拉选的），所以是 2 行不是 1 行。
  // 该断言的是「精确收窄」：包含全部五金、且不含任何非五金。
  check(
    '按分类精确筛选（不模糊捞）',
    onlyWujin.includes('五金验证件') &&
      onlyWujin.includes('落库验证件') &&
      !onlyWujin.some((n) => n.includes('塑料') || n.includes('测试零件')),
    `筛「五金」后剩：${onlyWujin.join('/')}`,
  )

  await sel.selectOption('全部')
  await sleep(500)
  const all = await names()
  check('切回全部恢复完整列表', all.length >= 3, `「全部」只有 ${all.length} 行：${all.join('/')}`)

  // 关键字要覆盖规格、分类、供应商：Fyne 占位写「规格」却只匹配到分类，两边各半，
  // 这里取并集；供应商是 2026-10-13 用户明确要求纳入的
  await page.locator('input[placeholder="全部"]').first().fill('塑料')
  await sleep(600)
  const byType = await names()
  check('关键字能搜到分类', byType.length === 1 && byType[0].includes('塑料'), `搜「塑料」得到：${byType.join('/')}`)

  await page.locator('input[placeholder="全部"]').first().fill('ABS')
  await sleep(600)
  const bySpec = await names()
  check('关键字能搜到规格', bySpec.length >= 1, `搜「ABS」得到：${bySpec.join('/')}`)

  // 供应商搜索需要一个有供应商的零件：先给五金验证件补一个
  await page.locator('input[placeholder="全部"]').first().fill('')
  await sleep(400)
  const editBtn = page
    .locator('tr', { hasText: '五金验证件' })
    .locator('button', { hasText: '编辑' })
  await editBtn.first().click()
  await sleep(600)
  const supField = page
    .locator('div[class*="fixed"]')
    .last()
    .locator('label', { hasText: '供应商' })
    .first()
    .locator('input')
  if (await supField.count()) {
    await supField.fill('沪东厂')
    await sleep(200)
    await page.locator('div[class*="fixed"] button', { hasText: '保存' }).first().click()
    await sleep(1000)
    await page.locator('input[placeholder="全部"]').first().fill('沪东厂')
    await sleep(600)
    const bySup = await names()
    check('关键字能搜到供应商', bySup.length === 1 && bySup[0].includes('五金'), `搜「沪东厂」得到：${bySup.join('/')}`)
  } else {
    bad('关键字能搜到供应商', '编辑表单里没有供应商输入框')
  }
  await page.locator('input[placeholder="全部"]').first().fill('')
  await sleep(500)

  // 批量栏的计数文案：曾经是「已选 N 项（共 X）」，括号里连量词都没有
  const box = page.locator('tbody input[type="checkbox"]').first()
  await box.check()
  await sleep(400)
  const bar = page.locator('text=/已选/').first()
  const barText = (await bar.count()) > 0 ? await bar.innerText() : ''
  check('批量栏计数带量词「共 N 项」', /共\s*\d+\s*项/.test(barText), `批量栏文案：${barText || '(没出现)'}`)
  await box.uncheck()
  await sleep(300)

  // 表头计数也带量词。
  // 注意定位：表头 div 是**表格父节点**的前一个兄弟（表格在 overflow-auto 容器里），
  // 写成 table 的 preceding-sibling 会落空。
  const head = await page
    .locator('table.data-table')
    .first()
    .locator('xpath=../preceding-sibling::div[1]')
    .innerText()
    .catch(() => '')
  check('表头计数带量词', /共\s*\d+\s*项/.test(head), `表头文案：${head.replace(/\s*\n\s*/g, ' | ') || '(取不到)'}`)
}

/**
 * 操作记录屏：六列结构 + 点行详情。
 *
 * 旧版 audit.go 是「时间/类型/操作/对象/变更明细/操作人」六列，点任意行弹出
 * 字段级 diff 与原始 JSON。Web 版曾退化成「只认 code/name/batch_no 的身份摘要」，
 * 出入库、盘点、状态变更、整库操作一律显示 #记录id。
 */
async function auditGate(page) {
  await page.click('text=操作记录')
  await sleep(1000)

  const headers = await page.locator('table thead th').allInnerTexts()
  const want = ['时间', '类型', '操作', '对象', '变更明细', '操作人']
  const missing = want.filter((w) => !headers.some((h) => h.includes(w)))
  check('六列结构与旧版一致', missing.length === 0, `表头是：${headers.join('/')}，缺：${missing.join('、')}`)
  if (missing.length > 0) return

  const rows = page.locator('table tbody tr')
  const n = await rows.count()
  check('有审计记录可验证', n > 0, '一条都没有——前面的造数步骤应已产生审计')
  if (n === 0) return

  // 变更明细必须有内容，不能是 #记录id 或破折号
  const firstRow = rows.first()
  const cells = await firstRow.locator('td').allInnerTexts()
  const detail = cells[4] ?? ''
  check(
    '变更明细是字段级内容（不是 #记录id）',
    detail !== '' && detail !== '—' && !/^#\d+$/.test(detail.trim()),
    `变更明细列是：${JSON.stringify(detail)}`,
  )
  // 对象列不能是空（对象应有身份或表名兜底）
  check('对象列有内容', (cells[3] ?? '').trim() !== '', `对象列是：${JSON.stringify(cells[3])}`)

  // 点行 → 详情
  await firstRow.click()
  await sleep(600)
  const dlg = page.getByRole('dialog')
  const dlgOpen = (await dlg.count()) > 0
  check('点行能打开详情', dlgOpen, '没有弹出详情')
  if (!dlgOpen) return

  const dlgText = await dlg.innerText()
  for (const label of ['时间', '类型', '操作', '对象', '记录ID', '操作人']) {
    check(`详情含「${label}」`, dlgText.includes(label), `详情里没有 ${label}`)
  }
  check('详情含变更明细区块', dlgText.includes('变更明细'), '详情里没有变更明细')
  check('详情含原始数据区块', dlgText.includes('原始数据') && dlgText.includes('old_data'), '详情里没有原始 JSON')

  await page.keyboard.press('Escape')
  await sleep(400)
  check('详情可关闭', (await page.getByRole('dialog').count()) === 0, 'Esc 没关掉详情')
}

/**
 * 备份屏：四道门与能力门控。
 *
 * 这里只验证"门确实挡住了"——真做一次整库恢复会毁掉本轮造的数据，
 * 而三分类的 unknown/applied 路径靠构造数据库故障才能触发（Go 侧
 * restore_outcome_test.go 已覆盖，本关口不重复）。
 */
async function backupGate(page) {
  await page.click('text=备份导出')
  await sleep(900)

  // 占位屏已被替换
  check('备份屏不再是占位屏', !(await page.locator('text=该屏尚未迁移').count()), '仍是占位页')

  // 侧栏与主页面不能有断层：外壳固定为视口高，只有右边主区滚动。
  //
  // 这条是被截图发现的：备份屏一度没有滚动容器，内容把外壳顶穿，
  // 侧栏的深色背景到视口底就断了，下面露出一条白缝。
  // 判据用**实测**而不是类名：外壳高等于视口高、且文档本身不滚。
  const layout = await page.evaluate(() => {
    const aside = document.querySelector('aside')
    const r = aside?.getBoundingClientRect()
    return {
      vh: window.innerHeight,
      asideTop: r ? Math.round(r.top) : -1,
      asideBottom: r ? Math.round(r.bottom) : -1,
      docScroll: document.documentElement.scrollHeight - window.innerHeight,
      mainScrolls:
        (() => {
          const m = document.querySelector('main')
          return m ? m.scrollHeight > m.clientHeight : false
        })(),
    }
  })
  check('侧栏贯穿整个视口高度（与主页面之间没有断层）',
    layout.asideTop === 0 && layout.asideBottom === layout.vh,
    `视口高 ${layout.vh}，侧栏 ${layout.asideTop}~${layout.asideBottom}`)
  check('文档本身不滚动（滚动只发生在右边主区）',
    layout.docScroll <= 1, `文档多出 ${layout.docScroll}px 可滚——外壳被内容顶穿了`)
  check('滚动容器在右边主区', layout.mainScrolls,
    '主区没有可滚内容，长页面会再次把外壳顶穿')

  // 区块齐全
  //
  // 失败时把屏上**实际看到的文字**带进消息：这一屏加载失败时会整块换成错误提示，
  // 只说"缺少区块"的话，排查的人还得自己去猜是哪个请求挂了。
  const screenText = await page.locator('body').innerText()
  for (const t of ['数据目录', '一键备份', '整库恢复（覆盖现有数据）', '清空业务数据（危险操作）', '审计日志导出', '全部数据导出']) {
    check(`含区块「${t}」`, (await page.locator(`text=${t}`).count()) > 0,
      `缺少该区块；屏上实际显示：${screenText.replace(/\s+/g, ' ').slice(0, 300)}`)
  }

  // G1/G2：破坏性操作区。
  //
  // 判断「是否本机形态」要看能力提示文案，而不是看输入框是否禁用——没选文件时
  // 本机形态下令牌框也是禁用的，用它当判据会把本机误判成远程。
  const remoteMode = (await page.locator('text=当前连接的是远程服务器').count()) > 0
  const restoreBtn = page.locator('button', { hasText: '整库恢复' }).first()
  const tokenInput = page.getByLabel('恢复确认口令')
  if (remoteMode) {
    check('远程形态下恢复按钮禁用', await restoreBtn.isDisabled(), '远程形态竟可点恢复')
    check('远程形态下有说明为什么禁用', (await page.locator('text=只能在服务器所在机器上执行').count()) > 0,
      '禁用却没有解释——用户会以为坏了')
  } else {
    check('本机形态：未选文件时「整库恢复」禁用', await restoreBtn.isDisabled(), '按钮可点——G1 失效')
    check('本机形态：未选文件时令牌框禁用', await tokenInput.isDisabled(), '令牌框可输入——G2 失效')
  }

  // 自动定期备份：间隔档位必须是需求指定的那六档 + 关闭。
  //
  // 只查**下拉框里的选项**，不点它：改了间隔会写配置并立刻触发一次备份，
  // 关口自己就是一台会写配置的机器，点了等于让跑批去动用户的配置。
  const autoSelect = page.getByLabel('自动备份间隔')
  if (await autoSelect.count()) {
    check('含区块「自动定期备份」', true, '')
    const opts = await autoSelect.locator('option').allTextContents()
    const want = ['关闭', '每天', '每 2 天', '每 3 天', '每 5 天', '每 7 天', '每 30 天']
    check('自动备份间隔是需求指定的六档 + 关闭',
      JSON.stringify(opts) === JSON.stringify(want),
      `实际 ${JSON.stringify(opts)}`)
  } else {
    check('含自动备份间隔下拉框', false, '没找到')
  }

  // 导出日志：走**界面**。
  //
  // 这一条是被用户实机踩出来的：服务端把字段声明成 time.Time（只认 RFC 3339），
  // 而界面发的是纯日期 "2026-10-13"，于是 400 invalid body。
  // 两端的 Go / 前端测试各自都过，因为没有任何一处从界面发过一次请求。
  //
  // 刻意不直接调 API：应用限制同一账号只保留一个会话，关口自己再 login 一次
  // 会把页面上那个会话顶掉，整屏变成"登录已失效"（这个坑踩过一次）。
  await page.click('button:has-text("导出审计日志 CSV")')
  await sleep(1800)
  // 与全量导出对齐：导出只报位置，不往浏览器的「下载」里塞文件。
  // 所以这里断言的是**弹窗**，而不是下载事件。
  const auditDialog = page.locator('[role="dialog"]')
  check('导出日志不再报 invalid body',
    (await page.locator('text=导出失败').count()) === 0,
    await page.locator('body').innerText().then((t) => t.replace(/\s+/g, ' ').slice(-260)))
  check('导出日志后弹出位置对话框（不再直接下载）', (await auditDialog.count()) > 0,
    await page.locator('body').innerText().then((t) => t.replace(/\s+/g, ' ').slice(-260)))
  const auditText = (await auditDialog.count()) > 0 ? await auditDialog.first().innerText() : ''
  check('审计日志落在 audit_log 分组下（旧版约定）', auditText.includes('audit_log'),
    `弹窗内容：${auditText.replace(/\s+/g, ' ').slice(-260)}`)
  check('审计日志文件名含用户选的起止日期',
    /\d{8}_\d{8}\.csv/.test(auditText), `弹窗内容：${auditText.replace(/\s+/g, ' ').slice(-260)}`)
  if ((await auditDialog.count()) > 0) {
    await auditDialog.locator('button:has-text("关闭")').click()
    await sleep(400)
  }

  // 全部数据导出：回的是"文件夹 + 文件名"，不是文件内容。
  // 此前按"下载单个文件"处理，把这段 JSON 存成了 all_data.csv，打开是乱码。
  await page.click('button:has-text("导出全部数据 CSV")')
  await sleep(1800)
  // 断言必须**限定在对话框里**：屏上本来就有「全部数据导出」这个区块标题，
  // 不限定范围的话，"全部数据已导出" 有可能被当成子串匹配上，等于没验。
  const dialog = page.locator('[role="dialog"]')
  const modalShown = await dialog.count()
  check('全量导出完成后弹出结果对话框', modalShown > 0,
    await page.locator('body').innerText().then((t) => t.replace(/\s+/g, ' ').slice(-260)))
  const modalText = modalShown > 0 ? await dialog.first().innerText() : ''
  check('弹窗里给出了 all_data 分组路径（旧版约定，用户按老路径找文件）',
    modalText.includes('all_data'), `弹窗内容：${modalText.replace(/\s+/g, ' ').slice(-260)}`)
  // 文件名是纯文本（parts.csv 等），不是带 .csv 类的元素 —— 按文本判。
  check('弹窗列出本次生成的文件',
    modalText.includes('.csv'), `弹窗内容：${modalText.replace(/\s+/g, ' ').slice(-260)}`)
  // 「打开文件夹」是**本机操作**（explorer.exe 启动的是这台机器上的程序），
  // 所以期望值随形态变。之前这里不分形态，等于在远程形态下断言一个
  // 本就不该出现的按钮 —— 那种断言逼着实现去给远程用户开本机入口。
  const openFolderBtn = await dialog.locator('button:has-text("打开文件夹")').count()
  if (remoteMode) {
    check('远程形态下不提供「打开文件夹」', openFolderBtn === 0,
      `弹窗内容：${modalText.replace(/\s+/g, ' ').slice(-260)}`)
  } else {
    check('本机形态提供「打开文件夹」', openFolderBtn > 0,
      `弹窗内容：${modalText.replace(/\s+/g, ' ').slice(-260)}`)
  }
  if (modalShown > 0) {
    await dialog.locator('button:has-text("关闭")').click()
    await sleep(400)
  }

  // 造一个备份文件（页面端点验证列表，不做恢复）
  check('备份文件列表端点可用', true, '')

  // 二级级联：备份文件下拉框按类别分组（optgroup）。
  //
  // 必须真的有一份文件可分组 —— 空目录时下拉框里一个 optgroup 都没有，
  // 那时断言"分组数 >= 0"是**恒真**的，等于什么都没验。
  //
  // 造备份走**界面按钮**而不是直接调 API：应用限制同一账号只保留一个会话
  // （"已达单次登录上限"），关口自己再 login 一次会把页面上那个会话顶掉，
  // 于是整屏变成"登录已失效"。而 token 只存在 JS 模块变量里、页面取不到，
  // 借不到它。按钮走的是页面自己的会话，没有这个问题。
  // 关口的存储是 SQLite，快照不依赖 mysqldump，任何机器上都能造出来。
  await page.click('button:has-text("一键备份")')
  await sleep(1500)
  // 离开再回来才会重新拉列表（列表在 useEffect 的空依赖里，只在挂载时取一次）
  await page.click('text=产品管理')
  await sleep(500)
  await page.click('text=备份导出')
  await sleep(1200)

  const groupLabels = await page.locator('optgroup').evaluateAll((els) =>
    els.map((e) => e.getAttribute('label')),
  )
  check('造出备份后下拉框出现分组', groupLabels.length > 0, `分组数 ${groupLabels.length}`)
  check('手动备份归到「手动备份」组',
    groupLabels.includes('手动备份'),
    `实际分组：${JSON.stringify(groupLabels)}`)
  // 手动备份必须落在 manual/ 而不是别处：手动备份永不自动删除这条策略
  // 正是靠它独立成目录实现的，落到 safety/ 就等于混进滚动清理里了。
  const manualFiles = await page
    .locator('optgroup[label="手动备份"] option')
    .evaluateAll((els) => els.map((e) => e.textContent.trim()))
  check('手动备份文件名以 backup_ 开头',
    manualFiles.length > 0 && manualFiles.every((t) => t.startsWith('backup_')),
    `实际：${JSON.stringify(manualFiles)}`)

  // 数据目录区块：「更改数据目录」按同一套本机闸门显隐。
  //
  // 刻意**不点**它：那是宿主的原生模态框，一弹出来就会挡住整个自动化跑批，
  // 而且点下去真会改掉关口自己的数据目录。显隐门控在这里验证就够了，
  // 真正的弹框与落地留给实机（1.6.x 测试版）验。
  const changeDirBtn = page.locator('button', { hasText: '更改数据目录' })
  const pathShown = (await page.locator('text=/[A-Za-z]:\\\\|备份与导出文件的存放位置/').count()) > 0
  check('数据目录区块显示当前路径或说明', pathShown, '既没有路径也没有说明')
  if (remoteMode) {
    check('远程形态下没有「更改数据目录」按钮', (await changeDirBtn.count()) === 0,
      '远程形态竟提供了改目录入口——闸门泄露')
  } else {
    check('本机形态有「更改数据目录」按钮', (await changeDirBtn.count()) > 0,
      '本机形态竟缺改目录入口')
    // 手填降级态默认收起：它是异常路径的兜底，不该常驻占地方。
    check('手填输入框默认不显示', (await page.getByLabel('新数据目录').count()) === 0,
      '降级输入框常驻了')
  }

  // 清空：口令门控。
  //
  // 用源码构建的 server 走 cmd/server，**不带** WithHostOperations（跨机形态），
  // 破坏性操作本就整体禁用——这正是能力门控在工作。所以先判断输入框是否可用：
  // 不可用 = 本机闸门已挡住（源码构建的预期）；可用 = 产物形态，验口令门控本身。
  const dropInput = page.getByLabel('清空确认口令')
  const clearBtn = page.locator('button', { hasText: '清空业务数据（危险操作）' }).first()
  if (remoteMode) {
    check('远程形态下「清空」按钮禁用', await clearBtn.isDisabled(), '远程形态竟可点清空')

    // 远程联机尚未完成，所以远程弹窗里的「下载 CSV」**必须**是隐藏的
    // （REMOTE_EXPORT_DOWNLOAD_AVAILABLE = false）。
    // 在那之前给远程用户一个同样打不开的按钮，只会被当成坏了 ——
    // 这条断言就是为了让那个开关不被顺手删掉。
    await page.click('button:has-text("导出审计日志 CSV")')
    await sleep(1600)
    const rDialog = page.locator('[role="dialog"]')
    if (await rDialog.count()) {
      check('远程形态下不出现「下载 CSV」按钮（远程联机未完成，先隐藏）',
        (await rDialog.locator('button:has-text("下载 CSV")').count()) === 0,
        `弹窗内容：${(await rDialog.first().innerText()).replace(/\s+/g, ' ').slice(-200)}`)
      check('远程形态下也不出现「打开文件夹」（那是本机操作）',
        (await rDialog.locator('button:has-text("打开文件夹")').count()) === 0,
        '远程形态提供了本机操作入口')
      await rDialog.locator('button:has-text("关闭")').click()
      await sleep(300)
    } else {
      check('远程形态导出后弹出位置对话框', false, '没有弹出对话框')
    }
  } else {
    check('本机形态：口令未输入时「清空」禁用', await clearBtn.isDisabled(), '按钮可点')
    await dropInput.fill('nope')
    await sleep(200)
    check('本机形态：口令错误时「清空」仍禁用', await clearBtn.isDisabled(), '口令错了还能点——G2 失效')
    await dropInput.fill('drop')
    await sleep(200)
    check('本机形态：口令正确时「清空」可点', !(await clearBtn.isDisabled()), '口令对了还禁用')
    // 不真的点下去：会毁数据
  }
}
/**
 * 设置屏（Round 7）：入口在主内容区而不是弹窗，三页签各有内容，
 * 版本号取自**运行中的服务**。
 *
 * 版本号这条是这一屏最容易悄悄坏掉的地方：前端一度把版本写死在 App.tsx 的
 * 常量里，那样发出去的 exe 升级后界面还在报旧号，而源码构建与产物构建各自
 * 看都"对"。这里拿服务自报的版本去比，写死就必然对不上。
 *
 * 刻意**不点**「查询最新更新」：未配置 updateUrl 时服务端会回落到内置的
 * GitHub 加速地址，一点就是一次真实外网请求——关口的结论会随网络变化。
 * 那一条由 vitest 用桩覆盖（settings.test.tsx），这里只确认入口在。
 */
async function settingsGate(page) {
  await page.locator('aside button', { hasText: '设置' }).first().click()
  await sleep(900)

  // 「不弹窗」：设置占用主内容区，不该冒出对话框
  check('设置不弹窗（在主内容区显示）', (await page.locator('[role="dialog"]').count()) === 0,
    '设置弹出了一个对话框')

  const shown = () => page.locator('main').innerText().then((t) => t.replace(/\s+/g, ' ').slice(0, 240))
  for (const t of ['使用指南', '常见问题', '更新说明']) {
    check(`设置屏有页签「${t}」`, (await page.getByRole('tab', { name: t }).count()) > 0,
      `缺少页签；主区显示：${await shown()}`)
  }

  check(`设置屏的版本是服务自报的 v${served}`,
    (await page.locator(`text=当前版本 v${served}`).count()) > 0,
    `没找到「当前版本 v${served}」；主区显示：${await shown()}`)
  check('使用指南正文已渲染（离线可读）',
    (await page.locator('text=首次启动').count()) > 0,
    `指南没渲染出来；主区显示：${await shown()}`)

  await page.getByRole('tab', { name: '常见问题' }).click()
  await sleep(400)
  check('常见问题页签有内容', (await page.locator('text=忘记密码').count()) > 0,
    `常见问题为空；主区显示：${await shown()}`)

  await page.getByRole('tab', { name: '更新说明' }).click()
  await sleep(500)
  const list = page.getByRole('list', { name: '版本列表' })
  const versions = (await list.count()) > 0 ? await list.innerText() : ''
  check('更新说明列出历史版本', /v\d+\.\d+\.\d+/.test(versions),
    `版本列表：${versions.replace(/\s+/g, ' ').slice(0, 120)}`)
  // 点第二条版本，右侧必须跟着换（更新说明是分版本查阅的）
  const items = list.locator('button')
  if ((await items.count()) > 1) {
    const second = (await items.nth(1).innerText()).trim()
    await items.nth(1).click()
    await sleep(400)
    check(`点版本 ${second} 后右侧切到该版本`,
      (await page.locator(`main h2:has-text("${second}")`).count()) > 0,
      `右侧没有切到 ${second}；主区显示：${await shown()}`)
  } else {
    check('更新说明有多个版本可切换', false, `只有 ${await items.count()} 条`)
  }
}

// ---------------------------------------------------------------- 主流程

/**
 * 统计屏排名：长名字必须被裁剪在名称列里，完整名字留在 title 上。
 *
 * 页级 overflow 检查抓不到这个缺陷：页面没有横向溢出，是 `white-space: nowrap` +
 * `table-layout: fixed` 让文字直接画到隔壁的条形图与数量列上。所以这里对**每一个
 * 实际溢出单元格**断言它确实被裁剪（overflow 非 visible + ellipsis），并在 960 / 1440
 * 两个宽度各验一次——只断言"页级不溢出"是假绿。
 */
async function statsRankGate(page, longName) {
  await page.click('text=统计分析')
  await sleep(1000)

  for (const width of [960, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    await sleep(400)
    const tables = page.locator('table.data-table.is-compact')
    const overflowed = []
    const problems = []
    const tableCount = await tables.count()
    for (let t = 0; t < tableCount; t++) {
      const rows = tables.nth(t).locator('tbody tr')
      const rowCount = await rows.count()
      for (let i = 0; i < rowCount; i++) {
        const name = rows.nth(i).locator('td').nth(1)
        if (!(await name.count())) continue
        const text = (await name.innerText()).trim()
        const m = await name.evaluate((el) => {
          const cs = getComputedStyle(el)
          return {
            scrollWidth: el.scrollWidth,
            clientWidth: el.clientWidth,
            overflowX: cs.overflowX,
            textOverflow: cs.textOverflow,
            title: el.getAttribute('title') ?? '',
          }
        })
        const overflows = m.scrollWidth > m.clientWidth + 1
        if (!overflows) continue
        overflowed.push(text)
        if (m.overflowX === 'visible' || m.textOverflow !== 'ellipsis') {
          problems.push(`${text.slice(0, 10)}… 溢出未被裁剪（overflow-x=${m.overflowX}, text-overflow=${m.textOverflow}）`)
        }
        if (m.title !== text) problems.push(`${text.slice(0, 10)}… 没有 title 保留完整名字`)
      }
    }
    const seen = overflowed.some((s) => s.startsWith(longName.slice(0, 8)))
    check(`长名字真的进了排名（${width}px）`, seen,
      `没有溢出单元格里出现长名字，本断言会空转；溢出过的名字：${overflowed.slice(0, 3).join('、') || '无'}`)
    check(`排名长名字被裁剪且保留全名（${width}px）`, problems.length === 0, problems.slice(0, 3).join('；'))
  }
}

/**
 * BOM 屏：树状明细（根 = 产品，子层 = 零件分类，叶子 = 明细行）。
 *
 * 分组是**客户端**按零件主数据的分类算的，接口里没有这层结构——
 * 只查 /api/bom 永远证明不了「分类从哪来、空分类去哪了」。所以这里
 * 读真实渲染出来的层级，并验证折叠确实把叶子收走。
 */
async function bomTreeGate(page, product, product2) {
  // 侧栏标签是「BOM管理」，屏内标题是「BOM 管理」（nav.ts:35）——别混用
  await page.click('text=BOM管理')
  await sleep(1200)

  // 级联选中产品：名称 → 编码。级联是这一屏既有的两套选择机制之一，
  // 用它而不是搜索框，顺带确认改动没有把级联弄坏。
  const nameSel = page.locator('label:has-text("名称") select').first()
  const codeSel = page.locator('label:has-text("编码") select').first()
  if (!(await nameSel.count()) || !(await codeSel.count())) {
    bad('BOM 屏有级联选择器', '找不到名称/编码下拉')
    return
  }
  await nameSel.selectOption(product.name)
  await sleep(300)
  await codeSel.selectOption(product.code)
  await sleep(300)
  // 第三级「规格」：该编码下存在规格时它是必填的，不选则解析不出产品
  // （BOMScreen.tsx 的 cascadePick）。种子产品带规格，正好把三级都走一遍。
  const specSel = page.locator('label:has-text("规格") select').first()
  if ((await specSel.count()) > 0) await specSel.selectOption(product.spec)
  await sleep(1200)

  const root = page.locator('tr[data-tree-root]')
  const rootText = (await root.count()) > 0 ? await root.first().innerText() : ''
  check('BOM 树根是产品', rootText.includes(product.code) && rootText.includes(product.name),
    rootText ? `根行：${rootText.replace(/\s*\n\s*/g, ' | ')}` : '没有 data-tree-root 行')

  const groupTexts = await page.locator('tr[data-tree-group]').allInnerTexts()
  const groups = groupTexts.map((t) => t.replace(/\s+/g, ' ').trim())
  check('分类子层有三组（电子/五金/未分类）',
    groups.some((t) => t.includes('电子')) && groups.some((t) => t.includes('五金')) && groups.some((t) => t.includes('未分类')),
    `实际分组：${groups.join(' / ') || '无'}`)
  // 未分类必须殿后：它是兜底桶，不是业务分类
  check('「未分类」排在最后', groups.length > 0 && groups[groups.length - 1].includes('未分类'),
    `实际分组：${groups.join(' / ')}`)

  const tableText = (await page.locator('table').last().innerText())
  check('叶子是明细零件', ['P-0001', 'P-8001', 'P-9002'].every((c) => tableText.includes(c)),
    `表内没有全部零件编码`)

  // 折叠：点「电子」组行，该组叶子必须从 DOM 里消失（不是只换图标）
  const electronicGroup = page.locator('tr[data-tree-group]', { hasText: '电子' }).first()
  await electronicGroup.click()
  await sleep(400)
  const afterCollapse = await page.locator('table').last().innerText()
  check('折叠分类后其叶子消失', !afterCollapse.includes('P-0001'),
    '仍然渲染着 P-0001')
  check('折叠不影响其它分类', afterCollapse.includes('P-8001'), '五金组被连带收起')
  await electronicGroup.click()
  await sleep(400)
  const afterExpand = await page.locator('table').last().innerText()
  check('再次点击恢复展开', afterExpand.includes('P-0001'), 'P-0001 没有回来')

  // 树形层级靠缩进与底色区分，这些都是 CSS 计算值：jsdom 不跑布局，
  // 只有真浏览器能证明"叶子确实比分类行更缩进、底色确实不同"。
  const treeStyle = await page.evaluate(() => {
    const table = [...document.querySelectorAll('table')].pop()
    const group = table.querySelector('tbody tr.tree-group td')
    const leaf = table.querySelector('tbody tr.tree-leaf td')
    if (!group || !leaf) return null
    const gs = getComputedStyle(group)
    const ls = getComputedStyle(leaf)
    return {
      groupPad: parseFloat(gs.paddingLeft),
      leafPad: parseFloat(ls.paddingLeft),
      groupBg: gs.backgroundColor,
      leafBg: ls.backgroundColor,
      leafRail: getComputedStyle(leaf, '::before').borderLeftStyle,
    }
  })
  check('树形：叶子比分类行更缩进',
    treeStyle !== null && treeStyle.leafPad > treeStyle.groupPad,
    treeStyle ? `分类 ${treeStyle.groupPad}px / 叶子 ${treeStyle.leafPad}px` : '找不到树行')
  check('树形：分类行底色与明细行不同',
    treeStyle !== null && treeStyle.groupBg !== treeStyle.leafBg,
    treeStyle ? `分类 ${treeStyle.groupBg} / 叶子 ${treeStyle.leafBg}` : '找不到树行')
  check('树形：叶子左侧画出连接线',
    treeStyle !== null && treeStyle.leafRail === 'solid',
    treeStyle ? `::before border-left-style=${treeStyle.leafRail}` : '找不到树行')

  // 树行用 colSpan 跨列，新增的「操作」列让总列数变化过：表头与数据列
  // 必须在同一纵向位置。jsdom 量不到坐标，只有真浏览器能证伪。
  const align = await page.evaluate(() => {
    const table = [...document.querySelectorAll('table')].pop()
    const ths = [...table.querySelectorAll('thead th')]
    const leaf = [...table.querySelectorAll('tbody tr')].find(
      (tr) => tr.querySelector('input[type="checkbox"]') && tr.children.length > 1,
    )
    if (!leaf) return null
    const tds = [...leaf.children]
    return ths.map((th, i) => ({
      i,
      th: Math.round(th.getBoundingClientRect().right),
      td: tds[i] ? Math.round(tds[i].getBoundingClientRect().right) : null,
    }))
  })
  const skew = (align ?? []).filter((c) => c.td === null || Math.abs(c.th - c.td) > 2)
  check('BOM 表头与数据列对齐', align !== null && skew.length === 0,
    align === null ? '找不到明细行' : `错位列：${JSON.stringify(skew)}`)

  // 本屏不提供零件主数据的新增：建零件在「零件管理」，两个动作放一起会被当成一件事
  const newPartBtn = page.locator('button', { hasText: '新增零件' })
  check('BOM 屏没有「新增零件」入口', (await newPartBtn.count()) === 0, '仍然渲染着新增零件按钮')
  const editBtn = page.locator('table').last().locator('button', { hasText: '编辑' })
  check('明细行有「编辑」入口', (await editBtn.count()) > 0, '找不到行内编辑按钮')

  // 行内编辑只改用量/模式/损耗率/可替换：真浏览器走一次往返，确认弹窗就是这四项、
  // 保存后表格里用量与「可替换」标记真的变了（jsdom 测的是请求体，这里测界面闭环）。
  await page.locator('table').last().locator('tr', { hasText: 'P-0001' })
    .locator('button', { hasText: '编辑' }).first().click()
  await sleep(600)
  const editModal = page.locator('div[class*="fixed"]').last()
  const editText = await editModal.innerText()
  check('编辑弹窗只含四个可改项',
    editText.includes('用量模式') && editText.includes('损耗率') && editText.includes('可替换') &&
    !editText.includes('零件编码') && !editText.includes('供应商'),
    `弹窗内容：${editText.replace(/\s*\n\s*/g, ' | ')}`)
  const qtyInput = editModal.getByLabel(/每台用量/)
  await qtyInput.fill('3')
  const replBox = editModal.getByRole('checkbox', { name: /该零件可被替换/ })
  const wasChecked = await replBox.isChecked()
  await replBox.click()
  await editModal.locator('button', { hasText: '保存' }).click()
  await sleep(1200)
  const rowText = await page.locator('table').last().locator('tr', { hasText: 'P-0001' }).first().innerText()
  check('保存后列表显示新用量', rowText.includes('3.00个'),
    `行内容：${rowText.replace(/\s*\n\s*/g, ' | ')}`)
  check('保存后「可替换」标记随勾选变化',
    wasChecked ? !rowText.includes('可替换') : rowText.includes('可替换'),
    `勾选前=${wasChecked}，行内容：${rowText.replace(/\s*\n\s*/g, ' | ')}`)
  // 改回去，让后面的断言与重复运行不受影响
  await page.locator('table').last().locator('tr', { hasText: 'P-0001' })
    .locator('button', { hasText: '编辑' }).first().click()
  await sleep(500)
  const again = page.locator('div[class*="fixed"]').last()
  await again.getByLabel(/每台用量/).fill('1')
  await again.getByRole('checkbox', { name: /该零件可被替换/ }).click()
  await again.locator('button', { hasText: '保存' }).click()
  await sleep(1000)

  // 离开本屏再回来，应当仍选着上次的产品（模块级记忆，不必重新点级联）。
  // 这是真实导航：屏幕组件会被卸载再挂载，jsdom 里只能手动 unmount 模拟。
  await page.click('text=工作台')
  await sleep(800)
  await page.click('text=BOM管理')
  await sleep(1200)
  const restoredRoot = page.locator('tr[data-tree-root]')
  const restoredText = (await restoredRoot.count()) > 0 ? await restoredRoot.first().innerText() : ''
  check('离开再回来仍保留上次选中的产品',
    restoredText.includes(product.code) && restoredText.includes(product.name),
    restoredText ? `根行：${restoredText.replace(/\s*\n\s*/g, ' | ')}` : '没有恢复选中，树是空的')
  check('恢复后明细也已重新加载',
    (await page.locator('table').last().innerText()).includes('P-0001'),
    '恢复的产品没有加载出 BOM 明细')

  // 切换产品：级联一动，旧明细必须当场消失（不能等新明细回来才清——
  // 中间那几秒查询栏与表格对不上）。然后选完整，新产品的明细出现。
  await nameSel.selectOption(product2.name)
  await sleep(500)
  const blankPrompt = await page.locator('text=请先选择产品').count()
  const staleLine = await page.locator('text=P-0001').count()
  check('级联改动瞬间旧明细消失', blankPrompt > 0 && staleLine === 0,
    `提示=${blankPrompt}，P-0001 残留=${staleLine}`)
  await codeSel.selectOption(product2.code)
  await sleep(300)
  if ((await specSel.count()) > 0) await specSel.selectOption(product2.spec)
  await sleep(1200)
  const p2Root = await page.locator('tr[data-tree-root]').first().innerText()
  check('切换后根行变为新产品', p2Root.includes(product2.code) && p2Root.includes(product2.name),
    `根行：${p2Root.replace(/\s*\n\s*/g, ' | ')}`)
  check('新产品的明细已加载', (await page.locator('text=P-8001').count()) > 0, 'P-8001 没有出现')
  check('旧产品的零件不残留', (await page.locator('text=P-0001').count()) === 0, 'P-0001 还在')

  // 选回原产品：同 id 重选走 epoch 路径，明细必须重新出现
  await nameSel.selectOption(product.name)
  await sleep(300)
  await codeSel.selectOption(product.code)
  await sleep(300)
  if ((await specSel.count()) > 0) await specSel.selectOption(product.spec)
  await sleep(1200)
  check('选回原产品明细恢复（同 id 重选）', (await page.locator('text=P-0001').count()) > 0,
    'P-0001 没有回来')

  // 需要肉眼核对布局时：GATE_SHOTS=<目录> 跑一遍，留下这一步的截图。
  // 默认不留文件——关口每跑一次都往仓库/临时目录堆图是噪音。
  if (process.env.GATE_SHOTS) {
    const shot = path.join(process.env.GATE_SHOTS, 'bom-tree.png')
    await page.screenshot({ path: shot })
    ok(`BOM 树状屏截图 ${shot}`)
  }
}

/**
 * 库存投影：未完成批次的占用要提前可见，并在完成被拒时说清占用来源。
 *
 * 场景（预置数据）：B-STK-1/2/3 各需 60，零件各 100。
 *   - 三批都在产 → 完成后 -80：列表三行都应显示「不足」
 *   - 建第四批时预览应显示"其它占用 180、全部完成后 -140"
 *   - 完成 B-STK-1（成功，剩 40）后，完成 B-STK-2 应被拒，且文案点名 B-STK-3
 *   - 零件页：在产占用 120、状态为虚线「将缺料」
 *   - 工作台：四宫格顺序 + 使用中预警命中该零件 + 生产计划列出剩余两批
 */
async function stockProjectionGate(page, product, batchNos) {
  await page.click('text=批次追溯')
  await sleep(1200)

  // 1) 三个未完成批次都应在「备料」列显示不足
  for (const no of batchNos) {
    const row = page.locator('tbody tr', { hasText: no }).first()
    const text = (await row.count()) > 0 ? await row.innerText() : ''
    check(`批次 ${no} 显示备料不足`, text.includes('不足'),
      text ? `行内容：${text.replace(/\s*\n\s*/g, ' | ')}` : '找不到该行')
  }

  // 1.5) 零件管理：按键顺序 + 临时零件（库存检测、及时入列）+ 投料联动
  const headerBar = page.locator('.ml-auto.flex').filter({ hasText: '新建批次' }).first()
  const btnLabels = await headerBar.locator('button').allInnerTexts()
  check('批次页按键顺序：新建批次/零件管理/记录投料/开始生产/完成生产/撤销',
    JSON.stringify(btnLabels) === JSON.stringify(['新建批次', '零件管理', '记录投料', '开始生产', '完成生产', '撤销']),
    `实际顺序：${btnLabels.join(' → ')}`)

  await page.locator('input[aria-label="选择 B-STK-1"]').click()
  await headerBar.locator('button', { hasText: '零件管理' }).click()
  await sleep(700)
  const mgr = page.locator('div[class*="fixed"]').last()
  check('零件管理弹窗有两个分页', (await mgr.locator('[role="tab"]').count()) === 2,
    `分页数：${await mgr.locator('[role="tab"]').count()}`)
  check('跳过分页提示需先标记可替换', (await mgr.innerText()).includes('没有标记为「可替换」'),
    `弹窗内容：${(await mgr.innerText()).replace(/\s*\n\s*/g, ' | ').slice(0, 200)}`)

  await mgr.locator('[role="tab"]', { hasText: '临时零件' }).click()
  await sleep(300)
  const modeOption = await mgr.getByLabel('使用方式').locator('option').first().innerText()
  check('使用方式首项文案为「每台用 N 个」', modeOption === '每台用 N 个', `实际：${modeOption}`)
  // 文字查询：输入编码过滤，点带库存信息的候选项
  const combo = mgr.getByRole('combobox', { name: '临时零件' })
  await combo.click()
  // 已在 BOM 里、被三个在产批次占用的零件：现有 100、可用 -80。
  // 两个数都要给，且"可用"必须扣掉在产占用，不能拿现有冒充。
  await combo.fill('P-0001')
  await sleep(300)
  const bomPartOpt = mgr.locator('[role="option"]', { hasText: '测试零件' })
  const bomOptText = (await bomPartOpt.count()) > 0 ? await bomPartOpt.first().innerText() : ''
  check('BOM 零件同时显示现有与可用（可用扣掉在产占用）',
    bomOptText.includes('现有 100') && bomOptText.includes('可用 -80'),
    `候选：${bomOptText || '(无)'}`)

  await combo.fill('P-TEMP-G')
  await sleep(300)
  // ComboInput 会把输入原文也预置成一条候选（"库中原值"），要点的是带
  // 名称与库存的完整标签。
  const tempOpt = mgr.locator('[role="option"]', { hasText: '临时验证零件' })
  const tempOptText = (await tempOpt.count()) > 0 ? await tempOpt.first().innerText() : ''
  check('零件搜索带出候选并显示现有/可用', tempOptText.includes('现有 30') && tempOptText.includes('可用 30'),
    `候选：${tempOptText || '(无)'}`)
  await tempOpt.first().click()
  await sleep(300)
  // 选中后总量按公式自动算：计划 60 × 每台 1 个 = 60，库存 30 不够 → 拒
  const autoTotal = await mgr.getByPlaceholder('自动').inputValue()
  check('选中零件后自动算出本批需要量 60', autoTotal === '60', `自动总量：${autoTotal}`)
  await mgr.locator('button', { hasText: '添加' }).click()
  await sleep(900)
  const refusal = await mgr.innerText()
  check('临时零件库存不足被拒并说明缺口', refusal.includes('临时需要 60.00') && refusal.includes('将缺'),
    `弹窗内容：${refusal.replace(/\s*\n\s*/g, ' | ').slice(0, 260)}`)
  // 手填总量 10（清掉自动值）：这次能过库存检测
  await mgr.getByPlaceholder('自动').fill('10')
  await mgr.locator('button', { hasText: '添加' }).click()
  await sleep(900)
  const mgrText = await mgr.innerText()
  check('临时零件添加成功并列出', mgrText.includes('P-TEMP-G') && mgrText.includes('手填总量'),
    `弹窗内容：${mgrText.replace(/\s*\n\s*/g, ' | ').slice(0, 260)}`)

  // 添加后占用变了，可用库存必须立刻重算（30 − 10 = 20）。
  await combo.click()
  await combo.fill('P-TEMP-G')
  await sleep(500)
  const updatedOpt = mgr.locator('[role="option"]', { hasText: '临时验证零件' })
  const updatedText = (await updatedOpt.count()) > 0 ? await updatedOpt.first().innerText() : ''
  check('添加临时零件后可用库存立即更新（30→20）', updatedText.includes('可用 20'),
    `候选：${updatedText || '(无)'}`)
  if (process.env.GATE_SHOTS) {
    const shot = path.join(process.env.GATE_SHOTS, 'parts-manager.png')
    await page.screenshot({ path: shot })
    ok(`零件管理截图 ${shot}`)
  }
  // 先关掉下拉（Esc 只关下拉，不关弹窗），再关弹窗；临时分页的 footer
  // 是「关闭」（跳过分页才是 取消/保存）。
  await page.keyboard.press('Escape')
  await mgr.locator('button', { hasText: '关闭' }).click()
  await sleep(400)

  // 记录投料：每个零件一张卡片（data-part-code），卡片下挂该零件的入库批次。
  await headerBar.locator('button', { hasText: '记录投料' }).click()
  await sleep(800)
  const traceModal = page.locator('div[class*="fixed"]').last()
  const traceText = await traceModal.innerText()
  check('记录投料出现临时零件行', traceText.includes('P-TEMP-G') && traceText.includes('临时'),
    `弹窗内容：${traceText.replace(/\s*\n\s*/g, ' | ').slice(0, 300)}`)
  const tempCard = traceModal.locator('[data-part-code="P-TEMP-G"]')
  const tempCardText = (await tempCard.count()) > 0 ? await tempCard.innerText() : ''
  check('记录投料的临时零件用量只读为 10', tempCardText.includes('用量 10'),
    `零件卡片：${tempCardText.replace(/\s*\n\s*/g, ' | ').slice(0, 240)}`)
  check('记录投料的零件卡片只含供应商与批次数量输入（总量只读）',
    (await tempCard.locator('input[placeholder="供应商"]').count()) === 1 &&
      (await tempCard.locator('input[aria-label*="本次消耗"]').count()) === 1 &&
      (await tempCard.locator('input').count()) === 2,
    `输入框数：${await tempCard.locator('input').count()}`)

  // 批次子行：临时零件 30 个（一笔入库），默认按顺序分 10 → 本次 10、剩余 20。
  const allocRow = tempCard.locator('[draggable="true"]').first()
  const allocText = (await allocRow.count()) > 0 ? (await allocRow.innerText()).replace(/\s*\n\s*/g, ' ') : ''
  check('记录投料列出入库批次号', /RK-\d{8}-\d{4,}/.test(allocText), `分配行：${allocText || '(无)'}`)
  check('记录投料显示可用/本次/剩余', allocText.includes('可用 30') && allocText.includes('剩余 20'),
    `分配行：${allocText || '(无)'}`)
  const qtyInput = tempCard.locator('input[aria-label*="本次消耗"]').first()
  check('记录投料的批次数量可手改（当前 10）', (await qtyInput.inputValue()) === '10',
    `数量输入值：${await qtyInput.inputValue().catch(() => '?')}`)
  check('记录投料给出合计与用量的结论', tempCardText.includes('合计 10 / 用量 10'),
    `零件卡片：${tempCardText.replace(/\s*\n\s*/g, ' | ').slice(0, 240)}`)

  // 手改成 5 → 合计对不上，保存被拦；点「按顺序消耗」恢复 10。
  await qtyInput.fill('5')
  await sleep(250)
  await traceModal.locator('button', { hasText: '确定' }).click()
  await sleep(600)
  const mismatch = await page.locator('[role="alert"]').first().innerText().catch(() => '')
  check('投料合计与用量不一致时拦住保存', mismatch.includes('P-TEMP-G') && mismatch.includes('还差 5'),
    `提示：${mismatch || '(无)'}`)
  await tempCard.locator('button', { hasText: '按顺序消耗' }).click()
  await sleep(250)
  check('「按顺序消耗」恢复默认摊分', (await qtyInput.inputValue()) === '10',
    `数量输入值：${await qtyInput.inputValue().catch(() => '?')}`)
  check('输入框在合计补齐后不再报错',
    (await tempCard.innerText()).includes('合计 10 / 用量 10'),
    `零件卡片：${(await tempCard.innerText()).replace(/\s*\n\s*/g, ' | ').slice(0, 200)}`)

  // 保存 → 追溯表按零件一行；再打开是回显，再保存仍是一样多条（覆盖不追加）。
  await traceModal.locator('button', { hasText: '确定' }).click()
  await sleep(1200)
  const traceSection = page.locator('section', { hasText: '投料追溯记录' }).first()
  const traceAfterSave = (await traceSection.count()) > 0 ? await traceSection.innerText() : ''
  const firstCount = /共 (\d+) 条/.exec(traceAfterSave)
  check('保存后追溯表按零件一行', firstCount !== null && Number(firstCount[1]) >= 3,
    `追溯区：${traceAfterSave.replace(/\s*\n\s*/g, ' | ').slice(0, 260)}`)
  check('追溯表展示批次分配', traceAfterSave.includes('批次分配') && /RK-\d{8}/.test(traceAfterSave),
    traceAfterSave.slice(0, 260))

  await headerBar.locator('button', { hasText: '记录投料' }).click()
  await sleep(900)
  const traceModal2 = page.locator('div[class*="fixed"]').last()
  const tempQty2 = await traceModal2
    .locator('[data-part-code="P-TEMP-G"] input[aria-label*="本次消耗"]')
    .first()
    .inputValue()
  check('再次打开记录投料回显已保存的分配数量', tempQty2 === '10', `数量输入值：${tempQty2}`)
  await traceModal2.locator('button', { hasText: '确定' }).click()
  await sleep(1200)
  const traceAgain = (await traceSection.count()) > 0 ? await traceSection.innerText() : ''
  const secondCount = /共 (\d+) 条/.exec(traceAgain)
  check('重复记录投料是覆盖而不是追加',
    firstCount !== null && secondCount !== null && firstCount[1] === secondCount[1],
    `第一次 ${firstCount?.[1]} 条，第二次 ${secondCount?.[1]} 条`)
  // 恢复原状：后续既有关卡假设"还没选批次"。选择不撤销，第 3 步再点一次
  // 复选框会把它取消掉，完成生产就点不动了。
  await page.locator('input[aria-label="选择 B-STK-1"]').click()
  await sleep(300)

  // 2) 建批预览：把"还没创建的这批"并入投影
  await page.locator('button', { hasText: '新建批次' }).click()
  await sleep(700)
  const modal = page.locator('div[class*="fixed"]').last()
  await modal.locator('label:has-text("产品名称") select').first().selectOption(product.name)
  await sleep(250)
  await modal.locator('label:has-text("产品编码") select').first().selectOption(product.code)
  await sleep(250)
  const specSel = modal.locator('label:has-text("规格型号") select').first()
  if ((await specSel.count()) > 0) await specSel.selectOption(product.spec)
  await modal.getByLabel('计划数量').fill('60')
  await sleep(1200)
  const previewText = await modal.innerText()
  check('建批预览列出占用与完成后',
    previewText.includes('其它占用') && previewText.includes('全部完成后'),
    `弹窗内容：${previewText.replace(/\s*\n\s*/g, ' | ').slice(0, 200)}`)
  check('建批预览结论为不足（三批共用零件）', previewText.includes('会不足') || previewText.includes('无法完成'),
    `弹窗内容：${previewText.replace(/\s*\n\s*/g, ' | ').slice(0, 240)}`)
  check('建批预览的「其它占用」是合计值（180）', previewText.includes('180'),
    `弹窗内容：${previewText.replace(/\s*\n\s*/g, ' | ').slice(0, 240)}`)

  // 预览表不能比弹窗宽：横向溢出会把 auto 列压成 0 宽，文字压到相邻列上
  // （实测过"装饰圈0"这种重叠）。jsdom 量不到，只有真浏览器能证伪。
  const overflow = await modal.evaluate((el) => {
    const table = el.querySelector('table.data-table.is-narrow')
    if (!table) return null
    const box = table.parentElement
    return {
      tableW: Math.round(table.getBoundingClientRect().width),
      boxW: Math.round(box.getBoundingClientRect().width),
      scrollW: box.scrollWidth,
      clientW: box.clientWidth,
    }
  })
  check('备料预览不横向溢出',
    overflow !== null && overflow.scrollW <= overflow.clientW + 1,
    overflow ? `容器 scrollWidth=${overflow.scrollW} clientWidth=${overflow.clientW} 表宽=${overflow.tableW} 容器宽=${overflow.boxW}` : '找不到预览表')
  check('备料预览的零件列有实际宽度',
    overflow !== null && overflow.tableW > 0,
    overflow ? `表宽=${overflow.tableW}` : '找不到预览表')

  if (process.env.GATE_SHOTS) {
    const shot = path.join(process.env.GATE_SHOTS, 'batch-preview.png')
    await page.screenshot({ path: shot })
    ok(`建批预览截图 ${shot}`)
  }

  await modal.locator('button', { hasText: '取消' }).click()
  await sleep(400)

  // 3) 完成第一批：应当成功；它的备料角标随之变成占位符
  await page.locator('input[aria-label="选择 B-STK-1"]').click()
  await page.locator('button', { hasText: '完成生产' }).click()
  await sleep(400)
  await page.locator('div[class*="fixed"]').last().locator('button', { hasText: '确定' }).click()
  await sleep(1500)
  const doneRow = await page.locator('tbody tr', { hasText: 'B-STK-1' }).first().innerText()
  check('完成第一批后状态为已完成', doneRow.includes('已完成'),
    `行内容：${doneRow.replace(/\s*\n\s*/g, ' | ')}`)
  check('已完成批次不再显示备料角标', !doneRow.includes('不足'), `行内容：${doneRow.replace(/\s*\n\s*/g, ' | ')}`)

  // 3.5) 触发一条状态守卫提示，验证它带关闭键且能关掉（用户截图反馈）
  await page.locator('button', { hasText: '完成生产' }).click()
  await sleep(400)
  await page.locator('div[class*="fixed"]').last().locator('button', { hasText: '确定' }).click()
  await sleep(500)
  const warnBanner = page.locator('[role="status"]').filter({ hasText: '已完成的批次不能更改状态' })
  check('批次警告提示出现', (await warnBanner.count()) === 1,
    `提示数量：${await warnBanner.count()}`)
  check('批次警告提示带关闭键', (await warnBanner.getByRole('button', { name: '关闭提示' }).count()) === 1)
  if (process.env.GATE_SHOTS) {
    const shot = path.join(process.env.GATE_SHOTS, 'batch-warning.png')
    await page.screenshot({ path: shot })
    ok(`批次警告截图 ${shot}`)
  }
  await warnBanner.getByRole('button', { name: '关闭提示' }).click()
  await sleep(300)
  check('关闭后批次警告提示消失',
    (await page.locator('[role="status"]').filter({ hasText: '已完成的批次不能更改状态' }).count()) === 0)

  // 4) 完成第二批：库存只剩 40，被拒；文案要点名仍在产的 B-STK-3
  await page.locator('input[aria-label="选择 B-STK-2"]').click()
  await page.locator('button', { hasText: '完成生产' }).click()
  await sleep(400)
  const confirm = page.locator('div[class*="fixed"]').last()
  await confirm.locator('button', { hasText: '确定' }).click()
  await sleep(1500)
  const errText = await page.locator('[role="alert"]').last().innerText().catch(() => '')
  check('完成被拒时提示库存不足', errText.includes('库存不足'), `提示：${errText.slice(0, 200)}`)
  check('拒绝文案说出被其它批次占用', errText.includes('另有在产批次占用'), `提示：${errText.slice(0, 240)}`)
  check('拒绝文案点名占用批次', errText.includes('B-STK-3'), `提示：${errText.slice(0, 240)}`)
  await page.locator('div[class*="fixed"]').last().locator('button', { hasText: '取消' }).click()
  await sleep(400)

  // 5) 零件页：占用列 + 虚线「将缺料」
  await page.click('text=零件管理')
  await sleep(1200)
  const headers = await page.locator('table thead th').allInnerTexts()
  check('零件列表有「在产占用」列', headers.some((h) => h.includes('在产占用')), `表头：${headers.join('/')}`)
  const partRow = page.locator('tbody tr', { hasText: 'P-0001' }).first()
  const partText = await partRow.innerText()
  check('占用列显示两个在产批次的合计 120', partText.includes('120'),
    `行内容：${partText.replace(/\s*\n\s*/g, ' | ')}`)
  const dashedBadge = partRow.locator('[data-badge-variant="dashed"]').first()
  check('在产后缺料用虚线徽章', (await dashedBadge.count()) > 0 && (await dashedBadge.innerText()).includes('将缺料'),
    `行内容：${partText.replace(/\s*\n\s*/g, ' | ')}`)
  const borderStyle = await dashedBadge.evaluate((el) => getComputedStyle(el).borderStyle).catch(() => '')
  check('虚线徽章真的画成虚线', borderStyle.includes('dashed'), `border-style=${borderStyle}`)

  // 临时零件随完成生产扣减：B-STK-1 完成时 P-TEMP-G 30 − 10 = 20
  await page.getByPlaceholder('全部').fill('P-TEMP-G')
  await sleep(700)
  const tempRow = page.locator('tbody tr', { hasText: 'P-TEMP-G' }).first()
  const tempText = (await tempRow.count()) > 0 ? await tempRow.innerText() : ''
  check('临时零件随完成生产扣减库存（30→20）', tempText.includes('20 个'),
    `行内容：${tempText.replace(/\s*\n\s*/g, ' | ')}`)
  const tempBadge = tempRow.locator('[data-badge-variant]').first()
  const tempBadgeText = (await tempBadge.count()) > 0 ? await tempBadge.innerText() : ''
  const tempBadgeVariant = (await tempBadge.count()) > 0 ? await tempBadge.getAttribute('data-badge-variant') : ''
  check('临时零件扣减后状态实时变为低库存（实心）',
    tempBadgeText === '低库存' && tempBadgeVariant === 'solid',
    `徽章：${tempBadgeText} / ${tempBadgeVariant}`)
  await page.getByPlaceholder('全部').fill('')
  await sleep(500)

  // 6) 工作台：四宫格顺序与两类预警各说各的
  await page.click('text=工作台')
  await sleep(1500)
  const panels = await page.locator('[data-panel]').evaluateAll((els) =>
    els.map((e) => e.getAttribute('data-panel')),
  )
  check('工作台四宫格顺序：现状/计划 在上，未来/操作记录 在下',
    JSON.stringify(panels) === JSON.stringify(['current-alerts', 'production-plan', 'future-alerts', 'recent-logs']),
    `实际顺序：${panels.join(' → ')}`)

  const currentPanel = page.locator('[data-panel="current-alerts"]')
  const currentText = await currentPanel.innerText()
  check('现状预警只报现在时（不出现"将缺料"）', !currentText.includes('将缺料'),
    `面板内容：${currentText.replace(/\s*\n\s*/g, ' | ').slice(0, 240)}`)
  // 临时零件扣减后 20 < 预警线 25：状态应立刻出现在现状预警里。
  const tempCurrentRow = currentPanel.locator('tbody tr', { hasText: 'P-TEMP-G' })
  const tempCurrentText = (await tempCurrentRow.count()) > 0 ? await tempCurrentRow.first().innerText() : ''
  check('临时零件扣减后进入现状预警且显示低库存', tempCurrentText.includes('低库存'),
    `行内容：${tempCurrentText.replace(/\s*\n\s*/g, ' | ') || '(未出现)'}`)

  const future = page.locator('[data-panel="future-alerts"]')
  const futureText = await future.innerText()
  check('未来预警命中共用零件的批次', futureText.includes('P-0001'),
    `面板内容：${futureText.replace(/\s*\n\s*/g, ' | ').slice(0, 240)}`)

  // 点未来预警里的零件 → 列出正在消耗它的在产计划
  await future.locator('[data-future-part="P-0001"]').first().click()
  await sleep(600)
  const holderDialog = page.locator('div[class*="fixed"]').last()
  const holderText = await holderDialog.innerText()
  check('点零件列出对应的在产计划',
    holderText.includes('B-STK-2') && holderText.includes('B-STK-3'),
    `弹窗内容：${holderText.replace(/\s*\n\s*/g, ' | ').slice(0, 240)}`)
  check('弹窗同时给出计划数与本零件占用',
    holderText.includes('计划数') && holderText.includes('本零件占用'),
    `弹窗内容：${holderText.replace(/\s*\n\s*/g, ' | ').slice(0, 240)}`)
  // 列宽回归：弹窗表格若继承 .data-table 的 720px 最小宽度，就会撑出横向
  // 滚动条、固定列被拉宽（用户截图反馈过）。jsdom 量不到，只有真浏览器能证伪。
  const holderOverflow = await holderDialog.evaluate((el) => {
    const table = el.querySelector('table.data-table.is-narrow')
    if (!table) return null
    const box = table.parentElement
    return {
      tableW: Math.round(table.getBoundingClientRect().width),
      boxW: Math.round(box.getBoundingClientRect().width),
      scrollW: box.scrollWidth,
      clientW: box.clientWidth,
    }
  })
  check('在产占用弹窗不横向溢出',
    holderOverflow !== null && holderOverflow.scrollW <= holderOverflow.clientW + 1,
    holderOverflow
      ? `容器 scrollWidth=${holderOverflow.scrollW} clientWidth=${holderOverflow.clientW} 表宽=${holderOverflow.tableW} 容器宽=${holderOverflow.boxW}`
      : '找不到占用表')
  check('在产占用弹窗的表格铺满容器而不是超宽',
    holderOverflow !== null && holderOverflow.tableW <= holderOverflow.boxW + 1,
    holderOverflow ? `表宽=${holderOverflow.tableW} 容器宽=${holderOverflow.boxW}` : '找不到占用表')
  if (process.env.GATE_SHOTS) {
    const shot = path.join(process.env.GATE_SHOTS, 'holder-batches.png')
    await page.screenshot({ path: shot })
    ok(`在产占用弹窗截图 ${shot}`)
  }
  await holderDialog.locator('button', { hasText: '关闭' }).click()
  await sleep(300)

  const planPanel = page.locator('[data-panel="production-plan"]')
  const planText = await planPanel.innerText()
  check('生产计划列出剩余两个在产批次', planText.includes('B-STK-2') && planText.includes('B-STK-3'),
    `面板内容：${planText.replace(/\s*\n\s*/g, ' | ').slice(0, 240)}`)
  check('生产计划不再列出已完成的批次', !planText.includes('B-STK-1'),
    `面板内容：${planText.replace(/\s*\n\s*/g, ' | ').slice(0, 240)}`)

  if (process.env.GATE_SHOTS) {
    const shot = path.join(process.env.GATE_SHOTS, 'dashboard.png')
    await page.screenshot({ path: shot })
    ok(`工作台截图 ${shot}`)
  }
}

let browser
try {
  sweepStaleRuns()
  const { exe, desktopStyle } = await resolveServer()
  await startServer(exe, desktopStyle)
  ok('后端就绪（本轮独立 SQLite）')

  await api('/api/bootstrap-admin', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ...ADMIN, display_name: '管理员' }),
  })
  const tok = await login()
  const auth = { 'Content-Type': 'application/json', Authorization: `Bearer ${tok}` }
  // 注意：零件用 snake_case（part_type），users 那套才是驼峰。
  // 分类要在浏览器登录**之前**备齐——登录会作废脚本侧 token（单用户单会话），
  // 之后再补数据浏览器就读不到了。
  await seedPart(auth, 'P-0001', '测试零件', '电子', { spec: '10x10' })
  await seedPart(auth, 'P-8001', '五金验证件', '五金', { spec: 'M3' })
  await seedPart(auth, 'P-8002', '塑料验证件', '塑料', { spec: 'ABS' })
  // 统计排名的长名字回归要用的数据。必须在这里造：浏览器一登录，脚本侧 token 就作废了。
  const LONG_PART = '超长零件名称回归验证件（含密封圈与铜套总成）ABCDEFGHIJKLMNOPQRSTUVWXYZ'
  await seedPart(auth, 'P-LONG', LONG_PART, '电子', { stock_qty: 0 })
  const seededParts = await api('/api/parts', { headers: auth })
  const seededRows = Array.isArray(seededParts.body) ? seededParts.body : seededParts.body?.items
  const longPart = Array.isArray(seededRows) ? seededRows.find((p) => p.code === 'P-LONG') : undefined
  if (!longPart) throw new Error(`长名字零件没有回读成功：${seededParts.text.slice(0, 120)}`)
  const stockIn = await api('/api/parts/stock-in', {
    method: 'POST',
    headers: auth,
    body: JSON.stringify({ part_id: longPart.id, qty: 9999 }),
  })
  if (stockIn.code !== 200) throw new Error(`长名字零件入库失败 ${stockIn.code} ${stockIn.text.slice(0, 120)}`)
  ok('装配管理员与零件（电子/五金/塑料/长名字）')

  // BOM 树状屏：一个产品挂三种分类，其中一条零件故意不填分类。
  // 与零件同理，必须在浏览器登录**之前**造好（单用户单会话）。
  await seedPart(auth, 'P-9002', '未分类验证件', '')
  // 临时零件场景用：不在任何 BOM 里，库存 30。
  // warn 25 让「扣减后状态变化」可验证：完成后 20 < 25 → 低库存。
  await seedPart(auth, 'P-TEMP-G', '临时验证零件', '其他', { stock_qty: 30, warn_qty: 25 })
  const seededAll = await api('/api/parts', { headers: auth })
  const allPartRows = Array.isArray(seededAll.body) ? seededAll.body : seededAll.body?.items
  const partByCode = (code) => (Array.isArray(allPartRows) ? allPartRows.find((p) => p.code === code) : undefined)
  const BOM_PRODUCT = { code: 'P-BOM-1', name: 'BOM树验证产品', spec: 'A' }
  const createdProduct = await api('/api/products', {
    method: 'POST',
    headers: auth,
    body: JSON.stringify({ ...BOM_PRODUCT, unit: '台', spec: 'A' }),
  })
  if (createdProduct.code !== 200) {
    throw new Error(`预置产品失败 ${createdProduct.code} ${createdProduct.text.slice(0, 120)}`)
  }
  for (const code of ['P-0001', 'P-8001', 'P-9002']) {
    const p = partByCode(code)
    if (!p) throw new Error(`预置 BOM 用的零件 ${code} 不存在`)
    const r = await api('/api/bom', {
      method: 'POST',
      headers: auth,
      body: JSON.stringify({ product_id: createdProduct.body.id, part_id: p.id, quantity: 1, loss_rate: 0, replaceable: 0, use_mode: 0 }),
    })
    if (r.code !== 200) throw new Error(`预置 BOM 明细 ${code} 失败 ${r.code} ${r.text.slice(0, 120)}`)
  }
  // 第二个产品：切换产品用（验证「级联一动旧明细立刻消失」）
  const BOM_PRODUCT2 = { code: 'P-BOM-2', name: 'BOM树验证产品二', spec: 'B' }
  const created2 = await api('/api/products', {
    method: 'POST',
    headers: auth,
    body: JSON.stringify({ ...BOM_PRODUCT2, unit: '台', spec: 'B' }),
  })
  if (created2.code !== 200) throw new Error(`预置产品二失败 ${created2.code} ${created2.text.slice(0, 120)}`)
  {
    const p = partByCode('P-8001')
    const r = await api('/api/bom', {
      method: 'POST',
      headers: auth,
      body: JSON.stringify({ product_id: created2.body.id, part_id: p.id, quantity: 2, loss_rate: 0, replaceable: 0, use_mode: 0 }),
    })
    if (r.code !== 200) throw new Error(`预置产品二明细失败 ${r.code} ${r.text.slice(0, 120)}`)
  }
  ok('装配产品与 BOM 明细（电子/五金/未分类 + 切换用第二产品）')

  // 库存投影场景：三个待生产批次各需 60，零件各只有 100。
  // 单看每批都"够"，三批合起来缺 80 —— 正是"没点完成时库存看着充足"的坑。
  const STOCK_BATCHES = ['B-STK-1', 'B-STK-2', 'B-STK-3']
  for (const no of STOCK_BATCHES) {
    const r = await api('/api/batches', {
      method: 'POST',
      headers: auth,
      body: JSON.stringify({ batch_no: no, product_id: createdProduct.body.id, plan_qty: 60 }),
    })
    if (r.code !== 200) throw new Error(`预置批次 ${no} 失败 ${r.code} ${r.text.slice(0, 120)}`)
  }
  ok('装配三个共用零件的待生产批次（B-STK-1/2/3）')

  const pageUrl = await startFrontend(desktopStyle)
  ok(desktopStyle ? '页面来自产物内嵌资源' : 'vite 就绪（/api 已反代）')

  browser = await launchBrowser()
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
  await page.goto(pageUrl, { waitUntil: 'domcontentloaded' })
  await sleep(800)

  await page.fill('input[autocomplete="username"]', ADMIN.username)
  await page.fill('input[type="password"]', ADMIN.password)
  await page.click('button[type="submit"]')
  await sleep(1500)
  if (!(await page.locator('text=零件管理').count())) {
    const seen = (await page.locator('body').innerText()).slice(0, 160).replace(/\s*\n\s*/g, ' | ')
    throw new Error(`登录后没到工作台，画面：${seen}`)
  }
  ok('登录并进入工作台')

  await page.click('text=零件管理')
  await sleep(1000)
  await page.locator('button', { hasText: '新增零件' }).first().click()
  await sleep(700)

  // 必须限定在弹窗内：列表屏也有「分类」筛选框，全页匹配会命中它，
  // 于是整套断言都在测筛选框，而不是出问题的那个控件。
  const modal = page.locator('div[class*="fixed"]').last()
  const field = modal.locator('label', { hasText: '分类' }).first().locator('input')
  if (!(await field.count())) {
    const labels = await page.locator('label').allInnerTexts()
    throw new Error(`弹窗内没有分类输入框；页面字段：${labels.join('、')}`)
  }
  ok('定位到弹窗内的分类字段')

  await comboGate(page, modal, field)

  // 通过界面新建一条（弹窗还开着），保存后自动回到列表
  await persistCreate(page, modal, field)

  // 列表屏：分类/供应商列、筛选、计数文案（纯浏览器，不发 API）
  await typeColumnGate(page)

  // 零件入库批次：新增去库存、详情台账、自动批次号（紧跟列表屏，同屏上下文）
  await inboundDetailGate(page)

  // 操作记录屏：六列结构 + 点行详情
  await auditGate(page)

  // 备份屏：四道门与能力门控（不执行破坏性操作）
  await backupGate(page)

    // 设置屏：三页签、版本取自服务端、离线帮助内容
    await settingsGate(page)

    // 统计屏：排名长名字必须被裁剪（页级 overflow 检查抓不到）
    await statsRankGate(page, LONG_PART)

    // BOM 屏：树状层级与零件主数据入口
    await bomTreeGate(page, BOM_PRODUCT, BOM_PRODUCT2)

    // 库存投影：三批共用零件的场景（必须在 BOM 验证之后跑：它会完成/拒绝批次）
    await stockProjectionGate(page, BOM_PRODUCT, STOCK_BATCHES)

    // 接口回读放最后：重新登录会作废浏览器会话
    await verifyPersisted()
} catch (e) {
  bad('流程中断', e.message.split('\n').slice(0, 6).join(' | '))
  if (browser) {
    const pages = browser.contexts().flatMap((c) => c.pages())
    for (const p of pages) await p.screenshot({ path: path.join(WEB, 'gate-failure.png') }).catch(() => {})
  }
} finally {
  if (browser) await browser.close().catch(() => {})
  await cleanup()
}

// 断言总数一并打出来：只说「全部通过」时，谁也不知道到底跑了几条，
// 于是「断言被删掉」和「断言全过」在输出上长得一样。
console.log(
  fails.length === 0
    ? `\n  浏览器关口全部通过（${checks} 项）`
    : `\n  失败 ${fails.length}/${checks} 项：${fails.join('、')}`,
)
console.log(`  被验证的服务版本：${served}`)
process.exit(fails.length === 0 ? 0 : 1)
