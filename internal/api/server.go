// Package api 提供 HTTP 应用入口（Phase C）。
// 鉴权与操作人来自服务端会话，不信任客户端传入的 operator。
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"app/internal/auth"
	"app/internal/model"
	"app/internal/update"
	"app/internal/usecase"
)

// Server 暴露同一套用例的 HTTP 面（C1–C3）。
type Server struct {
	restoreRequiresRestart atomic.Bool
	apps                   usecase.Applications

	// desc 供 /api/v1/serverinfo 使用。默认与 apps 相同，单独成字段是为了让该
	// 端点只依赖 ServerDescriptor —— 便于测试替换，也明确它不碰业务数据。
	desc usecase.ServerDescriptor

	// version 由进程注入（ldflags），用于 /api/v1/serverinfo 自报版本（D-016）。
	version string

	mu       sync.Mutex
	sessions map[string]*session

	// remember 保存「记住登录」凭证，与普通会话分开管理。
	remember *rememberStore

	// rememberEnabled 把「记住登录」限制在**单机桌面形态**。
	//
	// 原因：handleLoadRemember 会把明文长期凭证返回给调用方，而这几个端点
	// 刻意不挂 s.auth（使用时用户还没有会话）。它们的安全边界只有启动 token
	// 网关——即"同机其它程序拿不到 Cookie"。
	//
	// cmd/server 监听 :8080 全网卡、给多人 MySQL 形态用，且**没有**启动 token。
	// 若在那里挂载 remember，任何能连到端口的人都能取走本机记住的凭证，
	// 无需密码直接冒用该用户。所以它只能由桌面入口显式开启。
	rememberEnabled bool

	// rememberFile 覆盖凭证落盘路径；空值表示用 %AppData%\RFERP\remember.bin。
	rememberFile string

	// startupToken 非空时启用启动 token 门（阶段 A 内嵌服务）。
	// cmd/server 留空，走 Bearer + 网络边界。
	startupToken string

	// uiReady 在前端首次挂载成功时被调用一次，为 nil 表示没人关心。
	// 更新后的回退判据靠它：界面显示不出来就不算这一版健康。
	uiReady func()

	// updater 承载软件更新。url 为空表示未配置更新源，此时不检查、不提示、
	// 也不发任何网络请求（本地部署不需要联网）。
	updater updateChecker

	// staticFS/staticDir 非空时挂载前端构建产物（阶段 A 内嵌服务用它同时提供 API 与页面）。
	staticFS  fs.FS
	staticDir string

	// hostOperations 标记「本机形态」，据此放行整库恢复 / 清空业务数据 / 修改数据目录。
	//
	// 缺省 false，即**默认不放行**。见 WithHostOperations 的理由：这几项在
	// 阶段 B/C 应当只对 server 所在机器开放，而漏传 Option 的后果必须是不放行。
	hostOperations bool

	// openFolder 是「在文件管理器里打开某个目录」的实际动作，默认调 explorer.exe。
	//
	// 做成可注入是因为它**有真实副作用**：会在开发者桌面上弹窗。测试注入一个
	// 只记录不执行的实现，于是跑测试不再开窗口、CI 上也不会去启动 GUI 程序，
	// 而且顺带能断言"传给启动器的到底是哪个目录"。
	openFolder func(dir string) error

	// applyUpdate 是「用更新包替换并重启自己」的实际动作，默认 update.ApplyAndRestart。
	//
	// 更要紧的是可注入：真实实现会**替换正在运行的测试进程并启动它**。
	// 测试绝不能走真实路径。
	applyUpdate func(zipPath string) error

	// exitProcess 是 apply 成功之后退出旧进程的动作，默认 exitProcessNow。
	//
	// 可注入的理由同上，且更直白：真实实现会把**跑测试的进程**直接杀掉。
	// WithProcessExit 只供测试使用。
	exitProcess func()

	// jobs 是"异步下载"的任务表，给界面显示进度用（见 update.go 的说明）。
	// 生命周期极短：一次下载，外加完成后 5 分钟的容错窗口。
	jobs   map[string]*downloadJob
	jobsMu sync.Mutex
	jobSeq int
	// jobSeqMu 只护 jobSeq（id 要单调递增，避免同毫秒内两次请求撞 id）。
	jobSeqMu sync.Mutex

	// newChecker 是「构造更新检查器」的方式，默认 update.NewChecker。
	//
	// 可注入的原因：真实实现给检查器安了**线上 GitHub** 作回退地址，于是"配置源失败"
	// 的测试会去打外网（本机不通 → 卡 60 秒；CI 通 → 结果又和本地不同）。注入后
	// 两条源都能指向本地，确定且离线。仅测试使用（WithUpdateChecker）。
	newChecker func(url, current string) (*update.Checker, error)

	// pendingUpdate 保存**下载步骤刚产生并通过校验**的更新包。
	//
	// apply 只认这一个 —— 客户端不能再指认任意路径。此前 apply 直接接受请求体里的
	// path 且不做校验，等于给了任何已登录会话一个「任意路径 → 取出 exe → 覆盖程序
	// 并运行」的原语（当时这条路由挂在 System.ApplyUpdate 上，2026-10-15 按产品决定
	// 对所有登录角色开放写入，于是这条"路径不可指认"的约束更重要了）。
	//
	// 存 sha 是为了在 apply 之前**再核对一次**：下载校验的是那个临时文件，
	// 而下载与 apply 之间隔着一次 HTTP 往返和一次客户端决策，中间文件可能被换掉。
	pendingUpdate struct {
		mu   sync.Mutex
		path string
		sha  string
	}

	// picker 由桌面宿主注入，负责弹**原生**目录选择框。为 nil 表示当前部署形态
	// 没有可弹框的窗口（cmd/server、-serve 之外的纯 HTTP 访问、浏览器）。
	//
	// 与 hostOperations 分开：能不能做（权限与形态）与"用什么方式做"是两件事。
	// 没有 picker 时数据目录仍可改，只是要走文本输入。
	picker DirectoryPicker

	// bootstrapMu 串行化"首次建管理员"的计数+创建，
	// 避免并发请求同时看到 0 用户而建出两个初始管理员。
	bootstrapMu sync.Mutex

	// readiness 是 /readyz 的探针：返回 nil 表示数据库可用。
	// 为 nil 表示当前形态没有可探的依赖，端点回 200（不装死）。
	readiness func() error

	// deviceGuard 是局域网版的登录门禁（设备登记 / 授权额度 / 会话绑定）。
	// 为 nil 表示未启用（单机桌面形态），所有设备头与授权检查一律跳过。
	deviceGuard DeviceGuard

	// bootstrapToken 非空时，POST /api/bootstrap-admin 必须带匹配的
	// X-Bootstrap-Token。桌面单机形态不设，保持首启即用。
	bootstrapToken string

	// minClientVersion 是服务端要求的最低客户端版本（FR-15）。
	// 空表示不设门禁：单机形态没有"客户端版本"这回事。
	minClientVersion string

	// lanConnect 只在「客户端模式」的桌面外壳注入：让用户在登录前
	// 就能改主机地址（FR-17）。返回归一化后的地址。
	lanConnect func(rawURL string) (string, error)
}

