package usecase

import "app/internal/model"

// 局域网版的准入契约（设备登记 / 授权额度 / 会话绑定）。
//
// 放在 usecase 而不是 api：api 与 service 都要引用这份类型，谁都不能
// import 对方（api → service 是单向的，反过来就是循环）。这里只放
// "问什么、答什么"，不放任何实现。

// DeviceLogin 是一次登录携带的设备事实。
type DeviceLogin struct {
	DeviceID string
	Name     string
	Version  string
	OS       string
	IP       string
}

// 登录拒绝的原因码（同时作为 HTTP 错误契约的 code）。
const (
	CodeDeviceLimit    = "device_limit"
	CodeDeviceRevoked  = "device_revoked"
	CodeLicenseMissing = "license_missing"
	CodeLicenseExpired = "license_expired"
	CodeDeviceMissing  = "device_missing"
	CodeVersionTooOld  = "version_incompatible"
	CodeLicenseInvalid = "license_invalid"
	CodeBootstrapToken = "bootstrap_token_invalid"
	CodeReadOnly       = "read_only"
	CodeNotHost        = "not_host"
)

// DeviceLoginResult 是登录门禁的判定结果。
type DeviceLoginResult struct {
	Allowed bool
	Host    bool
	// Code 为空表示放行；否则是错误契约里的 code。
	Code string
	// Message 是给用户看的中文文案。
	Message string
}

// DeviceRuntime 是授权与设备额度的当前快照（设置页状态条与设备列表用）。
type DeviceRuntime struct {
	Licensed      bool   `json:"licensed"`
	Kind          string `json:"kind"`
	MaxClients    int    `json:"max_clients"`
	UsedClients   int    `json:"used_clients"`
	Customer      string `json:"customer,omitempty"`
	Edition       string `json:"edition,omitempty"`
	SupportUntil  string `json:"support_until,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	ReadOnly      bool   `json:"read_only"`
	PolicyMessage string `json:"policy_message,omitempty"`
}

// DeviceGuard 由 internal/service 实现，供 HTTP 层调用。
type DeviceGuard interface {
	// LoginCheck 校验设备是否允许以该用户登录。
	LoginCheck(in DeviceLogin) (DeviceLoginResult, error)
	// ValidateSession 在已认证请求上复核设备（撤销 / 绑定的设备漂移）。
	ValidateSession(deviceID string) error
	// IsHostDevice 报告该设备指纹是否为主机。
	IsHostDevice(deviceID string) bool
	// Runtime 返回授权/设备运行时状态。
	Runtime() DeviceRuntime
	// HostDeviceID 返回服务端自己登记的主机指纹（用于首启登记与兜底）。
	HostDeviceID() string
}

// LicenseStatus 是授权管理页需要的完整状态（额度 + 导入元数据）。
type LicenseStatus struct {
	Runtime    DeviceRuntime `json:"runtime"`
	KeyID      string        `json:"key_id,omitempty"`
	ImportedAt string        `json:"imported_at,omitempty"`
	ImportedBy string        `json:"imported_by,omitempty"`
}

// LANAdmin 是服务器形态才有的管理面（授权导入、设备列表/撤销）。
//
// 与 DeviceGuard 分开：Guard 是"每个登录都要走的热路径"，Admin 是"设置页
// 偶尔点一次"，两者生命周期与失败语义都不同。
type LANAdmin interface {
	LicenseStatus() (LicenseStatus, error)
	ImportLicense(raw []byte, actor string) (LicenseStatus, error)
	ListDevices() ([]model.Device, error)
	RevokeDevice(deviceID, actor string) error
	DeleteDevice(deviceID, actor string) error
	// EnsureHostDevice 在服务端启动时登记主机。
	EnsureHostDevice() (string, error)
}
