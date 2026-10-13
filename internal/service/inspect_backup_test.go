package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// Service.InspectBackupFile 的关口：它把「文件里有什么」与「当前必须回填什么」
// 这两件事接在一起，而后者只有 service 知道（businessTables）。
//
// 这一层出错的后果是界面说错话：漏报缺表 → 用户以为能安全回滚；
// 谎报缺表 → 用户对着一份完全正常的备份被警告。

func writeInspectFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// dumpFor 造一份覆盖指定业务表的 mysqldump 结构。
func dumpFor(t *testing.T, tables ...string) []byte {
	t.Helper()
	var b strings.Builder
	b.WriteString("-- MySQL dump 10.13  Distrib 8.0.43\n-- Host: 127.0.0.1    Database: appliancedb\n")
	for _, tb := range tables {
		b.WriteString("DROP TABLE IF EXISTS `" + tb + "`;\n")
		b.WriteString("CREATE TABLE `" + tb + "` (\n  `id` int NOT NULL\n) ENGINE=InnoDB;\n")
	}
	b.WriteString("-- Dump completed on 2026-09-24 17:08:30\n")
	return []byte(b.String())
}

func TestInspectBackupFileReportsMissingBusinessTables(t *testing.T) {
	svc, _ := newRestoreMockService(t, &fakeSnapshotPort{kind: "mysql"})
	dir := t.TempDir()

	// 缺 batch_consumptions —— D:\仓库数据 里 9 份备份有 7 份如此。
	p := writeInspectFile(t, dir, "backup_20260922_123649.sql",
		dumpFor(t, "parts", "products", "bom_items", "product_batches", "batch_trace", "batch_skip_parts", "audit_log"))

	insp, err := svc.InspectBackupFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !insp.Usable {
		t.Errorf("UTF-8 备份应可用，Reason=%q", insp.Reason)
	}
	if !insp.SchemaKnown {
		t.Fatal("小文件应读完，覆盖度应已知")
	}
	if len(insp.MissingTables) != 1 || insp.MissingTables[0] != "batch_consumptions" {
		t.Errorf("MissingTables = %v，期望 [batch_consumptions]", insp.MissingTables)
	}
}

func TestInspectBackupFileFullCoverage(t *testing.T) {
	svc, _ := newRestoreMockService(t, &fakeSnapshotPort{kind: "mysql"})
	dir := t.TempDir()

	p := writeInspectFile(t, dir, "backup_20260924_170830.final.sql", dumpFor(t, businessTables...))
	insp, err := svc.InspectBackupFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !insp.Usable || !insp.SchemaKnown || len(insp.MissingTables) != 0 {
		t.Errorf("覆盖完整却报问题：usable=%v known=%v missing=%v", insp.Usable, insp.SchemaKnown, insp.MissingTables)
	}
}

// UTF-16 的副本是真实的：pre_restore_20260924_172919.sql（PowerShell 另存为的产物）。
// 它必须被判为不可用，而且**不能**同时报缺表——否则用户会去查错的方向。
func TestInspectBackupFileRejectsUTF16SafetyCopy(t *testing.T) {
	svc, _ := newRestoreMockService(t, &fakeSnapshotPort{kind: "mysql"})
	dir := t.TempDir()

	src := string(dumpFor(t, businessTables...))
	u := utf16.Encode([]rune(src))
	raw := []byte{0xFF, 0xFE}
	for _, c := range u {
		raw = append(raw, byte(c), byte(c>>8))
	}
	p := writeInspectFile(t, dir, "pre_restore_20260924_172919.sql", raw)

	insp, err := svc.InspectBackupFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if insp.Usable {
		t.Error("UTF-16 副本必须不可用")
	}
	if !strings.Contains(insp.Reason, "UTF-16") {
		t.Errorf("原因要指名编码：%q", insp.Reason)
	}
	if len(insp.MissingTables) != 0 {
		t.Errorf("不可用时不应报缺表：%v", insp.MissingTables)
	}
}

// SQLite 整库快照是整文件替换，不存在表级覆盖度问题。
func TestInspectBackupFileSQLiteSnapshotHasNoCoverage(t *testing.T) {
	svc, _ := newRestoreMockService(t, &fakeSnapshotPort{kind: "sqlite"})
	dir := t.TempDir()

	p := writeInspectFile(t, dir, "pre_restore_20260929_110000.db", []byte("SQLite format 3\x00"))
	insp, err := svc.InspectBackupFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !insp.Usable || !insp.SchemaKnown || len(insp.MissingTables) != 0 {
		t.Errorf("SQLite 快照不该有覆盖度问题：%+v", insp)
	}
}

func TestInspectBackupDirKeysByName(t *testing.T) {
	svc, _ := newRestoreMockService(t, &fakeSnapshotPort{kind: "mysql"})
	dir := t.TempDir()

	writeInspectFile(t, dir, "backup_20260924_170830.final.sql", dumpFor(t, businessTables...))
	writeInspectFile(t, dir, "backup_20260922_123649.sql", dumpFor(t, "parts"))

	got := svc.InspectBackupDir(dir, []string{
		"backup_20260924_170830.final.sql",
		"backup_20260922_123649.sql",
		"missing.sql", // 不存在的文件也要给出结论，不能漏
	})
	if len(got) != 3 {
		t.Fatalf("InspectBackupDir 应给每个名字一个结论，实际 %d", len(got))
	}
	if len(got["backup_20260924_170830.final.sql"].MissingTables) != 0 {
		t.Error("覆盖完整的那份不该报缺表")
	}
	if len(got["backup_20260922_123649.sql"].MissingTables) == 0 {
		t.Error("缺表的那份应报出来")
	}
	if got["missing.sql"].Usable {
		t.Error("读不到的文件必须判为不可用")
	}
}
