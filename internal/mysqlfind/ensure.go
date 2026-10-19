package mysqlfind

import (
	"errors"
	"fmt"
	"log"
	"net"
	"time"
)

// EnsureRunning 在 MySQL 看起来没在运行时尝试拉起本机服务。
//
// 只处理本机地址：远程库的启停不是客户端该管的事，也不该为此弹 UAC。
//
// 权限：Windows 下启动服务通常需要管理员。StartService 返回 NeedsAdmin 时改用
// StartServiceElevated 走 UAC —— 提权的只是"启动服务"这一个动作所对应的子进程
// （RunServiceStartIfRequested），不是整个应用。正常使用时（库已在跑）端口是通的，
// 这里直接返回，不会出现任何 UAC 提示。
//
// 返回值只表示"有没有成功拉起并等到端口就绪"；调用方仍应继续尝试连接 ——
// 端口通不等于账号/库名对。
func EnsureRunning(host, port, service string) error {
	if !isLocalHost(host) {
		return nil
	}
	addr := net.JoinHostPort(host, port)
	if portOpen(addr) {
		return nil
	}
	if service == "" {
		return errors.New("未配置 MySQL 服务名")
	}

	log.Printf("MySQL 未运行，尝试启动服务 %s...", service)
	if err := StartService(service); err != nil {
		// 权限不足：改用 UAC 提权启动（会弹一次用户确认）。
		var se *StartError
		if errors.As(err, &se) && se.NeedsAdmin {
			log.Printf("启动 MySQL 服务需要管理员权限，改用 UAC 提权：%v", err)
			if err2 := StartServiceElevated(service); err2 != nil {
				return err2
			}
		} else {
			return err
		}
	}

	// 等待 MySQL 就绪：服务"已启动"到真正监听端口之间有一段空档。
	for i := 0; i < 30; i++ {
		if portOpen(addr) {
			log.Println("MySQL 已就绪")
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("等待 MySQL 就绪超时（%s）", addr)
}

func portOpen(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func isLocalHost(host string) bool {
	switch host {
	case "", "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}
