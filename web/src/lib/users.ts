/**
 * 用户管理的类型与请求封装。
 *
 * 这一层的请求体有三个坑，全都是「写错了还会返回 200」那一类，所以
 * 字段在这里写死而不是让调用方拼：
 *
 * 1. **字段名是驼峰，不是下划线。** 四个处理器直接反序列化进
 *    `usecase.CreateUserInput` 等结构体，而这些结构体**没有 json tag**
 *    （usecase/inputs.go:34-65），所以 `display_name` 会被**静默丢弃**。
 *    同一个 struct 在同一次响应里又用 `display_name` 返回，读写不对称。
 *
 * 2. **`PUT /api/users` 是三字段全量替换**，不是 PATCH：
 *      UPDATE users SET display_name=?, role=?, status=? WHERE id=?
 *    少发 `displayName` → 显示名被置空；少发 `status` → status=0，
 *    **用户当场被停用**；少发 `role` → 角色被写成空字符串。
 *    与 parts/products 用 `*int` 保护 status 的做法不同，用户这里没有。
 *
 * 3. **没有 DisallowUnknownFields**，所以拼错的键不会 400，只会静默忽略。
 *    这是 1 和 2 变成静默数据损坏的原因。
 *
 * 因此 `updateUser` 强制四个字段全部提供，不给部分更新留口子。
 */

import { api } from './api'

export interface User {
  id: number
  username: string
  display_name?: string
  role: string
  status: number
  last_login_at?: string
  created_at?: string
  updated_at?: string
}

export const ROLES = [
  { value: 'admin', label: '管理员' },
  { value: 'warehouse', label: '仓管' },
  { value: 'production', label: '生产' },
  { value: 'viewer', label: '只读' },
] as const

export function roleLabel(role: string): string {
  return ROLES.find((r) => r.value === role)?.label ?? role
}

export const users = {
  /** 零行时返回 null，不是 [] —— 与 /api/batches 同一种坑。 */
  list: async (): Promise<User[]> => (await api.get<User[] | null>('/api/users')) ?? [],

  /** 注意 displayName 驼峰。 */
  create: (b: { username: string; password: string; displayName: string; role: string }) =>
    api.post<User>('/api/users', b),

  /**
   * 全量替换：三个字段一个都不能少（见文件头第 2 条）。
   * 类型上就把它们设成必填，而不是靠运行时的 if。
   */
  update: (b: { id: number; displayName: string; role: string; status: number }) =>
    api.put<{ status: string }>('/api/users', b),

  remove: (id: number) => api.del<{ status: string }>(`/api/users?id=${id}`),

  resetPassword: (id: number, password: string) =>
    api.post<{ status: string }>('/api/users/reset-password', { id, password }),

  /**
   * 改自己的密码。字段名同样是驼峰：oldPass / newPass。
   *
   * **成功后调用者自己的会话会被撤销**（admin.go:116-117），所以 UI 必须
   * 先告诉用户会被登出，否则他会看到一个来路不明的「登录状态已失效」。
   * 另外 id 必填且必须等于会话用户，否则 403。
   */
  changePassword: (id: number, oldPass: string, newPass: string) =>
    api.post<{ status: string }>('/api/users/change-password', { id, oldPass, newPass }),
}

/**
 * 与后端 auth.ValidatePassword 同规则：按 **rune**（不是字节）计数的 4 位。
 * 所以「密码abc」只有 3 个字符、不足 4 位，而后端会拒——这里提前说清，
 * 别让用户填完一整张表才收到一句「密码至少 4 位」。
 */
export function passwordTooShort(pw: string): boolean {
  return [...pw].length < 4
}