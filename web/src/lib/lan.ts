import { api, ApiError } from './api'

/**
 * 局域网版的界面数据面：授权、设备、部署形态。
 *
 * 与 api.ts 分开的理由：这些能力**只在局域网形态存在**。单机桌面与服务端
 * 是两套部署事实，混进 api.ts 会让"这个方法什么时候能调"变得含糊。
 */

export type Deployment = 'local' | 'lan' | 'client'

/** 授权与额度的运行时快照（服务端 usecase.DeviceRuntime）。 */
export interface DeviceRuntime {
  licensed: boolean
  kind: string
  max_clients: number
  used_clients: number
  customer?: string
  edition?: string
  support_until?: string
  expires_at?: string
  read_only: boolean
  policy_message?: string
}

export interface LANStatus {
  deployment: Deployment
  runtime?: DeviceRuntime
  hostDevice?: string
  /** 仅客户端模式：当前连接的主机地址 */
  serverUrl?: string
}

export interface LicenseStatus {
  runtime: DeviceRuntime
  key_id?: string
  imported_at?: string
  imported_by?: string
}

export interface Device {
  id: number
  device_id: string
  name: string
  kind: 'host' | 'client'
  status: 'active' | 'revoked'
  user_id?: number
  app_version: string
  os: string
  first_seen_at: string
  last_seen_at: string
  last_ip: string
  revoked_at?: string
  revoked_by?: string
}

export interface DeviceList {
  devices: Device[]
  runtime: DeviceRuntime
  hostDevice: string
}

export interface SetupStatus {
  needsSetup: boolean
  /** 主机在空库启动时签发了初始化令牌，首建管理员必须带上它（FR-41）。 */
  tokenRequired?: boolean
}

export const lan = {
  status: () => api.get<LANStatus>('/api/lan/status'),
  setupStatus: () => api.get<SetupStatus>('/api/setup/status'),
  license: () => api.get<LicenseStatus>('/api/license'),
  /**
   * 导入授权。服务端接受文件原文或 {license: text}，这里走后者：
   * 界面拿到的是用户在文件选择框里读出的文本，包一层 JSON 更明确。
   */
  importLicense: (text: string) => api.post<LicenseStatus>('/api/license/import', { license: text }),
  devices: () => api.get<DeviceList>('/api/devices'),
  revoke: (deviceId: string) => api.post<{ status: string }>('/api/devices/revoke', { device_id: deviceId }),
  remove: (deviceId: string) => api.post<{ status: string }>('/api/devices/delete', { device_id: deviceId }),
  connect: (url: string) => api.post<{ serverUrl: string }>('/api/lan/connect', { url }),
}

/**
 * 把局域网相关的错误翻译成用户能行动的一句话。
 *
 * 服务端的 403 有多种含义（额度满 / 设备被撤销 / 授权到期 / 无授权），
 * 只显示 error 文案时四个场景长得一样；这里按 code 再补一句"接下来该做什么"。
 */
export function lanErrorText(e: unknown): string {
  if (e instanceof ApiError) {
    switch (e.code) {
      case 'device_limit':
        return `${e.message}（设置 → 授权与设备 里可以撤销或删除闲置设备）`
      case 'device_revoked':
        return `${e.message}（请联系管理员在设备列表里删除该记录后重新登录）`
      case 'license_missing':
        return `${e.message}（请在主机上导入授权文件）`
      case 'license_expired':
        return `${e.message}（数据仍可查询与备份，续期后自动恢复写入）`
      case 'device_missing':
        return `${e.message}（客户端版本过旧，需要升级）`
      case 'bootstrap_token_invalid':
        return `${e.message}（令牌在主机数据目录的「初始化令牌.txt」里）`
    }
    return e.message
  }
  return e instanceof Error ? e.message : String(e)
}
