import { useState } from 'react'
import { api } from '../lib/api'
import { Btn } from '../components/ui'
import { Modal } from '../components/form'

export function RestoreRestart({ uncertain = false }: { uncertain?: boolean }) {
  const [error, setError] = useState('')
  const [exiting, setExiting] = useState(false)
  async function exit() {
    setExiting(true)
    setError('')
    try {
      await api.post('/api/restore/exit', {})
    } catch {
      setError('无法自动退出，请关闭程序窗口后重新打开。')
      setExiting(false)
    }
  }
  return <Modal title="需要重新启动" dismissible={false} onClose={() => {}}
    footer={<Btn onClick={() => void exit()} disabled={exiting}>{exiting ? '正在退出…' : '退出程序'}</Btn>}>
    <p>{uncertain ? '恢复结果未确认，请重新打开后核对数据，勿直接重试。' : 'SQLite 快照已替换，当前程序不能继续操作。'}</p>
    <p>请退出并重新打开程序，使用恢复后的账号重新登录。</p>
    {error ? <p role="alert">{error}</p> : null}
  </Modal>
}
