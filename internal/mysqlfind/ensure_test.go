package mysqlfind

import (
	"net"
	"testing"
	"time"
)

// EnsureRunning 的两条短路必须成立，否则每次启动都会去碰服务管理器：
//   - 远程库不是客户端该启停的东西；
//   - 端口已通就说明库在跑，不该弹 UAC。
//
// 这里只测短路：真正去启动服务会动本机 SCM，不属于单元测试的范围。

func TestEnsureRunningSkipsRemoteHost(t *testing.T) {
	for _, host := range []string{"192.168.1.10", "db.internal", "10.0.0.5"} {
		if err := EnsureRunning(host, "3306", "MySQL80"); err != nil {
			t.Errorf("远程库 %s 不该报错（也不该尝试启动服务）: %v", host, err)
		}
	}
}

// 端口通时必须立刻返回，不能等 30 秒、更不能去启动服务。
func TestEnsureRunningReturnsFastWhenPortOpen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- EnsureRunning("127.0.0.1", port, "") }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("端口已通时不该报错: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("端口已通时 EnsureRunning 没立刻返回 —— 每次启动都会先卡一下")
	}
}
