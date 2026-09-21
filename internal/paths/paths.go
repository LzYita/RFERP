package paths

import (
	"os"
	"path/filepath"
)

var dataDir string

func SetDataDir(d string) {
	dataDir = d
}

func DataDir() string {
	if dataDir != "" {
		return dataDir
	}
	if v := os.Getenv("MES_DATA_DIR"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "RFERP")
	}
	return "RFERP"
}

func BackupDir() string {
	return filepath.Join(DataDir(), "备份")
}

func ExportDir() string {
	return filepath.Join(DataDir(), "导出")
}