// DeviceGuard 是 LAN 形态的准入接口，由 internal/service 实现。
//
// 类型定义在 usecase：api 与 service 都要引用它，谁都不能 import 对方。
// 这里只做别名，让 api 内部写法短一些。
type DeviceGuard = usecase.DeviceGuard

// DeviceLogin / DeviceLoginResult / DeviceRuntime 同为 usecase 的别名。
type (
	DeviceLogin       = usecase.DeviceLogin
	DeviceLoginResult = usecase.DeviceLoginResult
	DeviceRuntime     = usecase.DeviceRuntime
)

// DirectoryPicker 由宿主实现的原生目录选择框。
//
// 为什么要绕这一圈、而不是让前端直接调 Wails 绑定：web/ 是**宿主无关**的——
// 同一份页面要能跑在 vite dev、纯浏览器（自动化关口）与内嵌服务里，
// 它全程不引用 window.go / wailsjs。原生选择框只能由宿主进程弹出，
// 所以能力从宿主注入、服务端裁决，前端只发一个普通 HTTP 请求，
// 架构边界与现有的 WithHostOperations 完全一致。
//
// 返回空字符串表示用户取消（不是错误）。
type DirectoryPicker interface {
	PickDirectory(current string) (string, error)
}

// DirectoryPickerFunc 让普通函数直接满足 DirectoryPicker。
type DirectoryPickerFunc func(current string) (string, error)

func (f DirectoryPickerFunc) PickDirectory(current string) (string, error) { return f(current) }

// WithDirectoryPicker 注入宿主的原生目录选择框。
//
// 只有 cmd/desktop-wails 该传——只有它持有可弹框的 Wails 窗口。cmd/server 不传，
// 于是那条端点会明确告诉前端"这里弹不了，请手填"，而不是静默失败。
func WithDirectoryPicker(p DirectoryPicker) Option {
	return func(s *Server) { s.picker = p }
}

// Option 用于可选装配（保持 New 的旧调用点不变）。
type Option func(*Server)

// WithVersion 让服务端在 /api/v1/serverinfo 里自报版本。
func WithVersion(v string) Option {
	return func(s *Server) { s.version = v }
}

// WithReadiness 注入 /readyz 的依赖探针（服务端传数据库 Ping）。
//
// 与 /healthz 分开的理由是它们回答不同的问题：healthz 回答"进程还在吗"，
// readyz 回答"能不能干活"。数据库文件被占用、磁盘只读时进程照样活，
// 而 Windows 服务包装器与安装器需要的是后一个答案。
func WithReadiness(f func() error) Option {
	return func(s *Server) { s.readiness = f }
}

// WithDeviceGuard 启用局域网版的设备与授权门禁。
//
// 只有「服务器形态」该传：单机桌面没有设备概念，硬加一层会让首启多出
// 一个必然失败的检查。传 nil 与不传等价。
func WithDeviceGuard(g DeviceGuard) Option {
	return func(s *Server) { s.deviceGuard = g }
}

// WithMinClientVersion 设置客户端最低版本（服务端形态）。
//
// 低于它的客户端在登录与每个已认证请求上都会收到 426：版本不兼容时
// 让新接口"用着用着报奇怪的错"，比直接拒绝升级要难查得多（R12）。
func WithMinClientVersion(v string) Option {
	return func(s *Server) { s.minClientVersion = strings.TrimSpace(v) }
}

// WithLANConnect 注入"切换局域网主机地址"的动作（仅客户端模式）。
func WithLANConnect(f func(rawURL string) (string, error)) Option {
	return func(s *Server) { s.lanConnect = f }
}

// WithBootstrapToken 要求首建管理员时携带 X-Bootstrap-Token。
//
// 空串表示不设门（单机桌面）。服务端必须在启动时生成并展示令牌：
// 空库第一次启动的那几分钟是对整个局域网敞开的，没有令牌任何人都能
// 抢先把管理员建掉（FR-41 / NFR-02）。
func WithBootstrapToken(tok string) Option {
	return func(s *Server) { s.bootstrapToken = tok }
}

// WithStaticFS 挂载前端构建产物。
// 单 exe 发行传 go:embed 得到的 fs.FS；开发期传 os.DirFS("web/dist")。
// dir 是资源在 fsys 内的前缀（嵌入式布局下为 "dist"）。
func WithStaticFS(fsys fs.FS, dir string) Option {
	return func(s *Server) {
		s.staticFS = fsys
		s.staticDir = dir
	}
}

// WithRemember 开启「记住登录」，并指定凭证落盘路径。
//
// 传空路径表示用默认位置（%AppData%\RFERP\remember.bin）。
// 只有 cmd/desktop-wails（单机桌面形态，带启动 token 网关）才应该调用它；
// cmd/server 必须保持关闭——见 Server.rememberEnabled 的说明。
func WithRemember(path string) Option {
	return func(s *Server) {
		s.rememberEnabled = true
		s.rememberFile = path
	}
}

// WithUpdate 启用软件更新。url 为空（默认）表示不启用。
//
// 只有桌面入口该传：更新会替换用户正在运行的 exe，这是单机行为，
// 不该在 cmd/server 上被触发。
func WithUpdate(url, currentVersion string) Option {
	return func(s *Server) {
		s.updater.url = url
		s.updater.cur = currentVersion
	}
}

