package api

import (
	"crypto/subtle"
	"io/fs"
	"net/http"
	"strings"
)

// 启动 token 门（阶段 A / Round 1 关口「启动 token」）。
//
// 内嵌服务只绑 127.0.0.1，但「只绑回环」挡不住同机的浏览器页面：用户若在
// WebView 里访问任意网站，该网站可以向 127.0.0.1:<端口> 发请求（CSRF /
// DNS rebinding）。启动 token 挡的就是这个。
//
// 形态：cmd/desktop-wails 的 AssetServer 把 WebView 的请求反向代理到内嵌服务，
// 代理在转发时补上启动 Cookie。因此：
//   - token 只存在于进程内存与代理内部，从不下发给浏览器，JS 读不到、也不落盘；
//   - 直接访问 127.0.0.1:<端口> 的同机进程没有该 Cookie，一律 403。
//
// 为什么不走 URL 握手：Wails v2 没有「让窗口导航到外部 URL」的选项，窗口始终
// 由 AssetServer 驱动，所以 ?boot=<token> → Set-Cookie → 跳转这条路在 Wails 下
// 无法成立。代理注入是等价且更紧的做法。
//
// 残余风险（已知）：能读取本进程内存的进程当然能拿到 token，这与浏览器同源模型
// 无法区分，属于可接受范围。
const startupCookieName = "rferp_boot"

// WithStartupToken 启用启动 token 门。
//
// cmd/server 不启用：它面向已就绪的服务器，认证走 Bearer + VPN，不假设调用方
// 与服务同机。阶段 A 的内嵌服务必须启用。
func WithStartupToken(tok string) Option {
	return func(s *Server) { s.startupToken = tok }
}

// StartupCookieName 供内嵌服务的反向代理补 Cookie 时使用。
const StartupCookieName = startupCookieName

// hasStartupToken 判断请求是否携带正确的启动凭据。常量时间比较，
// 避免按前缀提前返回泄露信息。
func (s *Server) hasStartupToken(r *http.Request) bool {
	if s.startupToken == "" {
		return true // 未启用网关：恒通过（cmd/server 的情形）
	}
	c, err := r.Cookie(startupCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.startupToken)) == 1
}

// withStartupGate 包住整个服务。
//
// 页面与 /api/ 一并受保护：只锁 API 会出现「页面进不去但接口能打」这种
// 前后端不一致的半可用状态，比直接拒绝更难排查。
func (s *Server) withStartupGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hasStartupToken(r) {
			writeErr(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// serveStatic 挂载前端构建产物。资源缺失时给出明确提示，
// 而不是让用户对着 404 猜是不是装坏了。
//
// fsys 支持两种来源：单 exe 发行用 go:embed，开发期用 os.DirFS("web/dist")。
// dir 是 fsys 内的前缀（嵌入式布局下资源位于 "dist/"）。
func serveStatic(fsys fs.FS, dir string) http.Handler {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeErr(w, http.StatusNotFound,
				"前端资源未随程序一起构建：请在 web/ 执行 npm run build 后重新构建本程序")
		})
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(sub, "index.html"); err != nil {
			writeErr(w, http.StatusNotFound,
				"前端资源未构建：请在 web/ 执行 npm run build 后重试")
			return
		}
		// 单页应用：未知路径回落到 index.html 交给前端路由。
		// 但 /api/ 前缀必须仍 404，不能被 SPA 回退吞掉——
		// 否则前端会把「接口不存在」渲染成一个空白页面。
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		if _, err := fs.Stat(sub, strings.TrimPrefix(r.URL.Path, "/")); err != nil {
			clone := r.Clone(r.Context())
			clone.URL.Path = "/"
			files.ServeHTTP(w, clone)
			return
		}
		files.ServeHTTP(w, r)
	})
}
