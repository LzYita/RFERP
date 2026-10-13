package dbbackup

import (
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// 备份文件的**可恢复性预检**。
//
// 这不是校验（校验是 VerifyDumpFile 的事，失败即中止恢复），而是**提前把后果说清楚**：
// 恢复的实现是「先 DELETE 全部业务表，再从备份里回灌 INSERT」（service/restore.go:107-113）。
// 于是一份备份能不能安全回滚，取决于它**schema 里有没有当前的每张业务表**——
// 表当时为空时 mysqldump 不发 INSERT，但只要 CREATE TABLE 在，空表也能被正确回滚成空表；
// 真正丢数据的是 schema 里**根本没有那张表**的备份：它照样会被 DELETE，且无处回填。
//
// 真实后果见 D:\仓库数据：9 份备份里 7 份早于 batch_consumptions 引入，
// 恢复它们会把该表清空且无法回填。用户在点确认之前应当知道这件事。

// Coverage 是一份备份的预检结果。
type Coverage struct {
	// SchemaKnown 为 false 表示只读了文件开头、没读到结尾，无法断言覆盖度。
	// 这时 MissingTables **没有意义**，界面必须说「无法确认」而不是谎报缺哪几张表。
	SchemaKnown bool

	// MissingTables 是 required 里、但这份备份的 schema 中不存在的表。
	// 仅当 SchemaKnown 为 true 时有效。
	MissingTables []string

	// Encoding 是探测到的文本编码：utf8 / utf16le / utf16be / binary / unknown。
	Encoding string

	// Usable 表示能否作为恢复来源。非 UTF-8 的文件是硬性不可用：
	// service.RestoreDatabase 用 os.ReadFile 按 UTF-8 读它，逐行找 "INSERT INTO"
	// 只会一行都匹配不到，于是「全部业务表被清空、零行回填」却报告成功。
	// VerifyDumpFile 会在此之前按字节找不到完成标记而拦下，但那时用户已经输完口令、
	// 点完确认了——失败得太晚。
	Usable bool

	// Reason 是不可用时的中文原因，直接用于界面。
	Reason string
}

// coverageWindowBytes 是预检最多读取的字节数。
//
// mysqldump 的输出顺序是：文件头 → SET 语句 → 逐表（CREATE TABLE … INSERT …）。
// 所以全部 CREATE TABLE 集中在靠前的部分，1MB 对本项目的表规模绰绰有余
// （实测 10 张表的 dump 里，结构段都在前 20KB 内）。
//
// 之所以要设上限：真实 MySQL 库的 dump 可能是 GB 级，而这里是在**列出备份时**对
// 每个文件跑一次，不能让它把整个备份目录读一遍。
const coverageWindowBytes = 1 << 20

// createTableRe 匹配 mysqldump 的建表语句。兼容反引号、无反引号与
// CREATE TABLE IF NOT EXISTS 三种写法。
var createTableRe = regexp.MustCompile("(?i)CREATE\\s+TABLE(?:\\s+IF\\s+NOT\\s+EXISTS)?\\s+`?([A-Za-z0-9_]+)`?")

// InspectDump 预检一份 .sql 备份能否作为恢复来源，以及它能回填哪些表。
//
// required 是「当前必须被回填的表」（业务表清单），由调用方传入——
// dbbackup 只管文件，不该知道业务有哪些表。
//
// 读不完整个文件时返回 SchemaKnown=false 而不是猜测：那会让界面把
// 「没读到」说成「备份里没有」，是比不显示更糟的误导。
func InspectDump(path string, required []string) Coverage {
	f, err := os.Open(path)
	if err != nil {
		return Coverage{Encoding: "unknown", Usable: false, Reason: "备份文件无法读取"}
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return Coverage{Encoding: "unknown", Usable: false, Reason: "备份文件无法读取"}
	}

	head := make([]byte, min64(info.Size(), coverageWindowBytes))
	n, err := readFull(f, head)
	if err != nil && n == 0 {
		return Coverage{Encoding: "unknown", Usable: false, Reason: "备份文件无法读取"}
	}
	head = head[:n]

	enc := detectEncoding(head)
	cov := Coverage{Encoding: enc}
	if enc != "utf8" && enc != "unknown" {
		cov.Usable = false
		switch enc {
		case "utf16le":
			cov.Reason = "文件是 UTF-16LE 编码（Windows PowerShell 另存为的常见结果），本程序按 UTF-8 读取，无法作为恢复来源"
		case "utf16be":
			cov.Reason = "文件是 UTF-16BE 编码，无法作为恢复来源"
		default:
			cov.Reason = "文件不是 UTF-8 文本，无法作为恢复来源"
		}
		return cov
	}
	if !utf8.Valid(head) && enc == "unknown" {
		cov.Usable = false
		cov.Reason = "文件不是有效的 UTF-8 文本，无法作为恢复来源"
		return cov
	}

	present := map[string]bool{}
	for _, m := range createTableRe.FindAllSubmatch(head, -1) {
		present[strings.ToLower(string(m[1]))] = true
	}

	// 读到了文件结尾才算「读完」。只读了窗口就断言缺表，等于把「没读到」
	// 谎报成「备份里没有」。
	cov.SchemaKnown = int64(n) >= info.Size()
	if cov.SchemaKnown {
		for _, t := range required {
			if !present[strings.ToLower(t)] {
				cov.MissingTables = append(cov.MissingTables, t)
			}
		}
	}

	cov.Usable = true
	return cov
}

// detectEncoding 只做一件事：判断它是不是 UTF-16。这是唯一必须硬性拒绝的编码——
// UTF-16 的字节里每个字符后面跟一个 NUL，按 UTF-8 读会得到满屏乱码，
// 而逐行匹配 "INSERT INTO" 一行都不会命中。
func detectEncoding(head []byte) string {
	switch {
	case len(head) >= 2 && head[0] == 0xFF && head[1] == 0xFE:
		return "utf16le"
	case len(head) >= 2 && head[0] == 0xFE && head[1] == 0xFF:
		return "utf16be"
	}
	// 无 BOM 时也认一下：大量 NUL 交替出现是 UTF-16 的强特征。
	// 阈值取前 512 字节里 NUL 的比例，避免把含二进制块的文本误判。
	probe := head
	if len(probe) > 512 {
		probe = probe[:512]
	}
	if len(probe) >= 8 {
		zeros, pairs := 0, 0
		for i := 0; i+1 < len(probe); i += 2 {
			pairs++
			if probe[i] == 0 && probe[i+1] != 0 {
				zeros++
			} else if probe[i+1] == 0 && probe[i] != 0 {
				zeros++
			}
		}
		if pairs > 0 && zeros*4 >= pairs*3 {
			if probe[0] != 0 {
				return "utf16le"
			}
			return "utf16be"
		}
	}
	if utf8.Valid(head) {
		return "utf8"
	}
	return "unknown"
}

// readFull 读满 buf 或到文件结尾，返回实际字节数。
func readFull(f *os.File, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := f.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			break
		}
	}
	return total, nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
