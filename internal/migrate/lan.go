package migrate

import (
	"fmt"

	"github.com/jmoiron/sqlx"
)

// 局域网版（lan-v1）新增的两张/三张表。
//
// 版本号从当前 steps 队列往后接：v19 devices，v20 license + security_events。
// 方案文档写的是 v18/v19；合并上游后基线已到 v18，所以这里落到 v19/v20。以代码里的
// nextVersion() 为准——版本号是迁移顺序的标识，不是对外契约。
//
// D-015 要求双端同时实现：只写一侧会让 MySQL 用户在升级后缺少设备/授权表，
// 而服务端代码已经按"表存在"来写，结果是登录直接报 SQL 错误。

const devicesDDLMySQL = `CREATE TABLE IF NOT EXISTS devices (
	id             BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
	device_id_hash CHAR(64)     NOT NULL,
	name           VARCHAR(128) NOT NULL DEFAULT '',
	kind           VARCHAR(16)  NOT NULL COMMENT 'host / client',
	status         VARCHAR(16)  NOT NULL DEFAULT 'active' COMMENT 'active / revoked',
	user_id        BIGINT       NULL,
	app_version    VARCHAR(32)  NOT NULL DEFAULT '',
	os             VARCHAR(64)  NOT NULL DEFAULT '',
	first_seen_at  DATETIME     NOT NULL,
	last_seen_at   DATETIME     NOT NULL,
	last_ip        VARCHAR(64)  NOT NULL DEFAULT '',
	revoked_at     DATETIME     NULL,
	revoked_by     VARCHAR(128) NOT NULL DEFAULT '',
	UNIQUE KEY uk_devices_hash (device_id_hash),
	KEY idx_devices_status (status)
) COMMENT '局域网设备登记-客户端设备授权依据'`

const devicesDDLite = `CREATE TABLE IF NOT EXISTS devices (
	id             INTEGER PRIMARY KEY,
	device_id_hash TEXT    NOT NULL UNIQUE,
	name           TEXT    NOT NULL DEFAULT '',
	kind           TEXT    NOT NULL,
	status         TEXT    NOT NULL DEFAULT 'active',
	user_id        INTEGER,
	app_version    TEXT    NOT NULL DEFAULT '',
	os             TEXT    NOT NULL DEFAULT '',
	first_seen_at  DATETIME NOT NULL,
	last_seen_at   DATETIME NOT NULL,
	last_ip        TEXT    NOT NULL DEFAULT '',
	revoked_at     DATETIME,
	revoked_by     TEXT    NOT NULL DEFAULT ''
)`

const licenseDDLMySQL = `CREATE TABLE IF NOT EXISTS license (
	id          TINYINT      NOT NULL PRIMARY KEY CHECK (id = 1),
	payload     TEXT         NOT NULL COMMENT 'Base64 授权原文',
	signature   TEXT         NOT NULL COMMENT 'Base64 Ed25519 签名',
	key_id      VARCHAR(64)  NOT NULL DEFAULT '',
	imported_at DATETIME     NOT NULL,
	imported_by VARCHAR(128) NOT NULL DEFAULT ''
) COMMENT '授权-全库单行'`

const licenseDDLite = `CREATE TABLE IF NOT EXISTS license (
	id          INTEGER PRIMARY KEY CHECK (id = 1),
	payload     TEXT NOT NULL,
	signature   TEXT NOT NULL,
	key_id      TEXT NOT NULL DEFAULT '',
	imported_at DATETIME NOT NULL,
	imported_by TEXT NOT NULL DEFAULT ''
)`

const securityEventsDDLMySQL = `CREATE TABLE IF NOT EXISTS security_events (
	id         BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
	event_type VARCHAR(64)  NOT NULL,
	actor      VARCHAR(128) NOT NULL DEFAULT '',
	detail     VARCHAR(512) NOT NULL DEFAULT '',
	ip         VARCHAR(64)  NOT NULL DEFAULT '',
	created_at DATETIME     NOT NULL,
	KEY idx_security_events_created (created_at)
) COMMENT '安全事件-授权/设备/登录失败留痕'`

const securityEventsDDLite = `CREATE TABLE IF NOT EXISTS security_events (
	id         INTEGER PRIMARY KEY,
	event_type TEXT NOT NULL,
	actor      TEXT NOT NULL DEFAULT '',
	detail     TEXT NOT NULL DEFAULT '',
	ip         TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL
)`

func applyDevicesMySQL(db *sqlx.DB) error  { return execDDL(db, devicesDDLMySQL) }
func applyDevicesSQLite(db *sqlx.DB) error { return execDDL(db, devicesDDLite) }

func applyLicenseAndEventsMySQL(db *sqlx.DB) error {
	if err := execDDL(db, licenseDDLMySQL); err != nil {
		return err
	}
	return execDDL(db, securityEventsDDLMySQL)
}

func applyLicenseAndEventsSQLite(db *sqlx.DB) error {
	if err := execDDL(db, licenseDDLite); err != nil {
		return err
	}
	if err := execDDL(db, securityEventsDDLite); err != nil {
		return err
	}
	return execDDL(db, `CREATE INDEX IF NOT EXISTS idx_security_events_created
		ON security_events(created_at)`)
}

func execDDL(db *sqlx.DB, ddl string) error {
	if _, err := db.Exec(ddl); err != nil {
		return fmt.Errorf("ddl: %w", err)
	}
	return nil
}
