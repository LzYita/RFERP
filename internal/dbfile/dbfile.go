// Package dbfile 识别本地数据库文件。
//
// 放在叶子包是为了让界面层不必依赖 internal/service 才能判断
// 「这个备份文件是不是 SQLite 快照」——UI 只应依赖用例接口与这种无依赖的小工具。
package dbfile

import (
	"io"
	"os"
)

// SQLiteMagic 是 SQLite 数据库文件头的 16 字节魔数。
const SQLiteMagic = "SQLite format 3\x00"

// IsSQLiteSnapshot 判断 path 是否为 SQLite 数据库文件。
//
// 只看文件内容（16 字节文件头），不看扩展名：备份文件被改名也不影响判断。
// 只读文件头，绝不把整份备份读进内存。
func IsSQLiteSnapshot(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var head [16]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return false
	}
	return string(head[:]) == SQLiteMagic
}
