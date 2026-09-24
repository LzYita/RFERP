package migrate

import (
	"crypto/rand"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// db_identity 给每个数据库一个稳定身份（D-016）。
//
// 用途：
//   - 服务器自描述：客户端能问出"你用的是哪个数据库"
//   - 接入服务器前的预检：判断本机库与目标服务器库是不是同一个
//     （同一个 → 切换运行模式不需要迁移数据；不同 → 必须先搬数据）
//   - 备份/快照校验：确认快照属于本库
//
// 全库只有一行，写入后永不改动。表结构对齐各自方言的时间约定。
const dbIdentityDDLMySQL = `CREATE TABLE IF NOT EXISTS db_identity (
	id         VARCHAR(36) NOT NULL PRIMARY KEY,
	created_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
) COMMENT '数据库身份-全库唯一标识'`

const dbIdentityDDLite = `CREATE TABLE IF NOT EXISTS db_identity (
	id         TEXT     NOT NULL PRIMARY KEY,
	created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
)`

func applyDBIdentityMySQL(db *sqlx.DB) error {
	return applyDBIdentity(db, dbIdentityDDLMySQL)
}

func applyDBIdentitySQLite(db *sqlx.DB) error {
	return applyDBIdentity(db, dbIdentityDDLite)
}

// applyDBIdentity 建表并在缺行时补一行。幂等：已有身份绝不覆盖。
func applyDBIdentity(db *sqlx.DB, ddl string) error {
	if _, err := db.Exec(ddl); err != nil {
		return err
	}
	return ensureIdentityRow(db)
}

func ensureIdentityRow(db *sqlx.DB) error {
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM db_identity`); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	id, err := newUUID()
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO db_identity (id) VALUES (?)`, id)
	return err
}

// newUUID 生成 RFC 4122 v4 UUID。自己实现以免为这一处引入新依赖。
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate uuid: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
