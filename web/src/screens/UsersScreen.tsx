import { useEffect, useState } from 'react'
import { Badge, Btn, ScreenError, classifyLoadError, shortTime } from '../components/ui'
import {
  ConfirmDialog,
  FormError,
  FormRow,
  Modal,
  PasswordInput,
  Select,
  TextInput,
} from '../components/form'
import { usePermissions } from '../lib/api'
import { canWrite } from '../lib/permissions'
import { ROLES, passwordTooShort, roleLabel, users, type User } from '../lib/users'

/**
 * 用户管理。
 *
 * 权限：`ModuleUsers` 只给 admin 写，其他三���（仓管/生产/只读）连读都没有，
 * 所以侧栏入口对它们直接不可见，写按钮也就无从谈起。
 *
 * 三处**刻意比 Fyne 更严**，都是补它的漏洞：
 *
 * 1. **不能停用/删除自己** —— Fyne 有这两个守卫（ui/users.go:217、:270），
 *    这里保留。
 * 2. **不能降级自己** —— Fyne 没有这个守卫，而后端允许：把���自己的角色改掉后，
 *    接口仍返回 200（admin.go:75 写在撤销会话**之后**），紧接着的请求 401，
 *    用户会看到一个来路不明的「登录状态已失效」弹窗。这里直接拦住。
 * 3. **降级别人之前先说清会被强制下线** —— 后端在角色变化时会撤销目标用户的
 *    全部会话与「记住登录」凭证（admin.go:67-74）。这是对的语义，但界面上
 *    不说，用户会以为改坏了别人的账号。
 *
 * 停用或删除后的会话与记住登录撤销由后端处理，不依赖页面主动登出。
 */

type Draft = {
  username: string
  displayName: string
  password: string
  confirm: string
  role: string
  status: number
}

const emptyDraft = (): Draft => ({
  username: '',
  displayName: '',
  password: '',
  confirm: '',
  // Fyne 新增表单的默认角色是 Roles() 的最后一位（只读）。这里显式写出来，
  // 不依赖数组下标——加一个角色就会静默改掉默认值。
  role: 'viewer',
  status: 1,
})

