package service

import (
	"fmt"
	"os"
	"testing"
)

// TestMain 给整个包一个临时数据目录兜底。
//
// 背景：恢复流程会生成"恢复前副本"，落点是 paths.DataDir()。多数用例自己设了
// MES_DATA_DIR 或 paths.SetDataDir，但有少数只依赖默认值——默认是 ~/RFERP，于是：
//
//  1. 测试往用户主目录写 pre_restore_*.db（本机实测到过）；
//  2. 这些文件留着，下一次运行的"保留策略"类用例会看到预期之外的文件而随机失败
//     （与业务无关的假红灯，排查一次要很久）。
//
// 这里把默认值兜底到临时目录、结束整体删除；显式调用 paths.SetDataDir 的用例
// 不受影响（全局优先级高于环境变量）。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rferp-service-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "service tests: 创建临时数据目录失败: %v\n", err)
		os.Exit(1)
	}
	if os.Getenv("MES_DATA_DIR") == "" {
		_ = os.Setenv("MES_DATA_DIR", dir)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
