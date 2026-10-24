/**
 * 备份导出。
 *
 * 结构对齐 Fyne 版 internal/ui/backup.go 的六个区块（数据目录 / 一键备份 /
 * 整库恢复 / 审计导出 / 全量导出），但**四道门做成了可复用组件**——
 * §Round 5 要求恢复与清空共用同一套，否则两处独立实现必然漂移，而这里漂移的
 * 后果是让用户在数据可能已经损坏时又点一次恢复。
 *
 * 四道门（§Round 5 原文）：
 *   G1 文件合法      —— 从备份列表里选；无法导出时间戳的文件直接拒（不是恢复）
 *   G2 输入确认口令  —— 令牌随所选文件变化，**不可预填、不可粘贴**
 *   G3 列明后果      —— 逐条列出，确认框
 *   G4 执行中禁用    —— 整段禁用按钮
 *
 * 与 Fyne 版的两处**有意差异**：
 *   - 令牌不预填（那边 ui/backup.go:439 选中即填）。预填 + 可粘贴等于把 G2
 *     降级成勾选框——用户不需要读懂任何东西就能划过去。
 *   - 恢复结果三分类分色 + 不渲染重试按钮（那边只有标题分三类）。
 */
import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { ApiError, api, saveBlob, usePermissions } from '../lib/api'
import { canWrite } from '../lib/permissions'
import {
  AUTO_BACKUP_DAY_OPTIONS,
  CLEAR_CONFIRM_TOKEN,
  autoBackupDayLabel,
  backupApi,
  classifyDirPick,
  groupByCategory,
  isDirPickUnavailable,
  restoreTokenFor,
  tablesLostOnRestore,
  type AutoBackupSchedule,
  type BackupFile,
  type ClearResult,
  type DataDirInfo,
  type RestoreResult,
} from '../lib/backup'
import { Btn, Field, Panel, ScreenError, classifyLoadError } from '../components/ui'
import { ConfirmDialog, Modal } from '../components/form'
import { OutcomeDialog, type OutcomeView } from './backupOutcome'
import { RestoreRestart } from './restoreRestart'

const today = () => new Date().toISOString().slice(0, 10)

/**
 * 远程形态的「下载 CSV」按钮是否开放。
 *
 * 代码路径已经就绪：服务端有 POST /api/export/audit/download（CSV 文件流），
 * internal/api/client.go 也指向它，远程弹窗里按钮与 handler 都写好了。
 *
 * 但**远程联机本身还没完成**（计划 §Round 2 的"运行模式选择"暂缓，
 * 详见 wails-migration.md）。在那之前，远程用户点导出只会拿到一个
 * **打不开的服务器路径**——而一个同样打不开的"下载"按钮只会让他以为是坏的。
 *
 * 所以现在先隐藏。等远程形态真正可用时把这个开关打开即可，代码不用动。
 */
const REMOTE_EXPORT_DOWNLOAD_AVAILABLE = false

/**
 * 下拉框里每项的后缀标记。
 *
 * 放在文件名旁边而不是等到选中之后才提示，是因为下拉框是用户比较候选的唯一界面：
 * 3 份同一时刻的备份（.sql / .upgraded.sql / .final.sql）光看名字分不出好坏，
 * 而它们恰恰是回滚结果不同的那几份。
 */
function fileBadge(f: BackupFile): string {
  if (f.restorable === false) return '　⚠ 不可用'
  if (f.schemaKnown === false) return '　（覆盖度未确认）'
  const lost = tablesLostOnRestore(f)
  if (lost.length > 0) return `　⚠ 缺 ${lost.length} 张表`
  return '　✓ 可完整回滚'
}

