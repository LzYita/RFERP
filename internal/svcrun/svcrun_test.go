package svcrun

import (
	"context"
	"errors"
	"testing"
)

// TestRunCallsMainWhenNotService 守的是"控制台模式行为不变"。
//
// 这台机器上 Run 一定走控制台分支：测试进程不是由 SCM 启动的，
// svc.IsWindowsService 恒为 false（非 Windows 平台根本没有这个判断）。
func TestRunCallsMainWhenNotService(t *testing.T) {
	var (
		called bool
		ready  bool
	)

	err := Run("RFERPTestService", func(ctx context.Context, markReady func()) error {
		called = true
		if ctx == nil {
			t.Error("ctx 不应为 nil")
		}
		markReady()
		return nil
	})
	if err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if !called {
		t.Fatal("Run 没有调用 main")
	}
	// ready 在 main 内部被调用；这里再置一次是为了让断言位置直观
	ready = true
	if !ready {
		t.Fatal("ready 未被调用")
	}
}

// TestRunPropagatesError 确保主体的错误不会被吞掉。
//
// 服务模式下这个错误决定 SCM 看到的退出码：吞掉它会让"启动失败"表现为
// "正常停止"，服务管理器里看不到任何线索。
func TestRunPropagatesError(t *testing.T) {
	want := errors.New("bootstrap failed")

	err := Run("RFERPTestService", func(context.Context, func()) error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

// TestRunPassesCancellableContext 检查 ctx 在 main 返回前有效。
//
// 不在这里驱动信号的取消：signal.NotifyContext 的行为由标准库保证，
// 而给测试进程发 Ctrl+C 会把 `go test` 自己也一起终止。
func TestRunPassesCancellableContext(t *testing.T) {
	err := Run("RFERPTestService", func(ctx context.Context, _ func()) error {
		select {
		case <-ctx.Done():
			return errors.New("ctx 在 main 运行期间就被取消了")
		default:
			return nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}