export function UsersScreen({ meId }: { meId: number }) {
  const [rows, setRows] = useState<User[] | null>(null)
  const [error, setError] = useState<{ kind: 'forbidden' | 'server'; text: string } | null>(null)
  const [sel, setSel] = useState<number | null>(null)
  const [notice, setNotice] = useState('')

  const [formOpen, setFormOpen] = useState(false)
  const [editing, setEditing] = useState<User | null>(null)
  const [draft, setDraft] = useState<Draft>(emptyDraft())
  const [formErr, setFormErr] = useState('')
  const [busy, setBusy] = useState(false)

  const [resetting, setResetting] = useState<User | null>(null)
  const [newPw, setNewPw] = useState('')
  const [newPw2, setNewPw2] = useState('')
  const [deleting, setDeleting] = useState<User | null>(null)
  const [roleChange, setRoleChange] = useState<User | null>(null)

  const { modules } = usePermissions()
  const canEdit = canWrite(modules, 'users')

  const selected = rows?.find((u) => u.id === sel) ?? null

  useEffect(() => {
    let alive = true
    users
      .list()
      .then((r) => {
        if (alive) {
          setRows(r)
          setError(null)
        }
      })
      .catch((e) => {
        if (alive) setError(classifyLoadError(e, '用户'))
      })
    return () => {
      alive = false
    }
  }, [])

  async function reload() {
    setRows(await users.list())
  }

  function needSelection(): boolean {
    if (selected) return true
    setNotice('请先选择一行用户')
    return false
  }

  /* ---------- 新增 / 编辑 ---------- */

  function openCreate() {
    setEditing(null)
    setDraft(emptyDraft())
    setFormErr('')
    setFormOpen(true)
  }

  function openEdit(u: User) {
    setEditing(u)
    setDraft({
      username: u.username,
      displayName: u.display_name ?? '',
      password: '',
      confirm: '',
      role: u.role,
      status: u.status,
    })
    setFormErr('')
    setFormOpen(true)
  }

  /**
   * 保存前的守卫。都在**提交前**拦，而不是等后端返回一句
   * 「至少需保留一个启用状态的管理员」——那是内部措辞。
   */
  function validate(): string {
    if (!editing && !draft.username.trim()) return '请输入用户名'
    if (!editing) {
      if (draft.password !== draft.confirm) return '两次输入的密码不一致'
      if (passwordTooShort(draft.password)) return '密码至少 4 位'
    }
    if (editing) {
      if (editing.id === meId && draft.status === 0) return '不能停用当前登录账号'
      if (editing.id === meId && draft.role !== editing.role) {
        return '不能降级当前登录账号，否则你会立刻被强制下线'
      }
    }
    return ''
  }

  async function submit() {
    const msg = validate()
    if (msg) {
      setFormErr(msg)
      return
    }
    setBusy(true)
    setFormErr('')
    try {
      if (editing) {
        // 四个字段全发 —— PUT 是全量替换，漏一个就静默清空或停用
        await users.update({
          id: editing.id,
          displayName: draft.displayName.trim(),
          role: draft.role,
          status: draft.status,
        })
        setFormOpen(false)
        await reload()
      } else {
        await users.create({
          username: draft.username.trim(),
          password: draft.password,
          displayName: draft.displayName.trim(),
          role: draft.role,
        })
        setFormOpen(false)
        await reload()
      }
    } catch (e) {
      setFormErr(mapUserError(e, editing !== null))
    } finally {
      setBusy(false)
    }
  }

  /* ---------- 改角色（带确认）---------- */

  async function confirmRoleChange() {
    if (!roleChange) return
    setBusy(true)
    setFormErr('')
    try {
      await users.update({
        id: roleChange.id,
        displayName: draft.displayName.trim(),
        role: draft.role,
        status: draft.status,
      })
      setRoleChange(null)
      setFormOpen(false)
      await reload()
    } catch (e) {
      setFormErr(mapUserError(e, true))
    } finally {
      setBusy(false)
    }
  }

  /** 编辑里改了别人的角色 → 先确认，不要直接提交。 */
  function save() {
    if (editing && editing.id !== meId && draft.role !== editing.role) {
      setRoleChange(editing)
      return
    }
    void submit()
  }

  /* ---------- 重置密码 ---------- */

  async function doReset() {
    if (!resetting) return
    if (newPw !== newPw2) {
      setFormErr('两次输入的密码不一致')
      return
    }
    if (passwordTooShort(newPw)) {
      setFormErr('密码至少 4 位')
      return
    }
    setBusy(true)
    setFormErr('')
    try {
      await users.resetPassword(resetting.id, newPw)
      setResetting(null)
      setNewPw('')
      setNewPw2('')
      setNotice(`密码已重置：${resetting.username}`)
    } catch (e) {
      setFormErr(mapUserError(e, false))
    } finally {
      setBusy(false)
    }
  }

  /* ---------- 删除 ---------- */

  async function doDelete() {
    if (!deleting) return
    if (deleting.id === meId) {
      setNotice('不能删除当前登录账号')
      setDeleting(null)
      return
    }
    setBusy(true)
    try {
      await users.remove(deleting.id)
      setSelected(null)
      setSel(null)
      setDeleting(null)
      await reload()
    } catch (e) {
      setNotice(mapUserError(e, false))
      setDeleting(null)
    } finally {
      setBusy(false)
    }
  }

  function setSelected(_: number | null) {
    setSel(null)
  }

  if (error) return <ScreenError kind={error.kind} text={error.text} />

  return (
    <>
      <div className="flex items-center gap-2.5">
        <h1 className="text-[15px] font-bold">用户管理</h1>
        <div className="ml-auto flex gap-2">
          {canEdit ? (
            <Btn primary onClick={openCreate}>
              新增用户
            </Btn>
          ) : null}
        </div>
      </div>

      {notice && (
        <div
          role="status"
          className="rounded-[8px] border border-[var(--color-warn)]/30 bg-[var(--color-warn-bg)] px-3 py-2 text-[12px] text-[var(--color-warn)]"
        >
          {notice}
        </div>
      )}

      <section className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-[8px] border border-[var(--color-border)] bg-[var(--color-surface)]">
        <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-2.5 py-1.5">
          <span className="text-[12px] font-semibold">用户列表</span>
          <span className="text-[11.5px] text-[var(--color-fg-subtle)]">共 {rows?.length ?? 0} 个用户</span>
          {canEdit && selected ? (
            <div className="ml-auto flex gap-2">
              <Btn onClick={() => openEdit(selected)}>
                编辑
              </Btn>
              <Btn
                onClick={() => {
                  if (!needSelection()) return
                  setResetting(selected)
                  setNewPw('')
                  setNewPw2('')
                  setFormErr('')
                }}
              >
                重置密码
              </Btn>
              <Btn
                danger
                onClick={() => {
                  if (!needSelection()) return
                  setDeleting(selected)
                }}
              >
                删除
              </Btn>
            </div>
          ) : null}
        </div>

        <div className="min-h-0 flex-1 overflow-auto">
          <table className="data-table">
            <colgroup>
              <col style={{ width: 46 }} />
              <col style={{ width: 180 }} />
              <col />
              <col style={{ width: 110 }} />
              <col style={{ width: 90 }} />
              <col style={{ width: 120 }} />
              <col style={{ width: 100 }} />
            </colgroup>
            <thead>
              <tr>
                <th />
                <th>用户名</th>
                <th>显示名</th>
                <th className="mid">角色</th>
                <th className="mid">状态</th>
                <th>最后登录</th>
                <th>创建时间</th>
              </tr>
            </thead>
            <tbody>
              {(rows ?? []).map((u) => (
                <tr
                  key={u.id}
                  data-selected={sel === u.id}
                  onClick={() => setSel(sel === u.id ? null : u.id)}
                >
                  <td className="mid">
                    <input
                      type="checkbox"
                      checked={sel === u.id}
                      onChange={() => setSel(sel === u.id ? null : u.id)}
                      aria-label={`选择 ${u.username}`}
                    />
                  </td>
                  <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{u.username}</td>
                  <td>{u.display_name ?? ''}</td>
                  <td className="mid">{roleLabel(u.role)}</td>
                  <td className="mid">
                    <Badge kind={u.status === 1 ? 'ok' : 'neutral'}>
                      {u.status === 1 ? '启用' : '停用'}
                    </Badge>
                  </td>
                  <td style={{ color: 'var(--color-fg-muted)' }}>
                    {u.last_login_at ? shortTime(u.last_login_at) : '—'}
                  </td>
                  <td style={{ color: 'var(--color-fg-muted)' }}>{shortTime(u.created_at)}</td>
                </tr>
              ))}
              {(rows ?? []).length === 0 && (
                <tr>
                  <td colSpan={7} className="py-8 text-center text-[12px] text-[var(--color-fg-subtle)]">
                    还没有用户
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      {/* ---------- 新增 / 编辑 ---------- */}
      {formOpen ? (
        <Modal
          title={editing ? '编辑用户' : '新增用户'}
          onClose={() => setFormOpen(false)}
          width={440}
          footer={
            <>
              <Btn onClick={() => setFormOpen(false)}>取消</Btn>
              <Btn primary disabled={busy} onClick={save}>
                {busy ? '保存中…' : '确定'}
              </Btn>
            </>
          }
        >
          <FormError text={formErr} />

          {editing ? (
            <FormRow label="用户名" hint="用户名不可修改">
              <TextInput value={draft.username} onChange={() => undefined} disabled mono />
            </FormRow>
          ) : (
            <FormRow label="用户名">
              <TextInput
                value={draft.username}
                onChange={(v) => setDraft({ ...draft, username: v })}
                placeholder="用户名"
                autoFocus
              />
            </FormRow>
          )}

          <FormRow label="显示名（可选）">
            <TextInput
              value={draft.displayName}
              onChange={(v) => setDraft({ ...draft, displayName: v })}
              placeholder="显示名"
            />
          </FormRow>

          {!editing ? (
            <>
              <FormRow label="密码" hint="至少 4 位">
                <PasswordInput value={draft.password} onChange={(v) => setDraft({ ...draft, password: v })} />
              </FormRow>
              <FormRow label="确认密码">
                <PasswordInput
                  value={draft.confirm}
                  onChange={(v) => setDraft({ ...draft, confirm: v })}
                  onEnter={() => void submit()}
                />
              </FormRow>
            </>
          ) : null}

          <FormRow label="角色">
            <Select
              value={draft.role}
              onChange={(v) => setDraft({ ...draft, role: v })}
              options={ROLES}
            />
          </FormRow>

          {editing ? (
            <FormRow label="状态">
              <Select
                value={String(draft.status)}
                onChange={(v) => setDraft({ ...draft, status: Number(v) })}
                options={[
                  { value: '1', label: '启用' },
                  { value: '0', label: '停用' },
                ]}
              />
            </FormRow>
          ) : null}

          {editing && editing.id !== meId && draft.role !== editing.role ? (
            <div className="rounded-[6px] border border-[var(--color-warn)]/30 bg-[var(--color-warn-bg)] px-2.5 py-1.5 text-[11px] text-[var(--color-warn)]">
              改角色会让该用户当前的所有登录立即失效（含「记住登录」凭证），需要重新登录。
            </div>
          ) : null}
        </Modal>
      ) : null}

      {/* ---------- 角色变更确认 ---------- */}
      {roleChange ? (
        <ConfirmDialog
          title="确认变更角色"
          message={
            <>
              确定把 <b>{roleChange.username}</b> 的角色从【{roleLabel(roleChange.role)}】改为
              【{roleLabel(draft.role)}】？
              <div className="mt-1.5 text-[11px] text-[var(--color-fg-muted)]">
                该用户当前的所有登录会立即失效，需要重新登录。
              </div>
            </>
          }
          busy={busy}
          onConfirm={() => void confirmRoleChange()}
          onCancel={() => setRoleChange(null)}
        />
      ) : null}

      {/* ---------- 重置密码 ---------- */}
      {resetting ? (
        <Modal
          title={`重置密码 - ${resetting.username}`}
          onClose={() => setResetting(null)}
          width={400}
          footer={
            <>
              <Btn onClick={() => setResetting(null)}>取消</Btn>
              <Btn primary disabled={busy} onClick={() => void doReset()}>
                {busy ? '提交中…' : '确定'}
              </Btn>
            </>
          }
        >
          <FormError text={formErr} />
          <FormRow label="新密码" hint="至少 4 位">
            <PasswordInput value={newPw} onChange={setNewPw} autoFocus />
          </FormRow>
          <FormRow label="确认密码">
            <PasswordInput value={newPw2} onChange={setNewPw2} onEnter={() => void doReset()} />
          </FormRow>
        </Modal>
      ) : null}

      {/* ---------- 删除 ---------- */}
      {deleting ? (
        <ConfirmDialog
          title="确认删除"
          danger
          busy={busy}
          message={`确定删除用户 ${deleting.username}？`}
          onConfirm={() => void doDelete()}
          onCancel={() => setDeleting(null)}
        />
      ) : null}
    </>
  )
}

/**
 * 把后端文案翻成人话。
 *
 * 唯一键冲突那一条必须处理：`uniqueConstraintMessage` 里没有 `users.username`
 * 的映射（errors.go:28-32 只覆盖 parts.code / products.code / bom_items），
 * 所以重名用户拿到的是 `唯一键冲突: Error 1062 (23000): Duplicate entry ...`。
 */
function mapUserError(e: unknown, isUpdate: boolean): string {
  const raw = e instanceof Error ? e.message : '保存失败'
  if (/UNIQUE constraint failed: users\.username|Duplicate entry .*users\.username/.test(raw)) {
    return '该用户名已存在'
  }
  if (raw.includes('至少需保留一个启用状态的管理员')) {
    return '至少需保留一个启用状态的管理员'
  }
  if (raw === 'invalid credentials') return '用户名或密码错误'
  return isUpdate ? `保存失败：${raw}` : `新增失败：${raw}`
}
