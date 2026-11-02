package repository

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"app/internal/model"
)

// 设备 / 授权 / 安全事件的持久化（局域网版 v14/v15）。
//
// 放在同一文件里、两个实现并排，是因为这套表最怕"只改一侧"（D-015）：
// 设备配额算错一个口径，表现就是"第 3 台能登录"或"第 2 台被拒"，
// 而两种症状都不会报错。并排写便于逐条对照。

// ---- MySQL ----

func (r *Repository) GetDeviceByHash(hash string) (*model.Device, error) {
	var d model.Device
	err := r.db.Get(&d, `SELECT * FROM devices WHERE device_id_hash=?`, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *Repository) ListDevices() ([]model.Device, error) {
	var list []model.Device
	err := r.db.Select(&list, `SELECT * FROM devices ORDER BY kind DESC, last_seen_at DESC, id`)
	return list, err
}

// SaveDeviceSeen 登记或刷新一台设备，返回最终记录。
//
// 用"先更新、再插入"而不是 MySQL 的 ON DUPLICATE KEY UPDATE：SQLite 侧的
// 等价语法是 ON CONFLICT DO UPDATE，两边写法不同，容易在某一侧漏字段。
// 这里只依赖最基础的 UPDATE/INSERT，两端逐字一致。
func (r *Repository) SaveDeviceSeen(d *model.Device) (*model.Device, error) {
	now := time.Now()
	res, err := r.db.Exec(deviceSeenSQL(d.Kind),
		deviceSeenArgs(d, now)...)
	if err != nil {
		return nil, TranslateError(err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return r.GetDeviceByHash(d.DeviceIDHash)
	}
	d.FirstSeenAt = now
	d.LastSeenAt = now
	if _, err := r.db.Exec(`INSERT INTO devices
		(device_id_hash,name,kind,status,user_id,app_version,os,first_seen_at,last_seen_at,last_ip,revoked_by)
		VALUES (?,?,?,?,?,?,?,?,?,?,'')`,
		d.DeviceIDHash, d.Name, d.Kind, model.DeviceStatusActive, d.UserID,
		d.AppVersion, d.OS, now, now, d.LastIP); err != nil {
		return nil, TranslateError(err)
	}
	return r.GetDeviceByHash(d.DeviceIDHash)
}

func (r *Repository) RevokeDevice(hash, by string) error {
	res, err := r.db.Exec(`UPDATE devices SET status=?, revoked_at=?, revoked_by=?
		WHERE device_id_hash=?`, model.DeviceStatusRevoked, time.Now(), by, hash)
	if err != nil {
		return TranslateError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) DeleteDevice(hash string) error {
	res, err := r.db.Exec(`DELETE FROM devices WHERE device_id_hash=?`, hash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountDevices 统计指定类型的设备数；status 为空表示不计状态。
//
// 额度口径（§7.4）：**撤销不释放名额，删除才释放**。所以准入判断用
// status="" 统计全部登记行——否则"撤销一台、再加一台"就能绕过额度，
// 而撤销的本意只是"这台不许再登录"。
func (r *Repository) CountDevices(kind, status string) (int, error) {
	var n int
	var err error
	if status == "" {
		err = r.db.Get(&n, `SELECT COUNT(*) FROM devices WHERE kind=?`, kind)
	} else {
		err = r.db.Get(&n, `SELECT COUNT(*) FROM devices WHERE kind=? AND status=?`, kind, status)
	}
	return n, err
}

func (r *Repository) GetLicense() (*model.LicenseRecord, error) {
	var rec model.LicenseRecord
	err := r.db.Get(&rec, `SELECT payload,signature,key_id,imported_at,imported_by FROM license WHERE id=1`)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		// 表尚未迁移（v15 之前）时也按"没有授权"处理：服务端要能启动并
		// 提示导入授权，而不是因为表不存在直接崩。
		if isMissingTable(err) {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

func (r *Repository) SaveLicense(rec *model.LicenseRecord) error {
	_, err := r.db.Exec(`INSERT INTO license (id,payload,signature,key_id,imported_at,imported_by)
		VALUES (1,?,?,?,?,?)
		ON DUPLICATE KEY UPDATE payload=VALUES(payload), signature=VALUES(signature),
		key_id=VALUES(key_id), imported_at=VALUES(imported_at), imported_by=VALUES(imported_by)`,
		rec.Payload, rec.Signature, rec.KeyID, rec.ImportedAt, rec.ImportedBy)
	return TranslateError(err)
}

func (r *Repository) AppendSecurityEvent(e *model.SecurityEvent) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	res, err := r.db.Exec(`INSERT INTO security_events (event_type,actor,detail,ip,created_at)
		VALUES (?,?,?,?,?)`, e.EventType, e.Actor, e.Detail, e.IP, e.CreatedAt)
	if err != nil {
		if isMissingTable(err) {
			// 留痕失败不该把主操作带崩：授权已经生效、设备已经登记，
			// 少一条流水是可接受的降级，掩盖掉主操作失败才是不可接受的。
			return nil
		}
		return err
	}
	id, _ := res.LastInsertId()
	e.ID = id
	return nil
}

func (r *Repository) ListSecurityEvents(limit int) ([]model.SecurityEvent, error) {
	var list []model.SecurityEvent
	if limit <= 0 {
		limit = 50
	}
	err := r.db.Select(&list, `SELECT * FROM security_events ORDER BY id DESC LIMIT ?`, limit)
	if isMissingTable(err) {
		return nil, nil
	}
	return list, err
}

// Ping 用于 /readyz：库连不上时返回错误。
func (r *Repository) Ping() error { return r.db.Ping() }

// ---- SQLite ----

func (s *SQLiteStore) GetDeviceByHash(hash string) (*model.Device, error) {
	var d model.Device
	err := s.db.Get(&d, `SELECT * FROM devices WHERE device_id_hash=?`, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *SQLiteStore) ListDevices() ([]model.Device, error) {
	var list []model.Device
	err := s.db.Select(&list, `SELECT * FROM devices ORDER BY kind DESC, last_seen_at DESC, id`)
	return list, err
}

func (s *SQLiteStore) SaveDeviceSeen(d *model.Device) (*model.Device, error) {
	now := time.Now()
	res, err := s.db.Exec(deviceSeenSQL(d.Kind),
		deviceSeenArgs(d, now)...)
	if err != nil {
		return nil, TranslateError(err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return s.GetDeviceByHash(d.DeviceIDHash)
	}
	d.FirstSeenAt = now
	d.LastSeenAt = now
	if _, err := s.db.Exec(`INSERT INTO devices
		(device_id_hash,name,kind,status,user_id,app_version,os,first_seen_at,last_seen_at,last_ip,revoked_by)
		VALUES (?,?,?,?,?,?,?,?,?,?,'')`,
		d.DeviceIDHash, d.Name, d.Kind, model.DeviceStatusActive, d.UserID,
		d.AppVersion, d.OS, now, now, d.LastIP); err != nil {
		return nil, TranslateError(err)
	}
	return s.GetDeviceByHash(d.DeviceIDHash)
}

func (s *SQLiteStore) RevokeDevice(hash, by string) error {
	res, err := s.db.Exec(`UPDATE devices SET status=?, revoked_at=?, revoked_by=?
		WHERE device_id_hash=?`, model.DeviceStatusRevoked, time.Now(), by, hash)
	if err != nil {
		return TranslateError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) DeleteDevice(hash string) error {
	res, err := s.db.Exec(`DELETE FROM devices WHERE device_id_hash=?`, hash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) CountDevices(kind, status string) (int, error) {
	var n int
	var err error
	if status == "" {
		err = s.db.Get(&n, `SELECT COUNT(*) FROM devices WHERE kind=?`, kind)
	} else {
		err = s.db.Get(&n, `SELECT COUNT(*) FROM devices WHERE kind=? AND status=?`, kind, status)
	}
	return n, err
}

func (s *SQLiteStore) GetLicense() (*model.LicenseRecord, error) {
	var rec model.LicenseRecord
	err := s.db.Get(&rec, `SELECT payload,signature,key_id,imported_at,imported_by FROM license WHERE id=1`)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		if isMissingTable(err) {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

func (s *SQLiteStore) SaveLicense(rec *model.LicenseRecord) error {
	_, err := s.db.Exec(`INSERT INTO license (id,payload,signature,key_id,imported_at,imported_by)
		VALUES (1,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET payload=excluded.payload, signature=excluded.signature,
		key_id=excluded.key_id, imported_at=excluded.imported_at, imported_by=excluded.imported_by`,
		rec.Payload, rec.Signature, rec.KeyID, rec.ImportedAt, rec.ImportedBy)
	return TranslateError(err)
}

func (s *SQLiteStore) AppendSecurityEvent(e *model.SecurityEvent) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	res, err := s.db.Exec(`INSERT INTO security_events (event_type,actor,detail,ip,created_at)
		VALUES (?,?,?,?,?)`, e.EventType, e.Actor, e.Detail, e.IP, e.CreatedAt)
	if err != nil {
		if isMissingTable(err) {
			return nil
		}
		return err
	}
	id, _ := res.LastInsertId()
	e.ID = id
	return nil
}

func (s *SQLiteStore) ListSecurityEvents(limit int) ([]model.SecurityEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	var list []model.SecurityEvent
	err := s.db.Select(&list, `SELECT * FROM security_events ORDER BY id DESC LIMIT ?`, limit)
	if isMissingTable(err) {
		return nil, nil
	}
	return list, err
}

func (s *SQLiteStore) Ping() error { return s.db.Ping() }

// deviceSeenSQL 生成"刷新设备在线事实"的 UPDATE。
//
// 主机这一支顺带把 status 复位成 active：主机行如果被（旧版本或误操作）
// 撤销过，服务重启时应当恢复——它是唯一能在授权锁死时救场的设备（R7），
// 让它因为一行历史状态永远进不来是最糟的失败方式。
func deviceSeenSQL(kind string) string {
	if kind == model.DeviceKindHost {
		return `UPDATE devices SET name=?, kind=?, status='active', revoked_at=NULL,
			revoked_by='', app_version=?, os=?, last_seen_at=?, last_ip=?, user_id=?
			WHERE device_id_hash=?`
	}
	return `UPDATE devices SET name=?, kind=?, app_version=?, os=?,
		last_seen_at=?, last_ip=?, user_id=? WHERE device_id_hash=?`
}

// deviceSeenArgs 两种 SQL 的参数顺序相同（status 复位用字面量，不占参数）。
func deviceSeenArgs(d *model.Device, now time.Time) []any {
	return []any{d.Name, d.Kind, d.AppVersion, d.OS, now, d.LastIP, d.UserID, d.DeviceIDHash}
}

// isMissingTable 识别"表不存在"这一类错误（两端驱动文案不同）。
//
// 只在授权/流水读取时使用：这两处允许在更老的库上启动并提示导入授权。
// 设备表不套用——设备表缺失说明迁移没跑，那是启动失败，不该静默降级成
// "所有设备都算新设备"。
func isMissingTable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "doesn't exist") ||
		strings.Contains(msg, "Error 1146")
}