// WithHostOperations 开启「只有本机才能做」的破坏性操作：整库恢复、清空业务数据、
// 修改数据目录。
//
// 依据 §4.5 本地/远程能力矩阵（wails-migration.md:750-769）：这些能力在阶段 B/C
// （桌面客户端/浏览器 → server）应当只对 server 所在机器开放。原话是"跨机后这一
// 前提不成立，四道门只剩仪式"——确认门建立在"操作者就是这台机器的主人"之上。
//
// 为什么是显式 Option 而不判断 cfg.Mode / 是否有 startupToken：
//   - cfg.Mode 只被 cmd/desktop（Fyne 桌面）读取，cmd/server 与 cmd/desktop-wails
//     都不读，拿它当守卫等于三处行为不一致；
//   - startupToken 语义是"同机 UI 门禁"，且 -serve 无头模式不带它，用它会把开发
//     模式误判成跨机，导致破坏性按钮在本地永远验证不了。
//
// 显式开关与 WithUpdate / WithRemember 完全同构：那两处也是"单机能力不该在
// cmd/server 上触发"，写法与评审口径都已定过。
//
// **默认值是 false（关闭）** —— 漏传就是不放行，方向是安全的。
func WithHostOperations() Option {
	return func(s *Server) { s.hostOperations = true }
}

// WithFolderOpener 替换「打开文件夹」的实际动作。仅供测试与将来的无头形态使用；
// 不传就用 explorer.exe。
func WithFolderOpener(f func(dir string) error) Option {
	return func(s *Server) { s.openFolder = f }
}

// WithUpdateApplier 替换「用更新包替换并重启自己」的实际动作。**仅供测试**：
// 真实实现会替换正在运行的进程，跑测试时用它会把自己的测试二进制换掉。
func WithUpdateApplier(f func(zipPath string) error) Option {
	return func(s *Server) { s.applyUpdate = f }
}

// WithProcessExit 替换「apply 成功之后退出旧进程」的动作。**仅供测试**：
// 默认实现是 os.Exit，会把跑测试的进程直接杀掉。
func WithProcessExit(f func()) Option {
	return func(s *Server) { s.exitProcess = f }
}

// WithUpdateChecker 替换「构造更新检查器」的方式。**仅供测试**：
// 真实实现（update.NewChecker）会把内置回退地址设为**线上 GitHub**，
// 于是任何"配置源失败"的用例都会去打外网 —— 本机 github.com 不通，一卡就是 60 秒；
// CI 上通了，也会因公钥对不上而得到与本地不同的结果。注入后两条源都能指向本地，
// 结果确定、不依赖网络。
func WithUpdateChecker(f func(url, current string) (*update.Checker, error)) Option {
	return func(s *Server) { s.newChecker = f }
}

// exitProcessNow 是 apply 成功之后结束旧进程的默认实现。
//
// 退出码 0 —— 这是"正常重启"不是"出错退出"，Fyne 版同一位置也是 os.Exit(0)。
//
// 写成具名函数而不是在 New 里包一层闭包：闭包的函数名是 New.func1，测试无法区分
// "默认实现"和"注入的假实现"，而这条默认值一旦被漏掉，缺陷⑥会静默复现。
func exitProcessNow() { os.Exit(0) }

// New 用给定用例实现构造 API。
func New(apps usecase.Applications, opts ...Option) *Server {
	s := &Server{apps: apps, desc: apps, sessions: make(map[string]*session), remember: newRememberStore("")}
	for _, opt := range opts {
		opt(s)
	}
	if remote, ok := apps.(interface{ RemoteDeployment() bool }); ok && remote.RemoteDeployment() {
		s.rememberEnabled = false
	}
	// WithRemember 才能决定凭证存哪，所以要在 opts 之后才建 store。
	s.remember.file = s.rememberFile
	if s.jobs == nil {
		s.jobs = make(map[string]*downloadJob)
	}
	// 同理：打开文件夹的实现必须在 opts 之后兜底，否则漏传就会 nil panic，
	// 而"漏传"在这里等价于"一点就崩"。
	if s.openFolder == nil {
		s.openFolder = explorerFolderOpener
	}
	if s.applyUpdate == nil {
		s.applyUpdate = update.ApplyAndRestart
	}
	if s.exitProcess == nil {
		s.exitProcess = exitProcessNow
	}
	// 把磁盘上的记住凭证登记回内存表，否则「记住登录」跨启动必然失效。
	// 必须在 opts 之后：开不开、存哪，都由 WithRemember 决定。
	if s.rememberEnabled {
		s.restoreRemember()
	}
	return s
}

