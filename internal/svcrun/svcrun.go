// Package svcrun 让同一个入口既能当控制台程序跑，也能当 Windows 服务跑。
//
// 存在的理由：交付包原先用 WinSW 把 RFERP-server.exe 包成服务。WinSW 是
// 第三方未签名二进制（约 18MB），从 GitHub 加速器下载、只校验 MZ 头，属于
// 不必要的供应链面；而它提供的四个能力（开机自启、失败重启、日志轮转、
// 低权限账号）在 Windows 上都有系统级替代，日志轮转还与 internal/logging 重复。
//
// 换成原生实现后，SCM（服务控制管理器）直接启动 RFERP-server.exe，
// 交付包里不再有第三方 exe。
//
// 关键约束：**启动过程必须在 SCM 的启动窗口内报到**。Bootstrapping（连库、
// 执行迁移、升级前备份）可能持续几十秒到几分钟，因此不能在 svc.Run 之前做——
// 那样 SCM 会在约 30 秒后以错误 1053（未及时响应启动请求）中止服务，
// 而日志里看不到任何异常。本包的做法是：先报 StartPending 并给出 WaitHint，
// 再在后台执行主体，监听成功后才报 Running。
package svcrun

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
)

// Main 是服务主体。
//
// ready 表示「已经可以对外服务」——服务模式下据此向 SCM 报 Running，
// 调用之前 SCM 看到的是 StartPending。它必须且只会被调用一次；
// 主体在 ready 之前返回错误，会被当作「启动失败」而不是「运行结束」。
type Main func(ctx context.Context, ready func()) error

// Run 按当前进程的启动方式运行 main。
//
// 由 SCM 启动 → 作为 Windows 服务运行，ctx 在收到停止/关机请求时取消；
// 否则 → 按控制台程序运行，ctx 在 Ctrl+C / SIGTERM 时取消。
//
// 两种情况对外行为一致：ctx 取消后 main 应尽快优雅退出。
func Run(name string, main Main) error {
	if handled, err := runAsService(name, main); handled {
		return err
	}
	return runConsole(main)
}

// runConsole 按普通控制台程序运行，处理 Ctrl+C / SIGTERM。
//
// 以前这段逻辑写在 cmd/server/main.go 里；挪到这里之后，控制台模式与服务模式
// 共用同一个 main 签名，"两份入口逻辑漂移"的风险就没了。
func runConsole(main Main) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return main(ctx, func() {
		// 控制台模式没有 SCM 需要通知；留空而不是传 nil，让调用方不必判空。
		log.Printf("svcrun: 已就绪（控制台模式）")
	})
}
