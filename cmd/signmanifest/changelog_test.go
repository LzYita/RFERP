package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readChangelog(t *testing.T, path string) changelogFile {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	var f changelogFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	return f
}

func TestAppendChangelogCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "changelog.json")

	if err := appendChangelog(path, "1.0.0", "2026-01-01T00:00:00Z", "首个版本"); err != nil {
		t.Fatalf("appendChangelog 失败: %v", err)
	}

	f := readChangelog(t, path)
	if len(f.Versions) != 1 || f.Versions[0].Version != "1.0.0" {
		t.Fatalf("内容不对: %+v", f.Versions)
	}
}

func TestAppendChangelogSortsNewestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "changelog.json")

	for _, v := range []string{"1.0.0", "1.2.0", "1.1.0"} {
		if err := appendChangelog(path, v, "2026-01-01T00:00:00Z", "说明 "+v); err != nil {
			t.Fatal(err)
		}
	}

	f := readChangelog(t, path)
	want := []string{"1.2.0", "1.1.0", "1.0.0"}
	if len(f.Versions) != len(want) {
		t.Fatalf("版本数 = %d，期望 %d", len(f.Versions), len(want))
	}
	for i, w := range want {
		if f.Versions[i].Version != w {
			t.Errorf("第 %d 位 = %q，期望 %q（应从新到旧）", i, f.Versions[i].Version, w)
		}
	}
}

// TestAppendChangelogSameVersionReplaces 重跑发布不能产生重复条目。
func TestAppendChangelogSameVersionReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "changelog.json")

	if err := appendChangelog(path, "1.0.0", "2026-01-01T00:00:00Z", "初版说明"); err != nil {
		t.Fatal(err)
	}
	if err := appendChangelog(path, "1.0.1", "2026-01-02T00:00:00Z", "次版说明"); err != nil {
		t.Fatal(err)
	}
	// 重跑 1.0.0
	if err := appendChangelog(path, "1.0.0", "2026-01-03T00:00:00Z", "修订后的说明"); err != nil {
		t.Fatal(err)
	}

	f := readChangelog(t, path)
	if len(f.Versions) != 2 {
		t.Fatalf("版本数 = %d，期望 2（重跑不应重复累积）", len(f.Versions))
	}
	if f.Versions[0].Version != "1.0.1" || f.Versions[1].Version != "1.0.0" {
		t.Fatalf("顺序不对: %+v", f.Versions)
	}
	if f.Versions[1].Notes != "修订后的说明" {
		t.Errorf("同版本应被替换，实际说明 = %q", f.Versions[1].Notes)
	}
}

// TestAppendChangelogRejectsBrokenFile 已有文件损坏时必须报错而不是静默覆盖，
// 否则一次误操作就会丢掉全部历史更新说明。
func TestAppendChangelogRejectsBrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "changelog.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := appendChangelog(path, "1.0.0", "2026-01-01T00:00:00Z", "说明"); err == nil {
		t.Fatal("文件损坏时应返回错误，而不是静默覆盖")
	}
	// 原文件应保持不变
	b, _ := os.ReadFile(path)
	if string(b) != "{ not json" {
		t.Errorf("损坏文件被改写了: %q", string(b))
	}
}

// TestAppendChangelogPreservesOtherFields 累积只应改 versions，
// 其它字段原样保留。
func TestAppendChangelogPreservesOtherFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "changelog.json")
	seed := `{"$comment":"人工说明","versions":[]}`
	if err := os.WriteFile(path, []byte(seed), 0644); err != nil {
		t.Fatal(err)
	}
	if err := appendChangelog(path, "1.0.0", "2026-01-01T00:00:00Z", "说明"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["$comment"] != "人工说明" {
		t.Errorf("其它字段丢失: %v", m)
	}
}

// TestAccumulateNeedsPathAndVersion -changelog-only 缺参数时应报错，
// 而不是静默写坏文件。
func TestAccumulateNeedsPathAndVersion(t *testing.T) {
	dir := t.TempDir()
	cf := filepath.Join(dir, "changelog.json")

	if err := accumulate("", "1.3.0", "", ""); err == nil {
		t.Error("未指定文件路径时应报错")
	}
	if err := accumulate(cf, "", "", ""); err == nil {
		t.Error("未指定版本号时应报错")
	}
	if _, err := os.Stat(cf); !os.IsNotExist(err) {
		t.Error("参数不合法时不应创建文件")
	}
}

// TestAccumulateReadsNotesFile -notes-file 的内容应写入，且去掉尾部空行。
func TestAccumulateReadsNotesFile(t *testing.T) {
	dir := t.TempDir()
	cf := filepath.Join(dir, "changelog.json")
	nf := filepath.Join(dir, "notes.txt")
	body := "RFERP v1.3.0\n\n第一项\n\n- 说明一\n"
	if err := os.WriteFile(nf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := accumulate(cf, "1.3.0", "被忽略的 -notes", nf); err != nil {
		t.Fatalf("accumulate: %v", err)
	}

	f := readChangelog(t, cf)
	if len(f.Versions) != 1 {
		t.Fatalf("版本数 = %d，期望 1", len(f.Versions))
	}
	got := f.Versions[0]
	if got.Version != "1.3.0" {
		t.Errorf("版本 = %q", got.Version)
	}
	if strings.Contains(got.Notes, "被忽略的") {
		t.Error("-notes-file 应优先于 -notes")
	}
	if strings.HasSuffix(got.Notes, "\n") {
		t.Error("尾部换行应被去掉")
	}
	if !strings.Contains(got.Notes, "第一项") {
		t.Errorf("正文缺失: %q", got.Notes)
	}
	if got.PublishedAt == "" {
		t.Error("应记录发布时间，否则「更新说明」页无法排序")
	}
}

// TestAccumulateMissingNotesFile -notes-file 指向不存在的文件时应报错。
func TestAccumulateMissingNotesFile(t *testing.T) {
	dir := t.TempDir()
	err := accumulate(filepath.Join(dir, "changelog.json"), "1.3.0", "",
		filepath.Join(dir, "nope.txt"))
	if err == nil {
		t.Fatal("说明文件不存在时应报错")
	}
}

// TestAccumulateIsIdempotent 同一版本重复累积应替换而非追加，
// 这样发版重跑不会在「更新说明」里留下两条相同版本。
func TestAccumulateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	cf := filepath.Join(dir, "changelog.json")
	nf := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(nf, []byte("第一版说明"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accumulate(cf, "1.3.0", "", nf); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nf, []byte("第二版说明"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accumulate(cf, "1.3.0", "", nf); err != nil {
		t.Fatal(err)
	}

	f := readChangelog(t, cf)
	if len(f.Versions) != 1 {
		t.Fatalf("版本数 = %d，期望 1（重复发版不应追加）", len(f.Versions))
	}
	if f.Versions[0].Notes != "第二版说明" {
		t.Errorf("说明 = %q，期望被替换为最新一版", f.Versions[0].Notes)
	}
}