// Handler 返回根路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	// readyz 检查依赖（数据库）。healthz 只看进程，安装器与服务监控要看的是
	// 「现在能不能干活」——两者混用会把"数据库被占用"误报成健康。
	mux.HandleFunc("GET /readyz", s.handleReady)
	// 前端挂载成功后报到一次。更新后的回退判据是"界面能用"而不是"进程活着"，
	// 而只有前端知道这件事 —— 资源坏掉时窗口照开、进程照活，用户只看到空白。
	// 挂在启动凭据闸门内即可，不必登录：报到发生在首屏，那时还没有会话。
	mux.HandleFunc("POST /api/startup/ui-ready", s.handleUIReady)
	// 未登录可读的服务端自描述（D-016）：供"接入服务器"预检使用。
	// 只暴露版本与数据库身份，不含任何业务数据。
	mux.HandleFunc("GET /api/v1/serverinfo", s.handleServerInfo)
	// 软件更新：查 → 下载 → 应用，三步分开。
	// 「应用」会替换正在运行的 exe，必须带 confirm；不配置更新源时这三个端点
	// 仍可调用，但一律返回 enabled:false / 400，不发任何网络请求。
	mux.HandleFunc("GET /api/update/check", s.handleCheckUpdate)
	// download/apply 只要求**已登录**，不按角色区分（2026-10-15，产品决定：
	// 对齐 Fyne 旧版——旧版的启动检查对任何登录用户都会弹框）。
	//
	// 为什么这不是权限绕过：能不能换掉程序，不由角色决定，而由三道与角色无关的
	// 硬闸门决定——
	//  1. 更新包必须带**生产私钥**的 ed25519 签名，且文件哈希与清单一致
	//     （下载时核一次，apply 之前再核一次）；
	//  2. apply 只认"本次下载刚校验通过"的那个临时文件，请求体里的 path 一律忽略；
	//  3. apply 必须带 confirm:true。
	//
	// 换句话说：任何登录角色能触发的只是"去装一个**只有生产私钥签得出**的包"，
	// 不能让他装任意代码。剩下的取舍是可用性取舍（任何人都可能在他录入数据时
	// 发起一次更新并重启程序），这正是本次产品决定接受的那一份。
	// 进度查询（给界面显示"正在下载 x%"）：`POST /api/update/download?async=1`
	// 立刻回 202 与 job id，下载在后台跑，`GET /api/update/progress?job=<id>` 轮询。
	// 不带 `async=1` 时仍是同步模式（下载完才响应），给脚本与测试用。
	mux.HandleFunc("POST /api/update/download", s.auth(s.handleDownloadUpdate, ""))
	mux.HandleFunc("GET /api/update/progress", s.auth(s.handleUpdateProgress, ""))
	mux.HandleFunc("POST /api/update/apply", s.auth(s.handleApplyUpdate, ""))
	mux.HandleFunc("POST /api/login", s.handleLogin)
	// 空库首次部署：仅当无任何用户时可建初始管理员。
	mux.HandleFunc("POST /api/bootstrap-admin", s.handleBootstrapAdmin)
	// 首启状态：客户端连接向导在登录前先问一句"这台主机初始化了吗"。
	mux.HandleFunc("GET /api/setup/status", s.handleSetupStatus)
	// 未登录可读用户数，供首启判断（仅 count）。
	mux.HandleFunc("GET /api/users/count", s.handleUserCount)
	mux.HandleFunc("GET /api/me", s.auth(s.handleMe, ""))
	// 当前用户的模块权限：前端导航据此显示，不在前端另抄一份权限矩阵。
	mux.HandleFunc("GET /api/me/permissions", s.auth(s.handleMyPermissions, ""))
	// 内置离线帮助（设置屏：使用指南 / 常见问题 / 更新说明）。
	//
	// 内容随二进制嵌入、不含任何业务数据，但仍然要求登录：它属于登录后的界面，
	// 没有对匿名开放的理由。用空权限点而不是新加权限点——帮助对四个角色一视同仁，
	// 为它引入一个模块档位只会让权限矩阵多出四格全「读」的噪声。
	mux.HandleFunc("GET /api/help", s.auth(s.handleHelp, ""))

	// 记住登录。注意这三个端点**不**挂 s.auth：它们在还没有用户会话时使用，
	// 安全边界是启动 token 网关（同机其它程序拿不到 Cookie）。
	//
	// 因此仅在 WithRemember 开启时挂载：cmd/server 无启动 token，挂上去等于
	// 把明文长期凭证开放给网络上任何客户端。
	if s.rememberEnabled {
		mux.HandleFunc("POST /api/remember/save", s.handleSaveRemember)
		mux.HandleFunc("GET /api/remember/load", s.handleLoadRemember)
		mux.HandleFunc("POST /api/remember/forget", s.handleForgetRemember)
		mux.HandleFunc("POST /api/remember/exchange", s.handleExchangeRemember)
	} else if remote, ok := s.apps.(interface{ RemoteDeployment() bool }); ok && remote.RemoteDeployment() {
		mux.HandleFunc("GET /api/remember/load", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"token": nil, "enabled": false})
		})
	}
	// 登出：作废服务端会话条目。缺这个端点时客户端只清本地 token，
	// 旧 token 在服务端仍然有效。
	mux.HandleFunc("DELETE /api/session", s.auth(s.handleLogout, ""))
	mux.HandleFunc("GET /api/parts", s.auth(s.handleListParts, "Catalog.ListParts"))
	mux.HandleFunc("POST /api/parts/stock-in", s.auth(s.handleStockIn, "Inventory.StockIn"))
	mux.HandleFunc("POST /api/parts/adjust-stock", s.auth(s.handleAdjustStock, "Inventory.AdjustStock"))
	mux.HandleFunc("POST /api/parts/adjust-inbounds", s.auth(s.handleAdjustInbounds, "Inventory.AdjustInbound"))
	mux.HandleFunc("GET /api/parts/inbounds", s.auth(s.handleListPartInbounds, "Inventory.PartInbounds"))
	mux.HandleFunc("GET /api/batches", s.auth(s.handleListBatches, "Production.ListBatches"))
	mux.HandleFunc("POST /api/batches/status", s.auth(s.handleUpdateBatchStatus, "Production.UpdateBatchStatus"))
	mux.HandleFunc("GET /api/audit/recent", s.auth(s.handleRecentAudit, "Audit.ListRecentAuditLogs"))
	mux.HandleFunc("GET /api/users", s.auth(s.handleListUsers, "Identity.ListUsers"))
	mux.HandleFunc("POST /api/users", s.auth(s.handleCreateUser, "Identity.CreateUser"))
	mux.HandleFunc("PUT /api/users", s.auth(s.handleUpdateUser, "Identity.UpdateUser"))
	mux.HandleFunc("DELETE /api/users", s.auth(s.handleDeleteUser, "Identity.DeleteUser"))
	mux.HandleFunc("POST /api/users/reset-password", s.auth(s.handleResetPassword, "Identity.ResetPassword"))
	// 改自己的密码：登录即可，处理器内部再校验只能改自己。
	mux.HandleFunc("POST /api/users/change-password", s.auth(s.handleChangePassword, ""))
	mux.HandleFunc("GET /api/products", s.auth(s.handleListProducts, "Catalog.ListProducts"))
	mux.HandleFunc("GET /api/products/get", s.auth(s.handleGetProduct, "Catalog.GetProduct"))
	mux.HandleFunc("POST /api/products", s.auth(s.handleCreateProduct, "Catalog.CreateProduct"))
	mux.HandleFunc("PUT /api/products", s.auth(s.handleUpdateProduct, "Catalog.UpdateProduct"))
	mux.HandleFunc("DELETE /api/products", s.auth(s.handleDeleteProduct, "Catalog.DeleteProduct"))
	mux.HandleFunc("GET /api/parts/get", s.auth(s.handleGetPart, "Catalog.GetPart"))
	mux.HandleFunc("POST /api/parts", s.auth(s.handleCreatePart, "Catalog.CreatePart"))
	mux.HandleFunc("PUT /api/parts", s.auth(s.handleUpdatePart, "Catalog.UpdatePart"))
	mux.HandleFunc("DELETE /api/parts", s.auth(s.handleDeletePart, "Catalog.DeletePart"))
	mux.HandleFunc("GET /api/bom", s.auth(s.handleListBOM, "Catalog.GetBOMByProduct"))
	mux.HandleFunc("POST /api/bom", s.auth(s.handleAddBOM, "Catalog.AddBOMItem"))
	mux.HandleFunc("PUT /api/bom", s.auth(s.handleUpdateBOM, "Catalog.UpdateBOMItem"))
	mux.HandleFunc("DELETE /api/bom", s.auth(s.handleRemoveBOM, "Catalog.RemoveBOMItem"))
	mux.HandleFunc("GET /api/bom/validate", s.auth(s.handleValidateBOM, "Catalog.ValidateBOM"))
	// 库存投影是只读派生值，四个角色都需要看（生产员排产、仓库员备料），
	// 因此挂在「零件可读」上，不新增权限点。
	mux.HandleFunc("GET /api/stock/projection", s.auth(s.handleStockProjection, "Catalog.ListParts"))
	mux.HandleFunc("POST /api/batches", s.auth(s.handleCreateBatch, "Production.CreateBatch"))
	mux.HandleFunc("POST /api/batches/revoke", s.auth(s.handleRevokeBatch, "Production.RevokeBatch"))
	mux.HandleFunc("GET /api/batches/skipped", s.auth(s.handleSkippedParts, "Production.GetSkippedParts"))
	mux.HandleFunc("POST /api/batches/skip", s.auth(s.handleAddSkip, "Production.AddSkipPart"))
	mux.HandleFunc("DELETE /api/batches/skip", s.auth(s.handleRemoveSkip, "Production.RemoveSkipPart"))
	mux.HandleFunc("GET /api/batches/temp-parts", s.auth(s.handleListTempParts, "Production.ListTempParts"))
	mux.HandleFunc("POST /api/batches/temp-parts", s.auth(s.handleAddTempPart, "Production.AddTempPart"))
	mux.HandleFunc("DELETE /api/batches/temp-parts", s.auth(s.handleRemoveTempPart, "Production.RemoveTempPart"))
	mux.HandleFunc("POST /api/traces", s.auth(s.handleRecordTrace, "Trace.RecordTrace"))
	mux.HandleFunc("POST /api/traces/batch", s.auth(s.handleRecordTraces, "Trace.RecordTraces"))
	mux.HandleFunc("GET /api/traces/by-batch", s.auth(s.handleTraceByBatch, "Trace.GetTraceByBatch"))
	mux.HandleFunc("GET /api/traces/by-product", s.auth(s.handleTraceByProduct, "Trace.TraceByProduct"))
	mux.HandleFunc("GET /api/traces/by-part", s.auth(s.handleTraceByPart, "Trace.TraceByPart"))
	mux.HandleFunc("GET /api/traces/by-part-lot", s.auth(s.handleTraceByPartLot, "Trace.TraceByPartLot"))
	mux.HandleFunc("GET /api/audit", s.auth(s.handleAuditByRecord, "Audit.GetAuditLogs"))
	mux.HandleFunc("GET /api/stats", s.auth(s.handleStats, "Stats.GetStockStats"))
	mux.HandleFunc("POST /api/export/audit", s.auth(s.handleExportAudit, "Backup.ExportAuditLogCSV"))
	// 文件流单独一个端点：只给确实需要字节的调用方（Go 客户端、远程界面形态）。
	// 主机形态下文件就在用户自己机器上，界面报位置即可，不该再往「下载」里塞一份。
	mux.HandleFunc("POST /api/export/audit/download", s.auth(s.handleDownloadAudit, "Backup.ExportAuditLogCSV"))
	mux.HandleFunc("POST /api/backup", s.auth(s.handleBackupDatabase, "Backup.BackupDatabase"))
	mux.HandleFunc("POST /api/export/all", s.auth(s.handleExportAll, "Backup.ExportAllDataCSV"))
	mux.HandleFunc("POST /api/export/all/open", s.auth(s.handleOpenExportFolder, "Backup.ExportAllDataCSV"))
	// Round 5 补齐此前定义了权限点却没有路由消费的四个用例。
	//
	// restore / clear / set-datadir 在 handler 内还有一道本机闸门
	// （s.hostOperations，见 WithHostOperations）：权限点是"谁可以做"，
	// 本机闸门是"在哪台机器上可以做"，两者都要，缺一个都会在阶段 B/C 出事。
	mux.HandleFunc("GET /api/backup/files", s.auth(s.handleListBackups, "Backup.DataDir"))
	mux.HandleFunc("GET /api/datadir", s.auth(s.handleDataDir, "Backup.DataDir"))
	// 选目录不改数据，权限点挂在 Backup.SetDataDir 上是因为它服务于这个操作，
	// 且要跟真正写入的那一步保持同一档位——否则只读账号能弹框却提交不了。
	mux.HandleFunc("POST /api/datadir/pick", s.auth(s.handlePickDataDir, "Backup.SetDataDir"))
	mux.HandleFunc("POST /api/backup/autoschedule", s.auth(s.handleSetAutoBackup, "Backup.SetDataDir"))
	mux.HandleFunc("GET /api/backup/autoschedule", s.auth(s.handleGetAutoBackup, "Backup.DataDir"))
	mux.HandleFunc("POST /api/datadir", s.auth(s.handleSetDataDir, "Backup.SetDataDir"))
	mux.HandleFunc("POST /api/restore", s.auth(s.handleRestoreDatabase, "Backup.RestoreDatabase"))
	mux.HandleFunc("POST /api/restore/exit", s.auth(s.handleExitAfterRestore, "Backup.RestoreDatabase"))
	mux.HandleFunc("POST /api/clear", s.auth(s.handleClearDatabase, "Backup.ClearDatabase"))

	// ---- 局域网版：授权 / 设备 / 状态 ----
	//
	// 只在挂了 deviceGuard（服务器形态）时注册：单机桌面没有设备与授权概念，
	// 注册上去会让前端多出永远 404 的入口。状态条（lan/status）读的是
	// "已连接/额度"这类部署事实，任何登录用户都该看到，所以用空权限点。
	// 状态条对所有形态注册：单机/客户端/服务器各自回答自己的部署事实
	//（本地 / 连哪台主机 / 额度与只读）。放进 deviceGuard 分支会让客户端
	// 模式拿不到它——那正是要显示"当前主机地址"的形态。
	//
	// **不挂鉴权**：登录页就要显示"当前连的是哪台主机"，那时还没有会话。
	// 它只回部署形态与额度，不含账号与业务数据；主机指纹仅对已登录用户返回。
	mux.HandleFunc("GET /api/lan/status", s.handleLANStatus)
	// 服务器形态（deviceGuard）与客户端形态（远程代理）都要挂：
	// 管理员可能在客户端上撤销设备或导入授权（FR-37）。
	//
	// 单机桌面不算：那里 apps 虽然也是 *Service（实现了 LANAdmin），但没有
	// 设备与授权的语义，挂上去只会让单机用户看到一个"尚未导入授权"的卡片。
	_, remoteApps := s.apps.(interface{ RemoteDeployment() bool })
	if s.deviceGuard != nil || remoteApps {
		mux.HandleFunc("GET /api/license", s.auth(s.handleGetLicense, "Identity.LicenseRead"))
		mux.HandleFunc("POST /api/license/import", s.auth(s.handleImportLicense, "Identity.LicenseImport"))
		mux.HandleFunc("GET /api/devices", s.auth(s.handleListDevices, "Identity.DeviceList"))
		mux.HandleFunc("POST /api/devices/revoke", s.auth(s.handleRevokeDevice, "Identity.DeviceRevoke"))
		mux.HandleFunc("POST /api/devices/delete", s.auth(s.handleDeleteDevice, "Identity.DeviceRevoke"))
	}
	// 客户端模式：登录前改主机地址。它不挂 s.auth —— 地址错了根本登不进去，
	// 而这条端点只存在于本机桌面外壳（有启动凭据闸门），不对外。
	if s.lanConnect != nil {
		mux.HandleFunc("POST /api/lan/connect", s.handleLANConnect)
	}
	// 备份下载：只有服务器形态（或任何通过 HTTP 访问的形态）需要。主机桌面
	// 上文件就在本机，界面上给路径即可；但端点本身无害，统一注册省得前端
	// 再判断部署形态。权限是管理员（Backup.DownloadBackup 写档）。
	mux.HandleFunc("GET /api/backup/download", s.auth(s.handleDownloadBackup, "Backup.DownloadBackup"))

	// 前端构建产物（阶段 A 内嵌服务）。放在 API 路由之后，
	// /api/ 前缀不会被静态文件截走。
	if s.staticFS != nil {
		mux.Handle("/", serveStatic(s.staticFS, s.staticDir))
	} else {
		// 无前端产物时（cmd/server），根路径回一句欢迎 JSON。
		// 用 /{$} 精确匹配，避免把 /api/... 之类未注册路径也吞掉。
		mux.HandleFunc("GET /{$}", s.handleWelcome)
	}
	return s.withStartupGate(drainRequestBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.restoreRequiresRestart.Load() && strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/restore/exit" {
			writeErr(w, http.StatusServiceUnavailable, "SQLite 恢复后必须退出并重新打开程序；请关闭当前窗口后重新启动")
			return
		}
		mux.ServeHTTP(w, r)
	})))
}

