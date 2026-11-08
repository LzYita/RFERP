//go:build windows

package svcrun

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
)

const (
	// startWaitHint 是「启动中」允许持续多久。
	//
	// 必须给足：主体里包含连库、执行迁移、以及有 pending 步骤时的升级前备份
	// （mysqldump 在大库上能跑几分钟）。WaitHint 太短会让 SCM 判定服务
	// 未及时响应并以 1053 中止启动——而那份备份其实马上就要成功了。
	startWaitHint = 300 * time.Second

	// stopWaitHint 是「停止中」允许持续多久。服务端优雅关闭自身上限是 10 秒，
	// 留 30 秒覆给在途请求与 SQLite WAL 落盘。
	stopWaitHint = 30 * time.Second

	// eventSourceID 用于写事件日志的标识（写入时只用 ID 做分类）。
	eventSourceID = 1
)

// runAsService 判断当前进程是否由 SCM 启动；是则按服务运行。
//
// 返回值 handled 表示"这次启动算不算服务模式"，与 err 分开是因为
// 调用方需要区分"不是服务，走控制台"和"是服务但启动失败"。
func runAsService(name string, main Main) (handled bool, err error) {
	isService, ierr := svc.IsWindowsService()
	if ierr != nil {
		// 判断失败不应让程序起不来：按控制台跑，行为可预期。
		log.Printf("svcrun: 判断是否为服务进程失败（按控制台模式运行）: %v", ierr)
		return false, nil
	}
	if !isService {
		return false, nil
	}
	return true, svc.Run(name, &handler{name: name, main: main})
}

// handler 实现 svc.Handler。
type handler struct {
	name string
	main Main

	el     *eventlog.Log
	elOnce sync.Once
}

// eventf 把一条消息同时写进日志文件与 Windows 事件日志。
//
// 为什么要事件日志：WinSW 会把子进程的 stdout/stderr 收进 logs 目录，
// 换成原生服务之后，SCM 启动的进程没有控制台，stdout 直接丢弃。服务在
// 迁移阶段失败时，客户在「服务」管理器里只看到"服务无法启动"，看不到原因。
// 写事件日志之后，事件查看器 → Windows 日志 → 应用程序 里能直接读到。
func (h *handler) eventf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(msg)

	h.elOnce.Do(func() {
		// 事件源由安装脚本注册（New-EventLog）。没注册时 Open 失败，
		// 静默降级到文件日志——不能因为"写不了事件日志"而让服务起不来。
		el, err := eventlog.Open(h.name)
		if err != nil {
			return
		}
		h.el = el
	})
	if h.el != nil {
		// 写失败同样只忽略：日志文件里已经有同一条。
		_ = h.el.Warning(eventSourceID, msg)
	}
}

func (h *handler) close() {
	if h.el != nil {
		_ = h.el.Close()
	}
}

// Execute 是 SCM 的入口：报到状态、转发动机、等主体退出。
func (h *handler) Execute(_ []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	// Accepts 里不能写 "AcceptInterrogate"——svc 包没有这个常量。
	// 询问（Interrogate）是 SCM 的隐式要求：任何服务都必须随时能回答当前状态，
	// 所以只能在 Execute 里处理 svc.Interrogate 请求，不能"声明接受"。
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	defer h.close()

	// 先报到：SCM 只有在收到状态变化后才认为进程"活着"。
	current := svc.Status{
		State:    svc.StartPending,
		WaitHint: uint32(startWaitHint / time.Millisecond),
	}
	status <- current

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var readyOnce sync.Once
	readyCh := make(chan struct{})
	ready := func() { readyOnce.Do(func() { close(readyCh) }) }

	done := make(chan error, 1)
	go func() { done <- h.main(ctx, ready) }()

	running := false
	for {
		select {
		case <-readyCh:
			if !running {
				running = true
				current = svc.Status{State: svc.Running, Accepts: accepted}
				status <- current
				h.eventf("RFERP-server 已就绪")
			}

		case err := <-done:
			if !running {
				// 还没就绪就退出 = 启动失败（配置错、库连不上、迁移失败……）。
				// 原因写进事件日志，否则客户只看到"服务无法启动"。
				h.eventf("启动失败：%v", err)
				return true, 1
			}
			if err != nil {
				h.eventf("运行异常结束：%v", err)
				return true, 1
			}
			h.eventf("已正常退出")
			return false, 0

		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				status <- current

			case svc.Stop, svc.Shutdown:
				h.eventf("收到停止请求，开始优雅关闭")
				current = svc.Status{
					State:    svc.StopPending,
					WaitHint: uint32(stopWaitHint / time.Millisecond),
				}
				status <- current
				cancel()

				select {
				case err := <-done:
					if err != nil {
						h.eventf("关闭过程中出错：%v", err)
						return true, 1
					}
					return false, 0
				case <-time.After(stopWaitHint):
					// 超时不能返回 0：那样 SCM 会认为"干净停止"，
					// 掩盖掉一次真正的卡死。
					h.eventf("优雅关闭超时（%s），强制结束", stopWaitHint)
					return true, 1
				}
			}
		}
	}
}
