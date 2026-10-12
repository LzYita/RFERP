// 可热切换的 HTTP 后端。
//
// 首启配置页与正常业务接口共用同一个监听端口、同一个窗口，差别只在
// 「当前由谁处理请求」：未配置时是配置页，装配成功后换成真正的业务服务。
// 换完让 WebView 重载一次即可，全程不重启进程、不闪窗口。
//
// 用 atomic 而不是锁：切换只发生一次（首启），但切换发生时必然有并发请求
// 正在处理（用户刚点完保存，页面还在发轮询），锁会把这些请求排队到
// 切换之后才拿到新 handler，行为难以推理。atomic 读是无锁且始终拿到
// 一个自洽的 handler。

package main

import (
	"net/http"
	"sync/atomic"
)

// swappableBackend 是一个 http.Handler，其具体实现可在运行时替换。
type swappableBackend struct {
	h atomic.Pointer[http.Handler]
}

func newSwappableBackend(h http.Handler) *swappableBackend {
	b := &swappableBackend{}
	b.swap(h)
	return b
}

func (b *swappableBackend) swap(h http.Handler) { b.h.Store(&h) }

func (b *swappableBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	(*b.h.Load()).ServeHTTP(w, r)
}
