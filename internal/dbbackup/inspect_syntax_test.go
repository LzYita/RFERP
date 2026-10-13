package dbbackup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 「引号未闭合」的检测关口。
//
// 真实起因：D:\仓库数据 里那份 09-24 的 pre_restore 副本，是
// `mysqldump | Out-File` 在 PowerShell(GBK 控制台) 里生成的。UTF-8 字节被按
// GBK 解码，中文变乱码，还吃掉了字符串的**结束引号**。结果是 MySQL 回
// Error 1064，而那已经发生在用户输完口令、点完恢复之后。
//
// 这一组用例守的重点是**别把好文件误判成坏文件**：引号计数的奇偶性很容易
// 被转义规则骗到，误报比不报更糟——用户会以为自己的备份坏了。

func writeRaw(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "backup_20260924_172919.sql")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func dumpHeader() string {
	return "-- MySQL dump 10.13  Distrib 8.0.43\n-- Host: 127.0.0.1    Database: appliancedb\n" +
		"CREATE TABLE `parts` (\n  `id` int NOT NULL\n) ENGINE=InnoDB;\n"
}

// 这就是那份损坏文件的真实形态：中文变乱码，且值的结束引号被吃掉了。
const brokenRow = "INSERT INTO `batch_trace` VALUES (2,1,1,'LOT-M001-0901',200.00,'瀹佹尝浜旈噾?,'2026-09-21 09:48:56','婕旂?),(3,1,2,'LOT-M002-0825',200.00,'涓滆帪鐢靛瓙','2026-09-21 09:48:56','婕旂?);"

func TestInspectDumpRejectsUnclosedQuotes(t *testing.T) {
	p := writeRaw(t, dumpHeader()+brokenRow+"\n-- Dump completed on 2026-09-24 17:29:19\n")

	cov := InspectDump(p, required)
	if cov.Usable {
		t.Fatal("引号未闭合的 INSERT 必然被 MySQL 拒绝，必须判为不可用")
	}
	if !strings.Contains(cov.Reason, "引号未闭合") {
		t.Errorf("原因要说清是引号问题：%q", cov.Reason)
	}
	// 不可用时不得同时报缺表：会把用户引向错的方向。
	if len(cov.MissingTables) != 0 {
		t.Errorf("不可用时不应报缺表：%v", cov.MissingTables)
	}
}

// 反斜杠转义的引号不参与计数。`'it\'s'` 只有 3 个引号字符，但其中 1 个被转义，
// 有效引号是 2 个 —— 若不跳过它，每个含撇号的中文备注都会被误判成损坏。
func TestInspectDumpAcceptsBackslashEscapedQuotes(t *testing.T) {
	body := dumpHeader() +
		"INSERT INTO `parts` VALUES (1,'it\\'s fine'),(2,'don\\'t');\n" +
		"-- Dump completed on 2026-09-24 17:29:19\n"
	cov := InspectDump(writeRaw(t, body), required)
	if !cov.Usable {
		t.Errorf("反斜杠转义的撇号不该被判为损坏：%q", cov.Reason)
	}
}

// 双写转义（”）两个引号都计入，奇偶性不受影响，同样不该误报。
func TestInspectDumpAcceptsDoubledQuotes(t *testing.T) {
	body := dumpHeader() +
		"INSERT INTO `parts` VALUES (1,'it''s fine'),(2,'don''t');\n" +
		"-- Dump completed on 2026-09-24 17:29:19\n"
	cov := InspectDump(writeRaw(t, body), required)
	if !cov.Usable {
		t.Errorf("双写撇号不该被判为损坏：%q", cov.Reason)
	}
}

// 跨行 INSERT：逐行数引号会误判，必须按**整条语句**判断。
func TestInspectDumpAcceptsMultiLineInsert(t *testing.T) {
	body := dumpHeader() +
		"INSERT INTO `parts` VALUES\n  (1,'常熟仓库'),\n  (2,'总仓库'),\n  (3,'待检区');\n" +
		"-- Dump completed on 2026-09-24 17:29:19\n"
	cov := InspectDump(writeRaw(t, body), required)
	if !cov.Usable {
		t.Errorf("合法的跨行 INSERT 不该被判为损坏：%q", cov.Reason)
	}
}

// 跨行且真的坏了，也要能查出来（否则上面的放宽就把检测能力一起放掉了）。
func TestInspectDumpRejectsBrokenMultiLineInsert(t *testing.T) {
	body := dumpHeader() +
		"INSERT INTO `parts` VALUES\n  (1,'常熟仓库'),\n  (2,'总仓库?),\n  (3,'待检区');\n" +
		"-- Dump completed on 2026-09-24 17:29:19\n"
	cov := InspectDump(writeRaw(t, body), required)
	if cov.Usable {
		t.Error("跨行且引号未闭合应判为不可用")
	}
}

// 注释里的引号不参与判断：dump 里到处是带撇号的说明文字。
func TestInspectDumpIgnoresQuotesInComments(t *testing.T) {
	body := dumpHeader() +
		"-- it's a dump, don't touch\n/*!40101 SET NAMES utf8mb4 */;\n" +
		"INSERT INTO `parts` VALUES (1,'常熟仓库');\n" +
		"-- Dump completed on 2026-09-24 17:29:19\n"
	cov := InspectDump(writeRaw(t, body), required)
	if !cov.Usable {
		t.Errorf("注释里的撇号不该被判为损坏：%q", cov.Reason)
	}
}

// 窗口边界处还没收尾的语句只是「没读到」，不是「损坏」。
func TestInspectDumpDoesNotJudgeTruncatedStatement(t *testing.T) {
	var b strings.Builder
	b.WriteString(dumpHeader())
	b.WriteString("INSERT INTO `parts` VALUES (1,'常熟仓库');\n")
	// 造一条巨大的未收尾语句，把窗口顶满
	b.WriteString("INSERT INTO `parts` VALUES (2,'")
	b.WriteString(strings.Repeat("x", 900*1024))
	cov := InspectDump(writeRaw(t, b.String()), required)
	if !cov.Usable {
		t.Errorf("被窗口截断的语句不该判为损坏（那只是没读到）：%q", cov.Reason)
	}
}

// 只统计 INSERT：DROP/CREATE 坏了也无所谓，重放根本不执行它们。
func TestInspectDumpIgnoresBrokenNonInsert(t *testing.T) {
	body := dumpHeader() +
		"DROP TABLE IF EXISTS `parts?;\n" +
		"INSERT INTO `parts` VALUES (1,'常熟仓库');\n" +
		"-- Dump completed on 2026-09-24 17:29:19\n"
	cov := InspectDump(writeRaw(t, body), required)
	if !cov.Usable {
		t.Errorf("非 INSERT 语句坏了不影响重放：%q", cov.Reason)
	}
}
