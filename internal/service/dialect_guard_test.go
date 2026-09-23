package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestServiceHasNoMySQLDialect 门禁（D-013）：业务实现不得出现 MySQL 方言或会话开关。
// 允许出现在适配层：repository / dbbackup / migrate / mysqlfind / config。
func TestServiceHasNoMySQLDialect(t *testing.T) {
	forbidden := []string{
		"JSON_EXTRACT",
		"INSERT IGNORE",
		"ON DUPLICATE",
		"LAST_INSERT_ID",
		"FOREIGN_KEY_CHECKS",
		"UNIQUE_CHECKS",
		"mysqldump",
		"@@",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read service dir: %v", err)
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".go") || strings.HasSuffix(ent.Name(), "_test.go") {
			continue
		}
		// 快照适配文件允许引用 dbbackup/mysqldump 路径名，但不得拼 SQL 会话方言。
		if ent.Name() == "snapshot_mysql.go" {
			raw, err := os.ReadFile(filepath.Join(".", ent.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", ent.Name(), err)
			}
			src := string(raw)
			for _, bad := range []string{"FOREIGN_KEY_CHECKS", "UNIQUE_CHECKS", "JSON_EXTRACT", "INSERT IGNORE", "ON DUPLICATE"} {
				if strings.Contains(src, bad) {
					t.Fatalf("%s contains forbidden dialect %q", ent.Name(), bad)
				}
			}
			continue
		}
		raw, err := os.ReadFile(filepath.Join(".", ent.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", ent.Name(), err)
		}
		src := string(raw)
		for _, bad := range forbidden {
			if strings.Contains(src, bad) {
				t.Fatalf("%s contains forbidden MySQL dialect marker %q", ent.Name(), bad)
			}
		}
	}
}
