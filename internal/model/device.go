package model

import "time"

// Device 是一台已登记的设备（局域网版 FR-25/26）。
//
// DeviceIDHash 是客户端上报的设备指纹哈希。服务端只存哈希：
// 原始指纹由客户端计算，服务端没有理由也无必要保存它的组成成分。
type Device struct {
	ID           int64      `db:"id" json:"id"`
	DeviceIDHash string     `db:"device_id_hash" json:"device_id"`
	Name         string     `db:"name" json:"name"`
	Kind         string     `db:"kind" json:"kind"`     // host / client
	Status       string     `db:"status" json:"status"` // active / revoked
	UserID       *int64     `db:"user_id" json:"user_id,omitempty"`
	AppVersion   string     `db:"app_version" json:"app_version"`
	OS           string     `db:"os" json:"os"`
	FirstSeenAt  time.Time  `db:"first_seen_at" json:"first_seen_at"`
	LastSeenAt   time.Time  `db:"last_seen_at" json:"last_seen_at"`
	LastIP       string     `db:"last_ip" json:"last_ip"`
	RevokedAt    *time.Time `db:"revoked_at" json:"revoked_at,omitempty"`
	RevokedBy    string     `db:"revoked_by" json:"revoked_by,omitempty"`
}

// 设备类型与状态常量。写成常量而不是自由字符串：数据库里已有这两个值域，
// 拼错时最坏的结果是"主机被当成客户端占额度"，排查代价很高。
const (
	DeviceKindHost   = "host"
	DeviceKindClient = "client"

	DeviceStatusActive  = "active"
	DeviceStatusRevoked = "revoked"
)

// LicenseRecord 是 license 表的单行（原始字节 + 签名，不做二次序列化）。
//
// 存原文而不是解析后的字段：验签必须对**原始字节**做，存解析结果等于
// 放弃"库里被改一个字符就能发现"这个保证。
type LicenseRecord struct {
	Payload    string    `db:"payload" json:"payload"`
	Signature  string    `db:"signature" json:"signature"`
	KeyID      string    `db:"key_id" json:"key_id"`
	ImportedAt time.Time `db:"imported_at" json:"imported_at"`
	ImportedBy string    `db:"imported_by" json:"imported_by"`
}

// SecurityEvent 是一条安全留痕（授权导入、设备登记/撤销、登录失败等）。
type SecurityEvent struct {
	ID        int64     `db:"id" json:"id"`
	EventType string    `db:"event_type" json:"event_type"`
	Actor     string    `db:"actor" json:"actor"`
	Detail    string    `db:"detail" json:"detail"`
	IP        string    `db:"ip" json:"ip"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}
