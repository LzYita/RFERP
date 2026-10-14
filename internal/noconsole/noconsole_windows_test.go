//go:build windows

package noconsole

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestHideSetsCreateNoWindow(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "exit")
	Hide(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr 仍为 nil：Hide 没有生效")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Errorf("CreationFlags = %#x，缺少 CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
}

func TestHideKeepsExistingFlags(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "exit")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
	Hide(cmd)

	if !cmd.SysProcAttr.HideWindow {
		t.Error("Hide 覆盖了调用方已有的 HideWindow")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Errorf("Hide 覆盖了调用方已有的 CreationFlags：%#x", cmd.SysProcAttr.CreationFlags)
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Error("Hide 没有补上 CREATE_NO_WINDOW")
	}
}

func TestHideIsIdempotentAndNilSafe(t *testing.T) {
	Hide(nil) // 不得 panic

	cmd := exec.Command("cmd", "/c", "exit")
	Hide(cmd)
	Hide(cmd)
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Error("重复调用后标志丢失")
	}
}

// Hide 的全部意义在于「窗口不出现」，而这一条同时守住另一半：子进程仍要
// 正常跑起来并把 stdout 交回调用方。把标志设错（例如传给不存在的位）时，
// CreateProcess 会直接失败，这里就会红。
func TestHiddenCommandStillRunsAndCapturesStdout(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "echo noconsole-ok")
	Hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("带 CREATE_NO_WINDOW 的子进程执行失败：%v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "noconsole-ok" {
		t.Errorf("stdout = %q，期望 %q", got, "noconsole-ok")
	}
}