// drainRequestBody 在处理器返回后排空未读的请求体。
//
// 为什么必须有：不少处理器会在读完 body 之前就返回——令牌不对的 bootstrap、
// 远程形态直接 403 的 restore/clear、越权请求等。net/http 在处理器返回后
// 若发现 body 没读完，会**直接关连接**；这时客户端往往还在发 body，Windows
// 上会收到 RST（WSAECONNRESET），于是用户看到的是"网络错误"而不是
// "初始化令牌不正确"这类真正该看到的答复。实测在本机约 5% 的请求会撞上。
//
// 排空上限 1MB：正常的 JSON 请求远小于它；超大 body 直接放弃（让连接关掉），
// 避免为恶意请求无限读取。
func drainRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if r.Body == nil || r.Body == http.NoBody {
			return
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
		_ = r.Body.Close()
	})
}

type apiUser struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name,omitempty"`
	Role        string `json:"role"`
}

func toAPIUser(u *model.User) apiUser {
	out := apiUser{ID: u.ID, Username: u.Username, Role: u.Role}
	if u.DisplayName != nil {
		out.DisplayName = *u.DisplayName
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// writeServerErr 把内部错误记入日志，只向客户端返回通用信息，
// 避免把 SQL 语句、表名等内部细节泄露出去。
func writeServerErr(w http.ResponseWriter, err error) {
	if writeUpstreamErr(w, err) {
		return
	}
	log.Printf("api: internal error: %v", err)
	writeErr(w, http.StatusInternalServerError, "internal error")
}

func writeAppErr(w http.ResponseWriter, status int, err error) {
	if !writeUpstreamErr(w, err) {
		writeErr(w, status, err.Error())
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleServerInfo(w http.ResponseWriter, r *http.Request) {
	writeServerInfo(w, s.desc, s.version, s.minClientVersion)
}

// writeServerInfo 是 handleServerInfo 的可测核心：只依赖 ServerDescriptor，
// 因此测试可以传入极小的假实现，不必构造整套 Applications。
func writeServerInfo(w http.ResponseWriter, desc usecase.ServerDescriptor, version, minClient string) {
	if desc == nil {
		writeErr(w, http.StatusServiceUnavailable, "server descriptor unavailable")
		return
	}
	info, err := desc.Describe()
	if err != nil {
		writeServerErr(w, err)
		return
	}
	// 版本由进程自报；数据库事实由用例层回答（D-016）。
	info.AppVersion = version
	if minClient != "" {
		// 以 api 注入的为准：service 的 cfg 在客户端形态下并不存在。
		info.MinClientVersion = minClient
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		// Remember 为真时额外签发长期凭证（见 remember.go）。
		// 客户端拿它去本地端点 DPAPI 加密落盘，下次启动换会话用。
		Remember bool `json:"remember"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid login body")
		return
	}
	dev := deviceHeaders(r)
	// 版本门禁排在密码之前：版本不对时让用户看到"请升级客户端"，
	// 而不是"密码错误"——后者会让他反复试密码（R12）。
	if msg := s.rejectOldClient(dev.Version); msg != "" {
		writeCodeErr(w, http.StatusUpgradeRequired, usecase.CodeVersionTooOld, msg)
		return
	}
	// 先验密码，再验设备。
	//
	// 顺序不能反：设备门禁会登记新设备并占额度，如果排在密码之前，
	// 任何人只要把客户端指向主机、随便输一个密码，就能把额度耗光。
	u, err := s.apps.Login(body.Username, body.Password)
	if err != nil || u == nil {
		if s.deviceGuard != nil {
			log.Printf("lan: 登录失败 user=%q device=%s ip=%s", body.Username, shortID(dev.DeviceID), dev.IP)
		}
		// 客户端形态：错误是主机返回的。把 device_limit / license_missing /
		// device_revoked 原样带回给界面——压成一句"账号或密码错误"会让用户
		// 永远不知道要去找管理员撤销设备（FR-14）。
		if writeUpstreamErr(w, err) {
			return
		}
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if s.deviceGuard != nil {
		if dev.DeviceID == "" {
			writeCodeErr(w, http.StatusBadRequest, usecase.CodeDeviceMissing,
				"缺少设备标识：请升级到局域网版客户端后重试")
			return
		}
		res, err := s.deviceGuard.LoginCheck(dev)
		if err != nil {
			writeServerErr(w, err)
			return
		}
		if !res.Allowed {
			writeCodeErr(w, http.StatusForbidden, res.Code, res.Message)
			return
		}
	}
	tok, err := s.issueSession(u, dev.DeviceID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "session unavailable")
		return
	}
	out := map[string]any{
		"token": tok,
		"user":  toAPIUser(u),
	}
	if s.deviceGuard != nil {
		rt := s.deviceGuard.Runtime()
		out["runtime"] = rt
	}
	if body.Remember && s.rememberEnabled {
		rt, err := s.remember.issue(u)
		if err != nil {
			// 记住登录失败不该挡住正常登录：用户照样能用，只是下次要输密码。
			log.Printf("issue remember credential: %v", err)
		} else {
			out["remember_token"] = rt
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleBootstrapAdmin(w http.ResponseWriter, r *http.Request) {
	got := strings.TrimSpace(r.Header.Get("X-Bootstrap-Token"))
	s.bootstrapMu.Lock()
	defer s.bootstrapMu.Unlock()

	// 先判"是否已初始化"，再验令牌。
	//
	// 顺序反了会给出误导性的答案：库已经建过管理员时，没带令牌的请求会被
	// 判成"令牌不正确"（403），用户于是去翻令牌文件——而真正该说的是
	// "已经初始化，请直接登录"（409）。"是否已初始化"本身不是秘密，
	// GET /api/setup/status 对匿名都回答这个问题。
	n, err := s.apps.UserCount()
	if err != nil {
		writeAppErr(w, http.StatusInternalServerError, err)
		return
	}
	if n > 0 {
		writeErr(w, http.StatusConflict, "already initialized")
		return
	}

	// 局域网服务器的首建门禁：空库第一次启动的那几分钟对整个局域网敞开，
	// 没有令牌任何人都能抢先建掉管理员（FR-41）。
	//
	// 客户端形态（s.bootstrapToken 为空）不在这里校验：令牌属于主机，
	// 客户端拿到后必须原样转发过去（见 CreateInitialAdminWithToken）。
	if s.bootstrapToken != "" {
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.bootstrapToken)) != 1 {
			writeCodeErr(w, http.StatusForbidden, usecase.CodeBootstrapToken,
				"初始化令牌不正确，请查看主机安装目录下的初始化令牌文件")
			return
		}
	}
	var body struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	// 客户端形态需要把令牌转发给主机；服务器形态用自己签发的那个。
	var u *model.User
	if fwd, ok := s.apps.(interface {
		CreateInitialAdminWithToken(username, password, displayName, token string) (*model.User, error)
	}); ok {
		u, err = fwd.CreateInitialAdminWithToken(body.Username, body.Password, body.DisplayName, got)
	} else {
		u, err = s.apps.CreateInitialAdmin(body.Username, body.Password, body.DisplayName)
	}
	if err != nil {
		writeAppErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, toAPIUser(u))
}

// auth 包装鉴权与用例权限点；opName 为空表示仅需登录。
func (s *Server) auth(next http.HandlerFunc, opName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.userFromRequest(r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if remote, ok := s.apps.(interface{ CheckSession(int64) error }); ok {
			if err := remote.CheckSession(u.ID); err != nil {
				var upstream *APIError
				if errors.As(err, &upstream) && upstream.Status == http.StatusUnauthorized {
					s.revokeToken(bearerToken(r))
				}
				if !writeUpstreamErr(w, err) {
					writeErr(w, http.StatusBadGateway, "无法连接主机")
				}
				return
			}
		}
		if msg := s.rejectOldClient(deviceHeaders(r).Version); msg != "" {
			writeCodeErr(w, http.StatusUpgradeRequired, usecase.CodeVersionTooOld, msg)
			return
		}
		if opName != "" && !usecase.Allow(auth.RoleFromString(u.Role), opName) {
			writeErr(w, http.StatusForbidden, "forbidden")
			return
		}
		// 授权到期只读（D-10）：业务写操作一律拒绝，但**不**拦管理员与备份
		// ——否则客户到期后连"导入新授权"和"导出数据"都做不了，只能打电话。
		if s.deviceGuard != nil && opName != "" {
			if rt := s.deviceGuard.Runtime(); rt.ReadOnly && isBusinessWrite(opName) {
				writeCodeErr(w, http.StatusForbidden, usecase.CodeLicenseExpired,
					rt.PolicyMessage)
				return
			}
		}
		next(w, r)
	}
}

// rejectOldClient 返回非空字符串表示应当拒绝（426）。
//
// 只在局域网形态生效（deviceGuard 非 nil）：单机桌面根本没有"客户端版本"，
// 服务端也没配置最低版本时不做任何判断。
func (s *Server) rejectOldClient(clientVersion string) string {
	if s.deviceGuard == nil || s.minClientVersion == "" {
		return ""
	}
	// 不报版本 = 无法证明够新，按"过低"处理。
	//
	// 之前这里直接落到 VersionLess，而 VersionLess 对空串返回 false
	// （"无法解析就不拦"），于是旧客户端只要不发这个头就能绕过门禁。
	// 开发构建"dev"仍放行——那是我们自己人，且 version 无法比较。
	if strings.TrimSpace(clientVersion) == "" {
		return "客户端未上报版本号（要求 " + s.minClientVersion + " 以上），请升级客户端后再连接"
	}
	if !VersionLess(clientVersion, s.minClientVersion) {
		return ""
	}
	got := strings.TrimSpace(clientVersion)
	if got == "" {
		got = "未知"
	}
	return "客户端版本过低（" + got + " < " + s.minClientVersion + "），请升级客户端后再连接"
}

// isBusinessWrite 判断一个权限点是否属于"业务写入"（只读模式要拦的那一类）。
//
// 按模块而不是按 HTTP 方法：POST /api/export/audit 也是 POST，但它是导出
// 而不是改数据；反过来 GET 里也确实没有写操作。用权限表里的 Access 档 +
// 模块白名单表达这条口径，与权限表本身保持单一来源。
func isBusinessWrite(opName string) bool {
	p, ok := usecase.PermissionFor(opName)
	if !ok || p.Access != auth.AccessWrite {
		return false
	}
	switch p.Module {
	case auth.ModuleUsers, auth.ModuleBackup:
		return false
	}
	return true
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, ok := s.userFromRequest(r)
	if !ok || u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, toAPIUser(u))
}

func (s *Server) handleListParts(w http.ResponseWriter, _ *http.Request) {
	parts, err := s.apps.ListParts()
	if err != nil {
		writeServerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, parts)
}

func (s *Server) handleStockIn(w http.ResponseWriter, r *http.Request) {
	u, _ := s.userFromRequest(r)
	var body struct {
		PartID int64   `json:"part_id"`
		Qty    float64 `json:"qty"`
		Note   string  `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	// C3：操作人来自会话，不用客户端字段。
	batchNo, err := s.apps.StockIn(usecase.StockInInput{
		PartID:   body.PartID,
		Qty:      body.Qty,
		Note:     body.Note,
		Operator: operatorName(u),
	})
	if err != nil {
		writeAppErr(w, http.StatusBadRequest, err)
		return
	}
	// 批次号带回界面：入库完成的提示要能指认"这一笔记成了哪批"。
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "batch_no": batchNo})
}

// handleListPartInbounds 返回零件的入库批次列表（零件详情页）。
// part_id 由调用方带上；缺它说明调用方拼错了 URL，空列表会把
// "请求错了"伪装成"没有入库记录"，比报错更糟。
func (s *Server) handleListPartInbounds(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("part_id")
	pid, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || pid <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid part_id")
		return
	}
	rows, err := s.apps.ListPartInbounds(pid)
	if err != nil {
		writeAppErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// handleAdjustInbounds 逐批次盘点：一次提交多个批次的实盘数量。
//
// 与 adjust-stock（总量）分开而不是合并成一个端点：两者的入参形状
// 完全不同，混在一起就要靠字段有无猜模式，猜错会把逐批次盘点退回成
// 总量盘点——账面看不出差别，用户却以为批次剩余已经核过。
func (s *Server) handleAdjustInbounds(w http.ResponseWriter, r *http.Request) {
	u, _ := s.userFromRequest(r)
	var body struct {
		PartID int64 `json:"part_id"`
		Items  []struct {
			InboundID int64   `json:"inbound_id"`
			Remaining float64 `json:"remaining"`
		} `json:"items"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	items := make([]usecase.InboundCount, 0, len(body.Items))
	for _, it := range body.Items {
		items = append(items, usecase.InboundCount{InboundID: it.InboundID, Remaining: it.Remaining})
	}
	if err := s.apps.AdjustInbounds(usecase.AdjustInboundsInput{
		PartID:   body.PartID,
		Items:    items,
		Operator: operatorName(u),
	}); err != nil {
		writeAppErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAdjustStock(w http.ResponseWriter, r *http.Request) {
	u, _ := s.userFromRequest(r)
	var body struct {
		PartID int64   `json:"part_id"`
		NewQty float64 `json:"new_qty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := s.apps.AdjustStock(usecase.AdjustStockInput{
		PartID:   body.PartID,
		NewQty:   body.NewQty,
		Operator: operatorName(u),
	}); err != nil {
		writeAppErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListBatches(w http.ResponseWriter, _ *http.Request) {
	list, err := s.apps.ListBatches()
	if err != nil {
		writeServerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleUpdateBatchStatus(w http.ResponseWriter, r *http.Request) {
	u, _ := s.userFromRequest(r)
	var body struct {
		ID     int64 `json:"id"`
		Status int   `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := s.apps.UpdateBatchStatus(usecase.UpdateBatchStatusInput{
		ID:       body.ID,
		Status:   body.Status,
		Operator: operatorName(u),
	}); err != nil {
		writeAppErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRecentAudit(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeErr(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = n
	}
	logs, err := s.apps.ListRecentAuditLogs(limit)
	if err != nil {
		writeServerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func operatorName(u *model.User) string {
	if u == nil {
		return ""
	}
	if u.DisplayName != nil && *u.DisplayName != "" {
		return *u.DisplayName
	}
	return u.Username
}

// ErrUnauthorized 供调用方判断（预留）。
var ErrUnauthorized = errors.New("unauthorized")