export function BackupScreen() {
  const { modules, hostOperations } = usePermissions()
  const canEdit = canWrite(modules, 'backup')

  const [dir, setDir] = useState<DataDirInfo | null>(null)
  const [files, setFiles] = useState<BackupFile[]>([])
  const [error, setError] = useState<{ kind: 'forbidden' | 'server'; text: string } | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [notice, setNotice] = useState('')

  // 恢复 / 清空的状态
  const [picked, setPicked] = useState('')
  // 令牌连同它是为哪个文件输入的一起存。换文件后 token 自动失效（渲染期判定），
  // 而不是用 effect 去 setToken('')：留着上一个文件的令牌会让用户以为已经过了
  // 这道门，而 effect 里同步 setState 还会多一轮渲染、让 React Compiler 放弃优化。
  const [tokenInput, setTokenInput] = useState<{ file: string; text: string } | null>(null)
  const token = tokenInput && tokenInput.file === picked ? tokenInput.text : ''
  const [confirmRestore, setConfirmRestore] = useState(false)
  const [confirmClear, setConfirmClear] = useState(false)
  const [dropText, setDropText] = useState('')
  const [outcome, setOutcome] = useState<OutcomeView | null>(null)
  const [restored, setRestored] = useState<RestoreResult | null>(null)
  const [restartUncertain, setRestartUncertain] = useState(false)
  const [cleared, setCleared] = useState<ClearResult | null>(null)

  // 导出
  const [from, setFrom] = useState(today())
  const [to, setTo] = useState(today())

  // 「更改数据目录」。manualDir 为 null = 不显示手填输入框；非 null = 显示。
  //
  // 为什么要这个降级态：原生目录框由宿主注入（方案丙），cmd/server 与浏览器
  // 里根本没有窗口可弹。那种形态下不能让用户对着一个按了没反应的按钮发呆，
  // 所以那条端点会明确回 501，界面据此把输入框亮出来。
  const [manualDir, setManualDir] = useState<string | null>(null)

  // 自动定期备份的设置与状态
  const [auto, setAuto] = useState<AutoBackupSchedule | null>(null)
  // 导出结果（审计日志 / 全量导出共用同一个弹窗）
  const [exported, setExported] = useState<{
    title: string
    dir: string
    files: string[]
    /** 远程形态下才提供"下载"：文件在服务器上，打开文件夹没有意义 */
    downloadable?: boolean
  } | null>(null)

  useEffect(() => {
    let alive = true
    Promise.all([backupApi.dataDir(), backupApi.listFiles(), backupApi.autoSchedule()])
      .then(([d, f, a]) => {
        if (!alive) return
        setDir(d)
        setFiles(f.files)
        setAuto(a)
        setError(null)
      })
      .catch((e) => {
        if (alive) setError(classifyLoadError(e, '备份'))
      })
    return () => {
      alive = false
    }
  }, [])

  /** 破坏性操作之后要刷新：副本会出现在备份列表里，数据目录也可能变过。 */
  const reload = useCallback(async () => {
    try {
      const [d, f] = await Promise.all([backupApi.dataDir(), backupApi.listFiles()])
      setDir(d)
      setFiles(f.files)
    } catch {
      /* 列表刷新失败不该盖掉刚才的操作结果，错误会在下次进入屏时出现 */
    }
  }, [])

  const fileGroups = useMemo(() => groupByCategory(files), [files])

  /**
   * 改自动备份间隔。
   *
   * 失败时**不**本地兜底成别的值：服务端拒绝非法值之后，界面就把服务端的话
   * 显示出来并恢复原状。前端悄悄改成邻近的合法档，用户会以为每 4 天有一份、
   * 实际每 5 天——而真正需要备份的那一刻正好落在空档里。
   */
  async function setAutoDays(days: number) {
    setBusy('autoschedule')
    setError(null)
    try {
      setAuto(await backupApi.setAutoSchedule(days))
      await reload()
    } catch (e) {
      setAuto((prev) => prev)
      setError({
        kind: e instanceof ApiError && e.isForbidden ? 'forbidden' : 'server',
        text: `设置自动备份失败：${e instanceof ApiError ? e.message : e instanceof Error ? e.message : '未知错误'}`,
      })
    } finally {
      setBusy(null)
    }
  }

  // 破坏性操作需要「写备份模块」且「本机形态」两个条件，缺一不可。
  const destructiveReady = canEdit && hostOperations
  const pickedFile = files.find((f) => f.name === picked) ?? null
  const wantToken = pickedFile ? restoreTokenFor(pickedFile.name) : null
  // 令牌是**两道**门，不止文件名那一道：服务端已判这份文件不可用
  // （非 UTF-8 编码）时不该再让用户输口令——输了也是在最后一步才失败。
  const pickedUsable = pickedFile?.restorable !== false
  const lostTables = pickedFile ? tablesLostOnRestore(pickedFile) : []
  // schemaKnown === false：预检只读到文件开头，覆盖度**未知**。
  // 把它当成"没问题"是不诚实的，但也绝不能反过来谎报缺哪几张表。
  const coverageUnknown = pickedFile?.schemaKnown === false

  function onDestructiveError(op: string, e: unknown, safetyCopy?: string) {
    if (e instanceof ApiError) {
      if (e.isForbidden) {
        setError({ kind: 'forbidden', text: `${op}只能在服务器所在机器上执行。请在主机上操作。` })
        return
      }
      setOutcome({
        op,
        // 缺 state 时按 rolled_back 处理：服务端对普通错误的语义就是「已确认回滚」，
        // 把它误判成 unknown 只会让用户白白不敢重试。
        state: e.state ?? 'rolled_back',
        message: e.message,
        safetyCopy,
      })
      return
    }
    setError({ kind: 'server', text: `${op}失败：${e instanceof Error ? e.message : '未知错误'}` })
  }

  async function doBackup() {
    setBusy('backup')
    setNotice('')
    try {
      const name = await backupApi.downloadBackup()
      setNotice(`备份已下载到本机：${name}（服务器上同时保留了一份）`)
      await reload()
    } catch (e) {
      setError({ kind: 'server', text: `备份失败：${e instanceof Error ? e.message : ''}` })
    } finally {
      setBusy(null)
    }
  }

  /**
   * 落地一个新数据目录。
   *
   * 单独抽出来是因为两条路径（原生框选中的、手填的）最后必须走**同一段**校验与
   * 保存逻辑——分成两处的话，改天给手填那条补了可写性校验、选中那条没补，
   * 用户就会遇到"框里选的不行、手打的行"。
   */
  async function applyDataDir(next: string) {
    const trimmed = next.trim()
    if (!trimmed) {
      // 空路径不提交：留在手填态，让用户看到自己还没填。
      setManualDir(next)
      return
    }
    setBusy('datadir')
    setNotice('')
    setError(null)
    try {
      const d = await backupApi.setDataDir(trimmed)
      setDir(d)
      setManualDir(null)
      // 备份列表跟着换目录而失效：清空是对的，但必须重新拉一次——新目录里
      // 未必是空的（用户可能选中了一个已有备份的目录），清空不刷新的话界面
      // 会一直显示空列表，而本屏没有手动刷新按钮。
      await reload()
      // 迁移提示：旧目录里的备份**不会**跟着走。不说清楚的话，用户回到旧目录
      // 看见文件还在、或者在新目录找不到文件，都会以为备份丢了。
      setNotice(`数据目录已改为 ${d.dataDir}。原目录中已有的备份文件不会自动迁移，仍保留在原处。`)
    } catch (e) {
      setError({
        kind: e instanceof ApiError && e.isForbidden ? 'forbidden' : 'server',
        text: `更改数据目录失败：${e instanceof ApiError ? e.message : e instanceof Error ? e.message : '未知错误'}`,
      })
      if (e instanceof ApiError && e.isForbidden) {
        // 被本机闸门挡下：手填框也没用，一并收掉。
        setManualDir(null)
      }
    } finally {
      setBusy(null)
    }
  }

  async function doChangeDataDir() {
    setBusy('datadir')
    setNotice('')
    setError(null)
    try {
      const outcome = classifyDirPick(await backupApi.pickDataDir())
      if (outcome.kind === 'cancelled') {
        // 用户取消。取消不是错误，界面保持原状，也不必弹任何东西。
        return
      }
      if (outcome.kind === 'chosen') {
        await applyDataDir(outcome.dir)
      }
    } catch (e) {
      if (isDirPickUnavailable(e)) {
        // 没有窗口可弹（501）或弹框本身失败（500）：回退到手填，并说明原因。
        setManualDir(dir?.dataDir ?? '')
      } else if (e instanceof ApiError && e.isForbidden) {
        setError({ kind: 'forbidden', text: '更改数据目录只能在服务器所在机器上执行。请在主机上操作。' })
      } else {
        setError({
          kind: 'server',
          text: `选择数据目录失败：${e instanceof ApiError ? e.message : e instanceof Error ? e.message : '未知错误'}`,
        })
      }
    } finally {
      setBusy(null)
    }
  }

  async function doRestore() {
    if (!pickedFile || !wantToken || token !== wantToken || !pickedUsable) return
    setBusy('restore')
    setConfirmRestore(false)
    try {
      const res = await backupApi.restore(pickedFile.name)
      setRestored(res)
      setTokenInput(null)
      if (!res.requiresRestart) await reload()
    } catch (e) {
      if (e instanceof ApiError && e.status === 503) {
        setRestored({ statements: 0, requiresRestart: true })
        setRestartUncertain(true)
        return
      }
      // 副本文件名只有成功路径才有；失败时从服务端文案之外拿不到，
      // 所以这里不传 safetyCopy，让用户到备份列表里看时间最近的 pre_restore_*。
      onDestructiveError('整库恢复', e)
    } finally {
      setBusy(null)
    }
  }

  async function doClear() {
    setBusy('clear')
    setConfirmClear(false)
    try {
      const res = await backupApi.clear()
      setCleared(res)
      setDropText('')
      await reload()
    } catch (e) {
      onDestructiveError('清空业务数据', e)
    } finally {
      setBusy(null)
    }
  }

  async function doExportAudit() {
    setBusy('audit')
    setNotice('')
    setError(null)
    try {
      // 与全量导出一致：文件**已经落在数据目录里**，这里只报位置，
      // 不再顺手往浏览器的「下载」里塞一份 —— 那只会让人多出一个不知道是什么的文件，
      // 而且每次导出多出一份重复数据。
      setExported({
        title: '审计日志已导出',
        ...(await backupApi.exportAudit({ start: from, end: to })),
      })
    } catch (e) {
      setError({ kind: 'server', text: `导出失败：${e instanceof ApiError ? e.message : e instanceof Error ? e.message : ''}` })
    } finally {
      setBusy(null)
    }
  }

  /** 远程形态下文件在服务器上，"打开文件夹"用不了，只能把字节取回来。 */
  async function downloadAudit() {
    setBusy('auditdl')
    try {
      const { blob, filename } = await api.download(
        '/api/export/audit/download',
        { start: from, end: to },
        'audit.csv',
      )
      saveBlob(blob, filename)
    } catch (e) {
      setError({ kind: 'server', text: `下载失败：${e instanceof ApiError ? e.message : e instanceof Error ? e.message : ''}` })
    } finally {
      setBusy(null)
    }
  }

  async function doExportAll() {
    setBusy('all')
    setNotice('')
    setError(null)
    try {
      // 全量导出是**多份** CSV 落在数据目录里，不是单个文件。
      // 此前这里按"下载一个文件"处理，把服务端回的 JSON 存成了 all_data.csv ——
      // 打开是乱码（其实是含服务器路径的 JSON），用户既拿不到数据也没人说得清去哪找。
      setExported({ title: '全部数据已导出', ...(await backupApi.exportAll()), downloadable: true })
    } catch (e) {
      setError({ kind: 'server', text: `导出失败：${e instanceof ApiError ? e.message : e instanceof Error ? e.message : ''}` })
    } finally {
      setBusy(null)
    }
  }

  async function openExportDir(dir: string) {
    setBusy('openexport')
    try {
      await backupApi.openExportFolder(dir)
    } catch (e) {
      setError({
        kind: e instanceof ApiError && e.isForbidden ? 'forbidden' : 'server',
        text: `打开文件夹失败：${e instanceof ApiError ? e.message : e instanceof Error ? e.message : ''}`,
      })
    } finally {
      setBusy(null)
    }
  }

  if (error) return <ScreenError kind={error.kind} text={error.text} />

  const gateHint = !hostOperations
    ? '当前连接的是远程服务器，破坏性操作只能在服务器所在机器上执行。'
    : !canEdit
      ? '当前账号没有备份写权限。'
      : ''

  return (
    <>
      <div className="flex items-center gap-2.5">
        <h1 className="text-[15px] font-bold">备份导出</h1>
        {notice ? (
          <span className="text-[11.5px] text-[var(--color-primary)]">{notice}</span>
        ) : null}
      </div>

      {gateHint ? (
        <p className="rounded-[6px] border border-[var(--color-border)] bg-[var(--color-canvas)] px-2.5 py-1.5 text-[11.5px] text-[var(--color-fg-muted)]">
          {gateHint}
        </p>
      ) : null}

      {/* 数据目录 */}
      <Panel className="p-3">
        <div className="flex items-center justify-between gap-2">
          <SectionTitle>数据目录</SectionTitle>
          {/* 与 Fyne 版一致：有写权限才有这个按钮（那边 ui/backup.go:81）。
              这里还多一道本机闸门——SetDataDir 端点本来就要求本机形态，
              不跟着藏的话远程形态下会是个按了报 403 的按钮。 */}
          {destructiveReady ? (
            <Btn primary disabled={busy !== null} onClick={doChangeDataDir}>
              更改数据目录
            </Btn>
          ) : null}
        </div>
        <p className="mt-1 text-[11.5px] text-[var(--color-fg-muted)]">
          备份与导出文件的存放位置：
        </p>
        <p
          className="mt-1 rounded-[6px] border border-[var(--color-border)] bg-[var(--color-canvas)] px-2.5 py-1.5 text-[11.5px]"
          style={{ fontFamily: 'Consolas, monospace' }}
        >
          {dir?.dataDir ?? '—'}
        </p>

        {/* 降级手填：只有真的弹不出框时才出现，不是常驻控件 */}
        {manualDir !== null ? (
          <div className="mt-2 flex items-end gap-2">
            <div className="min-w-0 flex-1">
              <Field label="新数据目录">
                <input
                  value={manualDir}
                  onChange={(e) => setManualDir(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') void applyDataDir(manualDir)
                  }}
                  autoComplete="off"
                  spellCheck={false}
                  aria-label="新数据目录"
                  disabled={busy !== null}
                  placeholder="例如 D:\仓库数据"
                  className="h-[26px] w-full rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-2.5 text-[11.5px] outline-none focus:border-[var(--color-primary)] disabled:bg-[var(--color-canvas)]"
                />
              </Field>
            </div>
            <Btn primary disabled={busy !== null} onClick={() => void applyDataDir(manualDir)}>
              保存
            </Btn>
            <Btn disabled={busy !== null} onClick={() => setManualDir(null)}>
              取消
            </Btn>
          </div>
        ) : null}

        {/* 用户明确要求：改目录要有提示，但不做成确认门。
            改目录可逆、不影响已有数据，多一道确认只会让人多按一次。 */}
        {manualDir !== null ? (
          <p className="mt-1.5 text-[11px] text-[var(--color-fg-muted)]">
            更改后，新产生的备份与导出文件会写入该目录；原目录中已有的备份文件不会自动迁移。
          </p>
        ) : null}
      </Panel>

      {/* 一键备份 */}
      <Panel className="p-3">
        <SectionTitle>一键备份</SectionTitle>
        <p className="mt-1 text-[11.5px] text-[var(--color-fg-muted)]">
          备份当前数据库，文件保存在 {dir?.backupDir ?? '备份目录'} 的 manual 子目录，
          并同时下载到本机。手动备份<b>永不自动删除</b>。
        </p>
        <div className="mt-2">
          <Btn primary disabled={!canEdit || busy !== null} onClick={doBackup}>
            {busy === 'backup' ? '备份中…' : '一键备份'}
          </Btn>
        </div>
      </Panel>

      {/* 自动定期备份 */}
      <Panel className="p-3">
        <SectionTitle>自动定期备份</SectionTitle>
        <p className="mt-1 text-[11.5px] text-[var(--color-fg-muted)]">
          按设定的间隔自动生成备份，存在 auto 子目录，滚动保留最近 {auto?.keep ?? 20} 份，
          与手动备份分开、互不影响。
        </p>
        <div className="mt-2 flex items-end gap-2">
          <div className="w-[160px]">
            <Field label="备份间隔">
              <select
                value={auto?.days ?? 0}
                // 改间隔会写配置并立刻触发一次备份，属于本机操作
                disabled={!destructiveReady || busy !== null || !!auto?.reason}
                onChange={(e) => void setAutoDays(Number(e.target.value))}
                aria-label="自动备份间隔"
                className="h-[26px] w-full rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-2 text-[11.5px] outline-none focus:border-[var(--color-primary)] disabled:bg-[var(--color-canvas)]"
              >
                {AUTO_BACKUP_DAY_OPTIONS.map((d) => (
                  <option key={d} value={d}>
                    {autoBackupDayLabel(d)}
                  </option>
                ))}
              </select>
            </Field>
          </div>
          {busy === 'autoschedule' ? <span className="pb-1 text-[11px]">保存中…</span> : null}
        </div>
        {auto?.reason ? (
          <p className="mt-1.5 text-[11px] text-[#8A6100]">当前不可用：{auto.reason}</p>
        ) : null}
        {auto?.enabled ? (
          <div className="mt-1.5 text-[11px] text-[var(--color-fg-muted)]">
            {auto.last ? (
              <>
                最近一次：{auto.last}
                {auto.nextDue ? `　下次到期：${auto.nextDue}` : null}　现有 {auto.count} 份
              </>
            ) : (
              <>尚未生成过自动备份</>
            )}
          </div>
        ) : null}
        {auto?.enabled ? (
          <p className="mt-1 text-[11px] text-[var(--color-fg-muted)]">
            说明：本软件没有后台服务，间隔到期时会在你<b>下一次打开软件</b>时补上一份；
            一直不开软件就不会备份。
          </p>
        ) : null}
      </Panel>

      {/* 整库恢复 */}
      <Panel className="p-3">
        <SectionTitle>整库恢复（覆盖现有数据）</SectionTitle>
        <div className="mt-2 grid grid-cols-2 gap-2.5">
          <Field label="备份文件">
            <select
              value={picked}
              onChange={(e) => setPicked(e.target.value)}
              disabled={!destructiveReady || busy !== null}
              className="h-[26px] w-full rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-2 text-[11.5px] outline-none focus:border-[var(--color-primary)]"
            >
              <option value="">请选择…</option>
              {/* 二级级联：按分类分组（同 BOM 的父子层级）。
                  光看文件名分不出好坏——同一时刻的 .sql / .upgraded.sql / .final.sql
                  三个文件回滚结果不同，而名字几乎一样。 */}
              {fileGroups.map((g) => (
                <optgroup key={g.category} label={g.label}>
                  {g.files.map((f) => (
                    <option key={f.name} value={f.name}>
                      {f.name}（{f.modified}）{fileBadge(f)}
                    </option>
                  ))}
                </optgroup>
              ))}
            </select>
          </Field>

          <Field label="确认口令">
            <input
              value={token}
              onChange={(e) => setTokenInput({ file: picked, text: e.target.value })}
              // 不可粘贴、不可自动填充：这道门的作用是让人停下来读一遍后果，
              // 粘贴或预填都会让它退化成勾选框。
              onPaste={(e) => e.preventDefault()}
              onDrop={(e) => e.preventDefault()}
              autoComplete="off"
              spellCheck={false}
              // 稳定的可访问名：placeholder 会随状态变（「先选择一个备份文件」/「在此
              // 输入确认口令」），用它定位会在两处都叫「确认口令」时选错输入框。
              aria-label="恢复确认口令"
              disabled={!wantToken || !pickedUsable || busy !== null}
              placeholder={
                !pickedUsable
                  ? '这份备份不可用'
                  : wantToken
                    ? '在此输入确认口令'
                    : '先选择一个备份文件'
              }
              className="h-[26px] w-full rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-2.5 text-[11.5px] outline-none focus:border-[var(--color-primary)] disabled:bg-[var(--color-canvas)]"
            />
          </Field>
        </div>

        {/* G1 的后半：选了文件但导不出令牌 → 直接拒绝，不给「下一步」 */}
        {pickedFile && !pickedUsable ? (
          <p className="mt-2 rounded-[6px] border border-[#E5A50A] bg-[#FFFBEB] px-2.5 py-1.5 text-[11.5px] text-[#8A6100]">
            这份备份不能作为恢复来源：{pickedFile.unusableReason ?? '文件格式无法识别'}。
            <div className="mt-1">
              若它是用 PowerShell「另存为」或重定向生成的，请用 UTF-8 重新导出一份再恢复。
            </div>
          </p>
        ) : null}
        {pickedFile && pickedUsable && !wantToken ? (
          <p className="mt-2 rounded-[6px] border border-[#E5A50A] bg-[#FFFBEB] px-2.5 py-1.5 text-[11.5px] text-[#8A6100]">
            无法从文件名「{pickedFile.name}」识别备份时间戳，为避免恢复错文件已拒绝恢复。
            请选择形如
            <span style={{ fontFamily: 'Consolas, monospace' }}> backup_20260925_101530.sql </span>
            的文件；本应用生成的备份、以及导出后自行改名的副本（结尾加 .final / .upgraded 等）都可以用。
          </p>
        ) : null}

        {/* 覆盖度预检：恢复是「先清空、再回灌」，缺表 = 那张表的数据回不来 */}
        {lostTables.length > 0 ? (
          <p className="mt-2 rounded-[6px] border border-[#E5A50A] bg-[#FFFBEB] px-2.5 py-1.5 text-[11.5px] text-[#8A6100]">
            这份备份早于部分表引入，恢复后下列数据会被清空且**无法从该备份回填**：
            <b>{lostTables.join('、')}</b>。仍可继续恢复，操作前副本可用于退回。
          </p>
        ) : null}
        {coverageUnknown ? (
          <p className="mt-2 rounded-[6px] border border-[var(--color-border)] bg-[var(--color-canvas)] px-2.5 py-1.5 text-[11.5px] text-[var(--color-fg-muted)]">
            这份备份较大，只校验了开头部分，**无法确认**它是否覆盖全部当前数据表。
          </p>
        ) : null}
        {wantToken && pickedUsable ? (
          <p className="mt-2 text-[11.5px] text-[var(--color-fg-muted)]">
            请在输入框中准确输入（区分大小写）：
            <b style={{ fontFamily: 'Consolas, monospace' }}>{wantToken}</b>
          </p>
        ) : null}

        <div className="mt-2">
          <Btn
            danger
            disabled={
              !destructiveReady || !wantToken || !pickedUsable || token !== wantToken || busy !== null
            }
            onClick={() => setConfirmRestore(true)}
          >
            {busy === 'restore' ? '恢复中…' : '整库恢复'}
          </Btn>
        </div>
      </Panel>

      {/* 清空业务数据 */}
      <Panel className="p-3">
        <SectionTitle>清空业务数据（危险操作）</SectionTitle>
        <p className="mt-1 text-[11.5px] text-[var(--color-fg-muted)]">
          将清空产品/零件/BOM/批次/追溯/操作记录；账号、迁移记录与数据库身份会保留。
          操作前会自动生成一份可回退的副本，因此本次清空是可恢复的。
        </p>
        <Field label={`输入 ${CLEAR_CONFIRM_TOKEN} 确认`}>
          <input
            value={dropText}
            onChange={(e) => setDropText(e.target.value)}
            onPaste={(e) => e.preventDefault()}
            autoComplete="off"
            spellCheck={false}
            aria-label="清空确认口令"
            disabled={!destructiveReady || busy !== null}
            placeholder="在此输入确认口令"
            className="mt-1 h-[26px] w-[180px] rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-2.5 text-[11.5px] outline-none focus:border-[var(--color-primary)] disabled:bg-[var(--color-canvas)]"
          />
        </Field>
        <div className="mt-2">
          <Btn
            danger
            disabled={!destructiveReady || dropText !== CLEAR_CONFIRM_TOKEN || busy !== null}
            onClick={() => setConfirmClear(true)}
          >
            {busy === 'clear' ? '清空中…' : '清空业务数据（危险操作）'}
          </Btn>
        </div>
      </Panel>

      {/* 审计日志导出 */}
      <Panel className="p-3">
        <SectionTitle>审计日志导出</SectionTitle>
        <div className="mt-2 flex flex-wrap items-end gap-2.5">
          <Field label="起始日期">
            <input
              type="date"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
              className="h-[26px] rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-2 text-[11.5px] outline-none focus:border-[var(--color-primary)]"
            />
          </Field>
          <Field label="截止日期">
            <input
              type="date"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              className="h-[26px] rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-2 text-[11.5px] outline-none focus:border-[var(--color-primary)]"
            />
          </Field>
          <Btn disabled={busy !== null} onClick={doExportAudit}>
            {busy === 'audit' ? '导出中…' : '导出审计日志 CSV'}
          </Btn>
        </div>
      </Panel>

      {/* 全量导出 */}
      <Panel className="p-3">
        <SectionTitle>全部数据导出</SectionTitle>
        <p className="mt-1 text-[11.5px] text-[var(--color-fg-muted)]">
          把产品、零件、BOM、批次、追溯与操作记录各导出一份 CSV，保存到{' '}
          <span style={{ fontFamily: 'Consolas, monospace' }}>
            {dir?.dataDir ? `${dir.dataDir}\\导出\\all_data` : '数据目录'}
          </span>
          下的时间戳文件夹里（每张表一个文件，可直接用 Excel 打开）。
        </p>
        <div className="mt-2">
          <Btn disabled={busy !== null || !canEdit} onClick={doExportAll}>
            {busy === 'all' ? '导出中…' : '导出全部数据 CSV'}
          </Btn>
        </div>
      </Panel>

      {/* 导出完成：告诉用户文件**在哪**，而不是丢一个没用的下载。
          审计日志与全量导出共用这一个弹窗 —— 两件事的形态是一样的，
          分成两个弹窗早晚会漂成两套措辞。 */}
      {exported ? (
        <Modal title={exported.title} onClose={() => setExported(null)} width={560}>
          <p className="text-[11.5px]">
            共 {exported.files.length} 个文件，保存到：
          </p>
          <p
            className="mt-1 break-all rounded-[6px] border border-[var(--color-border)] bg-[var(--color-canvas)] px-2 py-1.5 text-[11.5px]"
            style={{ fontFamily: 'Consolas, monospace' }}
          >
            {exported.dir}
          </p>
          <div className="mt-2 max-h-[160px] overflow-auto rounded-[6px] border border-[var(--color-border)] bg-[var(--color-canvas)] px-2 py-1.5">
            {exported.files.map((f) => (
              <div key={f} className="truncate text-[11px]" style={{ fontFamily: 'Consolas, monospace' }}>
                {f}
              </div>
            ))}
          </div>
          <p className="mt-2 text-[11px] text-[var(--color-fg-muted)]">
            {destructiveReady
              ? '文件不会自动下载到浏览器——它们已经在上面这个文件夹里了。'
              : '当前连接的是远程服务器，文件保存在服务器上，本机打不开这个文件夹。'}
          </p>
          <div className="mt-3 flex justify-end gap-2">
            <Btn onClick={() => setExported(null)}>关闭</Btn>
            {destructiveReady ? (
              <Btn primary disabled={busy !== null} onClick={() => void openExportDir(exported.dir)}>
                {busy === 'openexport' ? '打开中…' : '打开文件夹'}
              </Btn>
            ) : null}
            {!destructiveReady && exported.downloadable && REMOTE_EXPORT_DOWNLOAD_AVAILABLE ? (
              <Btn primary disabled={busy !== null} onClick={() => void downloadAudit()}>
                {busy === 'auditdl' ? '下载中…' : '下载 CSV 到本机'}
              </Btn>
            ) : null}
          </div>
        </Modal>
      ) : null}

      {/* G3：列明后果的确认框 */}
      {confirmRestore ? (
        <ConfirmDialog
          title="最终警告：整库恢复"
          danger
          confirmLabel="整库恢复"
          busy={busy === 'restore'}
          message={
            <>
              即将把数据库整库恢复到备份时刻：
              <div style={{ fontFamily: 'Consolas, monospace' }}>{pickedFile?.name}</div>
              <div className="mt-1.5">请确认以下后果：</div>
              <ol className="mt-1 list-decimal pl-4">
                <li>当前业务数据会被完全覆盖（不是追加导入）</li>
                <li>操作前会自动生成一份完整副本，可用整库恢复退回</li>
                <li>SQLite 快照恢复后必须退出并重新打开程序，当前会话不能继续操作</li>
                <li>账号、迁移记录与数据库身份不会被覆盖，账号保持现状</li>
                <li>
                  回灌在单个事务内完成；如提交结果不明或提交后核验失败，须先核对数据再重试
                </li>
              </ol>
              {/* 覆盖度预检：把「这张表的数据回不来」摆进后果清单里。
                  不硬拦——回滚到某个历史时刻是正当需求，替用户决定不合适；
                  但让他在按下确认之前就知道。 */}
              {lostTables.length > 0 ? (
                <div className="mt-2 rounded-[6px] border border-[#E5A50A] bg-[#FFFBEB] px-2.5 py-1.5 text-[11.5px] text-[#8A6100]">
                  <b>这份备份回填不了下列数据表：</b>
                  {lostTables.join('、')}
                  <div className="mt-1">
                    它们会被清空，且无法从该备份恢复——只能用操作前自动生成的副本找回。
                    这说明该备份早于这些表引入。
                  </div>
                </div>
              ) : null}
              {coverageUnknown ? (
                <div className="mt-2 rounded-[6px] border border-[var(--color-border)] bg-[var(--color-canvas)] px-2.5 py-1.5 text-[11.5px] text-[var(--color-fg-muted)]">
                  这份备份较大，只校验了开头部分，无法确认它是否覆盖全部当前数据表。
                </div>
              ) : null}
            </>
          }
          onConfirm={doRestore}
          onCancel={() => setConfirmRestore(false)}
        />
      ) : null}

      {confirmClear ? (
        <ConfirmDialog
          title="最终警告"
          danger
          confirmLabel="清空业务数据"
          busy={busy === 'clear'}
          message={
            <>
              确定要清空全部业务数据吗？
              <ul className="mt-1.5 list-disc pl-4">
                <li>产品/零件/BOM/批次/追溯/操作记录会被清空</li>
                <li>账号、迁移记录与数据库身份会保留</li>
                <li>操作前会自动生成一份完整副本，可用整库恢复退回</li>
              </ul>
            </>
          }
          onConfirm={doClear}
          onCancel={() => setConfirmClear(false)}
        />
      ) : null}

      {/* 成功结果 */}
      {restored?.requiresRestart ? <RestoreRestart uncertain={restartUncertain} /> : restored ? (
        <Modal title="恢复完成" onClose={() => setRestored(null)} width={520}>
          <p className="text-[12.5px]">整库恢复完成，共执行 {restored.statements} 条 INSERT。</p>
          {restored.tableCounts && Object.keys(restored.tableCounts).length > 0 ? (
            <pre className="mt-2 max-h-[220px] overflow-auto rounded-[6px] border border-[var(--color-border)] bg-[var(--color-canvas)] p-2 text-[11px]">
              {Object.keys(restored.tableCounts)
                .sort((a, b) => a.localeCompare(b))
                .map((t) => `${t}=${restored.tableCounts![t]}`)
                .join('\n')}
            </pre>
          ) : null}
          {restored.preRestore ? (
            <p className="mt-2 text-[12px] text-[var(--color-fg-muted)]">
              可回退副本：
              <span style={{ fontFamily: 'Consolas, monospace' }}>{restored.preRestore}</span>
              。如需退回，用该文件再执行一次整库恢复即可。
            </p>
          ) : null}
        </Modal>
      ) : null}

      {cleared ? (
        <Modal title="清空完成" onClose={() => setCleared(null)} width={520}>
          <p className="text-[12.5px]">
            业务数据已清空；账号、迁移记录与数据库身份已保留。
          </p>
          {cleared.preClear ? (
            <p className="mt-2 text-[12px] text-[var(--color-fg-muted)]">
              可回退副本：
              <span style={{ fontFamily: 'Consolas, monospace' }}>{cleared.preClear}</span>
            </p>
          ) : null}
          {cleared.cleared?.length ? (
            <p className="mt-2 text-[12px] text-[var(--color-fg-muted)]">
              已清空：{cleared.cleared.join('、')}
            </p>
          ) : null}
        </Modal>
      ) : null}

      {/* 三分类结局 */}
      {outcome ? <OutcomeDialog view={outcome} onClose={() => setOutcome(null)} /> : null}
    </>
  )
}

function SectionTitle({ children }: { children: ReactNode }) {
  return <span className="text-[13px] font-semibold">{children}</span>
}
