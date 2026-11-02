import { useEffect, useState } from 'react'

import { Btn } from '../components/ui'
import { lan, lanErrorText } from '../lib/lan'

/**
 * 登录页右下角的「主机地址」。
 *
 * 只挂在客户端模式：单机桌面与被访问的服务器都没有"连哪台主机"这个问题，
 * 无条件显示只会让人以为软件要配置网络。
 *
 * 为什么放在登录页而不是设置页：主机 IP 变了（路由器重置、换机）时用户连
 * 登录都进不去，设置页是一个到不了的地方。这个入口必须在登录之前可用。
 */
export function ServerAddressBar() {
  const [serverUrl, setServerUrl] = useState('')
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let alive = true
    lan
      .status()
      .then((st) => {
        if (!alive || st.deployment !== 'client') return
        setServerUrl(st.serverUrl ?? '')
        setDraft(st.serverUrl ?? '')
      })
      .catch(() => {
        /* 连不上本机内嵌服务时静默：那不是用户能处理的事 */
      })
    return () => {
      alive = false
    }
  }, [])

  if (!serverUrl && !editing) return null

  const save = async () => {
    setBusy(true)
    setErr('')
    try {
      const r = await lan.connect(draft)
      setServerUrl(r.serverUrl)
      setEditing(false)
    } catch (e) {
      setErr(lanErrorText(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed right-3 bottom-3 z-20 max-w-[320px] rounded-[8px] border border-[var(--color-border)] bg-[var(--color-surface)] p-2.5 text-[11.5px] shadow-sm">
      {editing ? (
        <div className="flex flex-col gap-1.5">
          <span className="text-[var(--color-fg-muted)]">主机地址</span>
          <input
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder="192.168.1.10 或 http://192.168.1.10:8080"
            className="h-[26px] w-[280px] rounded-[6px] border border-[var(--color-border)] px-2 text-[12px] outline-none focus:border-[var(--color-primary)]"
          />
          {err ? <span className="text-[var(--color-danger)]">{err}</span> : null}
          <div className="flex justify-end gap-1.5">
            <Btn onClick={() => { setEditing(false); setErr('') }} disabled={busy}>
              取消
            </Btn>
            <Btn primary onClick={save} disabled={busy || draft.trim() === ''}>
              {busy ? '连接中…' : '测试并保存'}
            </Btn>
          </div>
        </div>
      ) : (
        <div className="flex items-center gap-2">
          <span className="text-[var(--color-fg-muted)]">主机</span>
          <span className="font-medium">{serverUrl}</span>
          <button
            type="button"
            onClick={() => {
              setDraft(serverUrl)
              setEditing(true)
            }}
            className="ml-auto text-[var(--color-primary)] underline underline-offset-2"
          >
            更改
          </button>
        </div>
      )}
    </div>
  )
}
