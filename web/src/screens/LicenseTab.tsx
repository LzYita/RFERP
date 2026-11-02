import { useEffect, useRef, useState } from 'react'

import { Btn, Panel } from '../components/ui'
import { lan, lanErrorText, type Device, type LicenseStatus } from '../lib/lan'

/**
 * 授权与设备（FR-37）。
 *
 * 一屏两段：上面是授权状态与导入，下面是设备列表与撤销/删除。
 * 放一屏的理由是它们回答同一个问题——"还能不能再加一台设备"；
 * 分两页会让管理员在两页之间来回核对额度与已用数。
 */
export function LicenseTab({
  initial,
  onChanged,
}: {
  initial: LicenseStatus
  onChanged: (s: LicenseStatus) => void
}) {
  const [license, setLicense] = useState<LicenseStatus>(initial)
  const [devices, setDevices] = useState<Device[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')
  // 二次确认用页内状态，而不是 window.confirm：WebView2 里原生确认框
  // 的行为不受本界面样式控制，且同步阻塞会让按钮看起来像卡住。
  const [pending, setPending] = useState<{ d: Device; remove: boolean } | null>(null)
  const fileRef = useRef<HTMLInputElement>(null)

  const reload = async () => {
    try {
      const d = await lan.devices()
      setDevices(d.devices)
      // 客户端形态（代理到主机）不返回 runtime，保留登录时拿到的那份。
      if (d.runtime) setLicense((prev) => ({ ...prev, runtime: d.runtime! }))
    } catch (e) {
      setErr(lanErrorText(e))
    }
  }

  useEffect(() => {
    void reload()
    // 只在挂载时拉一次；后续显式刷新，避免与用户操作互相覆盖。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const importFile = async (f: File) => {
    setBusy(true)
    setMsg('')
    setErr('')
    try {
      const text = await f.text()
      const st = await lan.importLicense(text)
      setLicense(st)
      onChanged(st)
      setMsg(`已导入：${st.runtime.customer ?? ''}（客户端数 ${st.runtime.max_clients}）`)
      await reload()
    } catch (e) {
      setErr(lanErrorText(e))
    } finally {
      setBusy(false)
      if (fileRef.current) fileRef.current.value = ''
    }
  }

  const act = async (d: Device, remove: boolean) => {
    const verb = remove ? '删除' : '撤销'
    const hint = remove
      ? '删除会释放一个客户端名额，该设备下次登录会重新登记。'
      : '撤销后该设备立即退出登录，且不再能登录；名额不会释放。'
    const label = d.name || d.device_id.slice(0, 8)
    if (!confirm(`${verb}设备「${label}」？\n\n${hint}`)) return
    setBusy(true)
    setMsg('')
    setErr('')
    try {
      if (remove) await lan.remove(d.device_id)
      else await lan.revoke(d.device_id)
      setMsg(`已${verb}：${label}`)
      await reload()
    } catch (e) {
      setErr(lanErrorText(e))
    } finally {
      setBusy(false)
    }
  }

  const rt = license.runtime
  // 授权文件里的日期是 RFC3339（2027-10-29T23:59:59+08:00），直接显示对用户
  // 没有意义；这里只留日期部分，与设备表的"最近在线"格式保持一致。
  const day = (v?: string) => (v && /^\d{4}-\d{2}-\d{2}/.test(v) ? v.slice(0, 10) : v || '—')

  return (
    <Panel className="p-3">
      <div className="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1.5 border-b border-[var(--color-border)] pb-2.5">
        <span className="text-[13px] font-bold">授权</span>
        <span className="text-[11.5px] text-[var(--color-fg-muted)]">
          {rt.licensed
            ? `${rt.customer ?? '未命名客户'} · ${rt.kind} · 客户端 ${rt.used_clients}/${rt.max_clients}`
            : '尚未导入授权（当前仅主机可登录）'}
        </span>
        {rt.read_only ? (
          <span className="rounded-[4px] bg-[var(--color-canvas)] px-1.5 py-0.5 text-[11px] text-[var(--color-danger)]">
            只读模式
          </span>
        ) : null}
        <span className="ml-auto flex items-center gap-2">
          <input
            ref={fileRef}
            type="file"
            accept=".rferplic,application/json"
            className="hidden"
            onChange={(e) => {
              const f = e.target.files?.[0]
              if (f) void importFile(f)
            }}
          />
          <Btn primary onClick={() => fileRef.current?.click()} disabled={busy}>
            {busy ? '处理中…' : '导入授权'}
          </Btn>
          <Btn onClick={() => void reload()} disabled={busy}>
            刷新
          </Btn>
        </span>
      </div>

      {rt.policy_message ? (
        <div className="mb-2 text-[11.5px] text-[var(--color-fg-muted)]">{rt.policy_message}</div>
      ) : null}
      {msg ? (
        <div role="status" className="mb-2 text-[11.5px] text-[var(--color-primary)]">
          {msg}
        </div>
      ) : null}
      {err ? (
        <div role="alert" className="mb-2 text-[11.5px] text-[var(--color-danger)]">
          {err}
        </div>
      ) : null}

      <div className="mb-1 flex flex-wrap gap-x-4 text-[11.5px] text-[var(--color-fg-muted)]">
        <span>类型：{rt.kind || '—'}</span>
        <span>支持到期：{day(rt.support_until)}</span>
        <span>授权到期：{rt.expires_at ? day(rt.expires_at) : '永久'}</span>
        {license.imported_at ? <span>导入于 {license.imported_at}</span> : null}
      </div>

      {pending ? (
        <div className="mb-2 rounded-[6px] border border-[var(--color-border)] bg-[var(--color-canvas)] px-2.5 py-2 text-[11.5px]">
          <div className="mb-1">
            {pending.remove ? '删除' : '撤销'}设备「{pending.d.name || pending.d.device_id.slice(0, 8)}」？
            <span className="ml-1 text-[var(--color-fg-muted)]">
              {pending.remove
                ? '删除会释放一个客户端名额，该设备下次登录会重新登记。'
                : '撤销后该设备立即退出登录且不再能登录；名额不会释放。'}
            </span>
          </div>
          <div className="flex justify-end gap-2">
            <Btn onClick={() => setPending(null)}>取消</Btn>
            <Btn primary onClick={() => void act(pending.d, pending.remove)}>
              确认{pending.remove ? '删除' : '撤销'}
            </Btn>
          </div>
        </div>
      ) : null}

      <div className="mt-3 border-t border-[var(--color-border)] pt-2.5">
        <div className="mb-1.5 text-[13px] font-bold">设备</div>
        {!devices ? (
          <div className="text-[11.5px] text-[var(--color-fg-muted)]">正在加载设备…</div>
        ) : devices.length === 0 ? (
          <div className="text-[11.5px] text-[var(--color-fg-muted)]">还没有设备登记。</div>
        ) : (
          <table className="w-full text-[11.5px]">
            <thead>
              <tr className="text-left text-[var(--color-fg-muted)]">
                <th className="py-1 font-normal">名称</th>
                <th className="py-1 font-normal">类型</th>
                <th className="py-1 font-normal">状态</th>
                <th className="py-1 font-normal">最近在线</th>
                <th className="py-1 font-normal">操作</th>
              </tr>
            </thead>
            <tbody>
              {devices.map((d) => (
                <tr key={d.device_id} className="border-t border-[var(--color-border)]">
                  <td className="py-1.5">{d.name || d.device_id.slice(0, 8)}</td>
                  <td className="py-1.5">{d.kind === 'host' ? '主机' : '客户端'}</td>
                  <td className="py-1.5">{d.status === 'active' ? '正常' : '已撤销'}</td>
                  <td className="py-1.5">{d.last_seen_at?.replace('T', ' ').slice(0, 16) ?? '—'}</td>
                  <td className="py-1.5">
                    {d.kind === 'host' ? (
                      <span className="text-[var(--color-fg-subtle)]">不可操作</span>
                    ) : (
                      <span className="flex gap-2">
                        {d.status === 'active' ? (
                          <button
                            type="button"
                            className="text-[var(--color-danger)] underline underline-offset-2"
                            onClick={() => setPending({ d, remove: false })}
                            disabled={busy}
                          >
                            撤销
                          </button>
                        ) : null}
                        <button
                          type="button"
                          className="text-[var(--color-fg-muted)] underline underline-offset-2"
                          onClick={() => setPending({ d, remove: true })}
                          disabled={busy}
                        >
                          删除
                        </button>
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </Panel>
  )
}
