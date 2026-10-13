package dbbackup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// 备份预检的关口。
//
// 三类断言，各自对应一类会被静默放过的错误：
//
//	编码      —— UTF-16 文件按 UTF-8 读会一行 INSERT 都匹配不到，
//	             于是「业务表全清空、零行回填」却报告成功
//	覆盖度    —— 缺表 = 恢复后该表被清空且无处回填（静默数据丢失）
//	不确定    —— 只读到文件开头时必须承认「无法确认」，不能把「没读到」
//	             谎报成「备份里没有」

var required = []string{"parts", "products", "bom_items", "batch_consumptions"}

func write(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// utf8Dump 造一份结构上像 mysqldump 产物的文件。
func utf8Dump(tables ...string) []byte {
	var b strings.Builder
	b.WriteString("-- MySQL dump 10.13  Distrib 8.0.43, for Win64 (x86_64)\n--\n-- Host: 127.0.0.1    Database: appliancedb\n--\n")
	b.WriteString("/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;\n")
	for _, t := range tables {
		b.WriteString("\n--\n-- Table structure for table `" + t + "`\n--\n\n")
		b.WriteString("DROP TABLE IF EXISTS `" + t + "`;\n")
		b.WriteString("CREATE TABLE `" + t + "` (\n  `id` int NOT NULL\n) ENGINE=InnoDB;\n")
		b.WriteString("LOCK TABLES `" + t + "` WRITE;\n")
		b.WriteString("/*!40000 ALTER TABLE `" + t + "` DISABLE KEYS */;\n")
	}
	b.WriteString("\n-- Dump completed on 2026-09-24 17:08:30\n")
	return []byte(b.String())
}

func TestInspectDumpDetectsMissingTables(t *testing.T) {
	// 这份备份早于 batch_consumptions 引入——D:\仓库数据 里 9 份有 7 份如此。
	p := write(t, "backup_20260922_123649.sql", utf8Dump("parts", "products", "bom_items"))

	cov := InspectDump(p, required)
	if !cov.SchemaKnown {
		t.Fatal("小文件应读完整个内容，SchemaKnown 应为 true")
	}
	if !cov.Usable {
		t.Errorf("UTF-8 文件应可用，Reason=%q", cov.Reason)
	}
	if len(cov.MissingTables) != 1 || cov.MissingTables[0] != "batch_consumptions" {
		t.Errorf("MissingTables = %v，期望 [batch_consumptions]", cov.MissingTables)
	}
}

func TestInspectDumpFullCoverage(t *testing.T) {
	p := write(t, "backup_20260924_170830.final.sql", utf8Dump(required...))
	cov := InspectDump(p, required)
	if !cov.SchemaKnown || !cov.Usable {
		t.Fatalf("应完全可用：known=%v usable=%v reason=%q", cov.SchemaKnown, cov.Usable, cov.Reason)
	}
	if len(cov.MissingTables) != 0 {
		t.Errorf("覆盖完整却报缺表：%v", cov.MissingTables)
	}
}

// 表存在但当时为空 → 没有 INSERT 也**不算**缺表。
// 判据是 schema 里有没有这张表，不是里面有没有行：DELETE 后回填空 = 正确回滚。
func TestInspectDumpEmptyTableStillCountsAsCovered(t *testing.T) {
	p := write(t, "backup_20260721_154617.sql", utf8Dump(required...))
	cov := InspectDump(p, required)
	if len(cov.MissingTables) != 0 {
		t.Errorf("只有 CREATE TABLE 没有 INSERT 不该算缺表：%v", cov.MissingTables)
	}
}

// --------------------------------------------------------------- 编码

func utf16leDump(tables ...string) []byte {
	var b strings.Builder
	b.WriteString("-- MySQL dump 10.13\n-- Host: 127.0.0.1    Database: appliancedb\n")
	for _, t := range tables {
		b.WriteString("CREATE TABLE `" + t + "` (\n  `id` int NOT NULL\n);\n")
	}
	b.WriteString("-- Dump completed on 2026-09-24 17:29:19\n")

	// PowerShell 5.1 的 Out-File 默认写 UTF-16LE **带 BOM**，这正是
	// D:\仓库数据\备份\pre_restore_20260924_172919.sql 的来路。
	u := utf16.Encode([]rune(b.String()))
	out := []byte{0xFF, 0xFE}
	for _, c := range u {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}

func TestInspectDumpRejectsUTF16(t *testing.T) {
	p := write(t, "pre_restore_20260924_172919.sql", utf16leDump(required...))

	cov := InspectDump(p, required)
	if cov.Usable {
		t.Error("UTF-16LE 文件必须判为不可用——按 UTF-8 读会一行 INSERT 都匹配不到")
	}
	if cov.Encoding != "utf16le" {
		t.Errorf("Encoding = %q，期望 utf16le", cov.Encoding)
	}
	if !strings.Contains(cov.Reason, "UTF-16") {
		t.Errorf("原因要指名编码，界面才说得清：%q", cov.Reason)
	}
	// 不可用时**不能**同时报缺表：会把两个问题混成一个，用户会去查错的方向。
	if len(cov.MissingTables) != 0 {
		t.Errorf("不可用时不应报缺表：%v", cov.MissingTables)
	}
}

func TestInspectDumpRejectsUTF16BEBOM(t *testing.T) {
	p := write(t, "backup_20260924_170830.final.sql", []byte{0xFE, 0xFF, 0x00, 'D', 0x00, 'u', 0x00, 'm'})
	cov := InspectDump(p, required)
	if cov.Usable || cov.Encoding != "utf16be" {
		t.Errorf("UTF-16BE 应不可用，got usable=%v enc=%q", cov.Usable, cov.Encoding)
	}
}

// 无 BOM 的 UTF-16（有些工具这么写）也要认出来。
func TestInspectDumpRejectsUTF16WithoutBOM(t *testing.T) {
	src := "CREATE TABLE `parts` (`id` int);\n-- Dump completed\n"
	u := utf16.Encode([]rune(src))
	raw := make([]byte, 0, len(u)*2)
	for _, c := range u {
		raw = append(raw, byte(c), byte(c>>8))
	}
	p := write(t, "manual.sql", raw)
	cov := InspectDump(p, required)
	if cov.Usable {
		t.Errorf("无 BOM 的 UTF-16 也应不可用，got encoding=%q", cov.Encoding)
	}
}

// --------------------------------------------------------------- 不确定

// 文件比读取窗口大、且结构段不在开头 → 必须承认「无法确认」。
//
// 这条最容易写错：若此时把「没读到」当成「备份里没有」，界面就会对着一份
// 完全正常的 GB 级备份说「这张表恢复后会被清空」。
func TestInspectDumpAdmitsUncertainty(t *testing.T) {
	var b strings.Builder
	b.Write(utf8Dump("parts", "products"))
	// 灌入大量无关内容把文件顶过窗口，且窗口内不出现全部 CREATE TABLE。
	pad := strings.Repeat("-- filler line to push the structure section out of the window\n", 60000)
	b.WriteString(pad)

	p := write(t, "huge.sql", []byte(b.String()))
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= coverageWindowBytes {
		t.Fatalf("测试前提不成立：文件 %d 字节，未超过窗口 %d", info.Size(), coverageWindowBytes)
	}

	cov := InspectDump(p, required)
	if cov.SchemaKnown {
		t.Error("只读到窗口就断言覆盖度 = 把「没读到」谎报成「没有」")
	}
	if len(cov.MissingTables) != 0 {
		t.Errorf("不确定时不得列出缺表（那是在编造）：%v", cov.MissingTables)
	}
	if !cov.Usable {
		t.Errorf("只是不确定，不等于不可用：Reason=%q", cov.Reason)
	}
}

func TestInspectDumpMissingFile(t *testing.T) {
	cov := InspectDump(filepath.Join(t.TempDir(), "nope.sql"), required)
	if cov.Usable {
		t.Error("读不到的文件必须不可用")
	}
}

func TestInspectDumpEmptyFile(t *testing.T) {
	p := write(t, "empty.sql", nil)
	cov := InspectDump(p, required)
	// 空文件读完了（SchemaKnown=true），但没有 CREATE TABLE → 全部缺失。
	// 真正的兜底是 service 侧的 VerifyDumpFile，这里只报告事实。
	if !cov.SchemaKnown {
		t.Error("空文件是读完了的")
	}
	if len(cov.MissingTables) != len(required) {
		t.Errorf("空文件应报全部缺失，实际 %v", cov.MissingTables)
	}
}
