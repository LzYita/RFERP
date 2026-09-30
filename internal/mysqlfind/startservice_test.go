//go:build windows

package mysqlfind

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestRunServiceStartIfRequestedIgnoresNormalArgs 普通启动不得被误判成提权子进程。
// 这是最危险的回归：一旦误判，普通启动会直接退出、什么都不做。
func TestRunServiceStartIfRequestedIgnoresNormalArgs(t *testing.T) {
	cases := [][]string{
		{"RFERP.exe"},
		{"RFERP.exe", "--some-other-flag"},
		{"RFERP.exe", "--rferp-start-mysql-service"},         // 无 "=name"
		{"RFERP.exe", "--rferp-start-mysql-service-other=x"}, // 前缀相近但不相等
		{"RFERP.exe", "-rferp-start-mysql-service=MySQL80"},  // 单横线
	}
	for _, args := range cases {
		handled, _ := RunServiceStartIfRequested(args)
		if handled {
			t.Errorf("参数 %v 不应被识别为提权子进程", args)
		}
	}
}

// TestRunServiceStartIfRequestedHandlesFlag 带正确参数时必须被识别，
// 且不再继续走正常启动流程。
func TestRunServiceStartIfRequestedHandlesFlag(t *testing.T) {
	handled, _ := RunServiceStartIfRequested([]string{
		"RFERP.exe", StartServiceFlag + "=RFERP-不存在的服务-仅用于测试",
	})
	if !handled {
		t.Fatal("带 " + StartServiceFlag + "= 的参数应被识别为提权子进程")
	}
}

// TestStartServiceFlagShape 参数名必须稳定：主程序与提权子进程靠它约定。
func TestStartServiceFlagShape(t *testing.T) {
	if StartServiceFlag != "--rferp-start-mysql-service" {
		t.Fatalf("StartServiceFlag = %q，改动会让已发布的提权调用方式失效", StartServiceFlag)
	}
	if !strings.HasPrefix(StartServiceFlag, "--") {
		t.Error("提权参数应以 -- 开头，避免与普通参数混淆")
	}
}

// TestStartErrorAdminHintIsActionable 锁定「权限不足」时的提示文案。
//
// 背景：旧实现用 `net start` / `sc start` 并把 CombinedOutput 直接当 UTF-8 用。
// 中文 Windows 上这两个命令输出的是控制台代码页 GBK，于是「拒绝访问。」在界面
// 和日志里都显示为乱码；同时那句 "启动服务失败（可能需要管理员权限）" 只在输出
// 为空时才出现，而权限不足恰恰有输出，所以有用的提示永远看不到。
//
// 现在改走 SCM API（Win32 错误码，无编码问题），并在权限不足时给出可操作提示。
func TestStartErrorAdminHintIsActionable(t *testing.T) {
	err := &StartError{Name: "MySQL80", Err: errors.New("Access is denied."), NeedsAdmin: true}
	msg := err.Error()

	for _, want := range []string{"MySQL80", "管理员权限", "以管理员身份运行"} {
		if !strings.Contains(msg, want) {
			t.Errorf("提示缺少 %q，实际输出：%s", want, msg)
		}
	}
	if !utf8.ValidString(msg) {
		t.Errorf("提示不是合法 UTF-8：%q", msg)
	}
	if strings.ContainsRune(msg, utf8.RuneError) {
		t.Errorf("提示中出现替换字符 U+FFFD，说明编码仍有问题：%s", msg)
	}
}

// TestStartErrorNonPermissionNotMisleading 非权限类错误不应把用户引去提权。
func TestStartErrorNonPermissionNotMisleading(t *testing.T) {
	err := &StartError{Name: "MySQL80", Err: errors.New("service does not exist")}
	msg := err.Error()

	if strings.Contains(msg, "管理员权限") {
		t.Errorf("非权限类错误不应提示提权，实际：%s", msg)
	}
	if !strings.Contains(msg, "service does not exist") {
		t.Errorf("提示应包含底层错误，实际：%s", msg)
	}
}

// TestStartErrorNilErr 底层错误为 nil 时也要有可读文案，不能 panic。
func TestStartErrorNilErr(t *testing.T) {
	msg := (&StartError{Name: "MySQL80"}).Error()
	if !strings.Contains(msg, "MySQL80") {
		t.Errorf("nil 底层错误时应给出含服务名的提示，实际：%s", msg)
	}
}

// TestStartErrorUnwrap 调用方需要能用 errors.Is/As 拿到底层错误。
func TestStartErrorUnwrap(t *testing.T) {
	base := errors.New("boom")
	var err error = &StartError{Name: "MySQL80", Err: base}

	if !errors.Is(err, base) {
		t.Fatal("StartError 应能 Unwrap 到底层错误")
	}
	var se *StartError
	if !errors.As(err, &se) || se.NeedsAdmin {
		t.Fatal("errors.As 应能取到 *StartError 及其 NeedsAdmin")
	}
}

// TestStartServiceAccessDeniedIsTyped 未提权时启动服务必须返回 NeedsAdmin，
// 这是 ensureMySQL 决定「是否改用 UAC 提权」的唯一依据。
func TestStartServiceAccessDeniedIsTyped(t *testing.T) {
	// MySQL80 通常需要管理员权限；但服务名不存在时会是别的错误。
	// 这里只断言错误能被识别为 *StartError，不假设具体服务存在。
	err := StartService("RFERP-不存在的服务-仅用于测试")
	if err == nil {
		t.Skip("该名称的服务恰好存在，跳过")
	}
	var se *StartError
	if !errors.As(err, &se) {
		t.Fatalf("StartService 应返回 *StartError，实际 %T: %v", err, err)
	}
}
