package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"app/internal/backuplayout"
	"app/internal/license"
	"app/internal/usecase"
)

// 局域网版的 HTTP 面：健康探针、首启状态、授权、设备、备份下载。
//
// 这里只做"接线与错误契约"，所有判定都在 usecase/service 层：
// api 不应该知道"超额怎么算"，否则单机样式与服务器样式会出现两套口径。

// lanAdmin 把 s.apps 当作管理面使用；未实现时返回 nil。
//
// 用接口断言而不是给 Applications 加方法：单机桌面不需要这些能力，
// 强行加进 Applications 会让 api.Client（远程版）也必须实现一堆
// 永远用不到的桩方法，而桩方法最容易被误当成"实现好了"。
func (s *Server) lanAdmin() usecase.LANAdmin {
	if a, ok := s.apps.(usecase.LANAdmin); ok {
		return a
	}
	return nil
}

// handleReady 检查依赖是否可用（/readyz）。
func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	if s.readiness != nil {
		if err := s.readiness(); err != nil {
			log.Printf("readyz: %v", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "not-ready", "error": "database unavailable",
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// handleWelcome 是服务器根路径的欢迎信息（FR-06）。
//
// 只回服务名与版本，不含任何业务数据：客户在浏览器里误开主机地址时，
// 应该看到"这是 RFERP 服务，请用客户端连接"，而不是一个 404 或空页面。
func (s *Server) handleWelcome(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "RFERP",
		"version": s.version,
		"tip":     "这是 RFERP 局域网主机的服务端口，请在其他电脑上用 RFERP 客户端连接本机 IP。",
		"health":  "/healthz",
		"ready":   "/readyz",
	})
}

// handleSetupStatus 让首次连接的客户端知道"这台主机初始化了没有"。
//
// 匿名可读，只回 needsSetup 与 tokenRequired 两个布尔：额度、客户名都不是
// 匿名者该知道的信息；用户名/账号更不在返回里。
func (s *Server) handleSetupStatus(w http.ResponseWriter, _ *http.Request) {
	// 客户端形态：状态在主机上，问主机（含"需不需要初始化令牌"）。
	if p, ok := s.apps.(interface {
		SetupInfo() (needsSetup bool, tokenRequired bool, err error)
	}); ok {
		need, token, err := p.SetupInfo()
		if err != nil {
			writeServerErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"needsSetup": need, "tokenRequired": token})
		return
	}
	n, err := s.apps.UserCount()
	if err != nil {
		writeServerErr(w, err)
		return
	}
	// 按 §7.2 只回一个布尔：客户的授权额度、已用数都是商业信息，
	// 匿名端点没有必要公开——连接向导也只需要知道"要不要先建管理员"。
	out := map[string]any{"needsSetup": n == 0}
	if s.deviceGuard != nil && s.bootstrapToken != "" {
		// 告诉客户端"这台主机需要初始化令牌"，它好把输入框亮出来（FR-41）。
		// 只有"需要令牌"这件事本身被公开，令牌值不在其中。
		out["tokenRequired"] = true
	}
	writeJSON(w, http.StatusOK, out)
}

// handleLANStatus 是客户端状态条的数据源：已连接 + 额度 + 只读。
func (s *Server) handleLANStatus(w http.ResponseWriter, r *http.Request) {
	// 已登录才给主机指纹：它对排障有用（"你连的到底是不是这台主机"），
	// 但对匿名者没有用途，也没必要在登录页之前公开一台机器的标识。
	_, authed := s.userFromRequest(r)
	if s.deviceGuard != nil {
		out := map[string]any{"deployment": "lan"}
		if authed {
			// 额度、客户名、只读状态只给已登录用户：客户名属于商业信息，
			// 没有理由让局域网里任何一台机器匿名读到。
			out["runtime"] = s.deviceGuard.Runtime()
			out["hostDevice"] = s.deviceGuard.HostDeviceID()
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	// 客户端模式：apps 是 api.Client，它自带 ServerURL。用结构化断言而不是
	// 新增一个 Option——"谁知道自己连的是哪台主机"这件事由 apps 本身回答，
	// 注入一个额外的回调只会多一条可与 apps 不一致的路径。
	if p, ok := s.apps.(interface{ ServerURL() string }); ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"deployment": "client",
			"serverUrl":  p.ServerURL(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deployment": "local"})
}

// writeUpstreamErr 把代理转发的错误按原状态码与 code 透传出去。
//
// 客户端模式下这些端点的错误其实是主机返回的：把 403 device_limit 压成
// 500，界面就只能显示一句"出错了"，用户不知道该撤销设备还是该导入授权。
func writeUpstreamErr(w http.ResponseWriter, err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		if ae.Code != "" {
			writeCodeErr(w, ae.Status, ae.Code, ae.Message)
		} else {
			writeErr(w, ae.Status, ae.Message)
		}
		return true
	}
	return false
}

func (s *Server) handleGetLicense(w http.ResponseWriter, _ *http.Request) {
	a := s.lanAdmin()
	if a == nil {
		writeErr(w, http.StatusNotImplemented, "当前部署形态没有授权管理")
		return
	}
	st, err := a.LicenseStatus()
	if err != nil {
		if !writeUpstreamErr(w, err) {
			writeServerErr(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleImportLicense 接收授权文件原文。
//
// 请求体就是文件字节（Content-Type: application/octet-stream），
// 也接受 {"license": "<文本>"} 的 JSON 形态——安装脚本用前者，界面用后者。
func (s *Server) handleImportLicense(w http.ResponseWriter, r *http.Request) {
	a := s.lanAdmin()
	if a == nil {
		writeErr(w, http.StatusNotImplemented, "当前部署形态没有授权管理")
		return
	}
	u, _ := s.userFromRequest(r)
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "读取授权文件失败")
		return
	}
	raw = unwrapLicenseBody(raw)
	st, err := a.ImportLicense(raw, operatorName(u))
	if err != nil {
		if writeUpstreamErr(w, err) {
			return
		}
		// 区分三种失败：
		//   - 签名/公钥不对 → 403：文件可能是伪造或来自别的产品线（R8）；
		//   - 文件本身不合法 → 400：用户拿错了文件；
		//   - 其它（写库失败等）→ 500：是服务器的问题，不该让用户去改文件。
		switch {
		case errors.Is(err, license.ErrBadSignature), errors.Is(err, license.ErrUnknownKey):
			writeCodeErr(w, http.StatusForbidden, usecase.CodeLicenseInvalid, err.Error())
		case errors.Is(err, license.ErrMalformed), errors.Is(err, license.ErrEmptyPayload),
			errors.Is(err, license.ErrInvalidMaxClie):
			writeCodeErr(w, http.StatusBadRequest, usecase.CodeLicenseInvalid, err.Error())
		default:
			writeServerErr(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// unwrapLicenseBody 兼容 JSON 包装形态。
func unwrapLicenseBody(raw []byte) []byte {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, `"license"`) {
		var body struct {
			License string `json:"license"`
		}
		if err := json.Unmarshal(raw, &body); err == nil && strings.TrimSpace(body.License) != "" {
			return []byte(body.License)
		}
	}
	return raw
}

func (s *Server) handleListDevices(w http.ResponseWriter, _ *http.Request) {
	a := s.lanAdmin()
	if a == nil {
		writeErr(w, http.StatusNotImplemented, "当前部署形态没有设备管理")
		return
	}
	list, err := a.ListDevices()
	if err != nil {
		if !writeUpstreamErr(w, err) {
			writeServerErr(w, err)
		}
		return
	}
	out := map[string]any{"devices": list}
	// 客户端形态没有本地 deviceGuard（额度由主机算），此处不能直接解引用。
	if s.deviceGuard != nil {
		out["runtime"] = s.deviceGuard.Runtime()
		out["hostDevice"] = s.deviceGuard.HostDeviceID()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	s.changeDevice(w, r, false)
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	s.changeDevice(w, r, true)
}

// changeDevice 处理撤销/删除。两者的差别只在"记录是否保留"，
// 权限、参数校验与错误契约完全一致，所以合成一处。
func (s *Server) changeDevice(w http.ResponseWriter, r *http.Request, deleteRec bool) {
	a := s.lanAdmin()
	if a == nil {
		writeErr(w, http.StatusNotImplemented, "当前部署形态没有设备管理")
		return
	}
	var body struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	id := normalizeDeviceID(body.DeviceID)
	if id == "" {
		writeErr(w, http.StatusBadRequest, "设备标识不合法")
		return
	}
	u, _ := s.userFromRequest(r)
	var err error
	if deleteRec {
		err = a.DeleteDevice(id, operatorName(u))
	} else {
		err = a.RevokeDevice(id, operatorName(u))
	}
	if err != nil {
		if !writeUpstreamErr(w, err) {
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	// 撤销/删除后立刻踢掉该设备的会话（FR-29）。会话表属于 HTTP 层，
	// 不在 service 里做——service 不该知道 HTTP 会话的存在。
	//
	// 客户端形态（无 deviceGuard）本地会话本来就不绑定设备，直接按 id 删
	// 不会命中任何条目；这里仍然调用，是为了"删除设备即下线"这条语义在
	// 两种形态下一致。
	keepHost := s.deviceGuard != nil && s.deviceGuard.IsHostDevice(id)
	if deleteRec || !keepHost {
		if n := s.revokeDeviceSessions(id); n > 0 {
			log.Printf("lan: 设备 %s 已撤销，作废 %d 个会话", shortID(id), n)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleLANConnect 让客户端在登录前切换主机地址。
func (s *Server) handleLANConnect(w http.ResponseWriter, r *http.Request) {
	if s.lanConnect == nil {
		writeErr(w, http.StatusNotImplemented, "当前形态不支持修改服务器地址")
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	url, err := s.lanConnect(body.URL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.Lock()
	clear(s.sessions)
	s.mu.Unlock()
	s.remember.mu.Lock()
	clear(s.remember.byID)
	s.remember.mu.Unlock()
	s.remember.clearDisk()
	writeJSON(w, http.StatusOK, map[string]string{"serverUrl": url})
}

// handleDownloadBackup 把主机上的备份文件以流的方式交给远程管理员（FR-34）。
//
// 文件名只接受备份目录里的"文件名"：backuplayout.Resolve 负责拒绝绝对路径
// 与 .. 逃逸（§10.2 的路径穿越用例由它兜住）。
func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	// 客户端形态：备份在主机上，本机连目录都没有。转发流。
	if remote, ok := s.apps.(interface {
		StreamBackup(name string) (*http.Response, error)
	}); ok {
		resp, err := remote.StreamBackup(name)
		if err != nil {
			if !writeUpstreamErr(w, err) {
				writeErr(w, http.StatusBadGateway, "从主机获取备份失败："+err.Error())
			}
			return
		}
		defer resp.Body.Close()
		for _, h := range []string{"Content-Disposition", "Content-Type", "Content-Length"} {
			if v := resp.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		if _, err := io.Copy(w, resp.Body); err != nil {
			log.Printf("api: 转发备份流中断: %v", err)
		}
		return
	}
	target, err := backuplayout.Resolve(name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	f, err := os.Open(target)
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, "备份文件不存在")
			return
		}
		writeServerErr(w, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeServerErr(w, err)
		return
	}
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+filepath.Base(target)+`"; filename*=UTF-8''`+urlEscape(filepath.Base(target)))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12] + "…"
}

// normalizeDeviceID 只接受 64 位小写十六进制指纹。
//
// 严格校验的原因：这个值会被写进数据库并作为额度计数的键。放过任意字符串
// 等于让客户端自己决定"我是哪台设备"，换个字符串就是一个新设备。
func normalizeDeviceID(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if len(s) != 64 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ""
		}
	}
	return s
}

// deviceHeaders 从请求里取设备事实。
func deviceHeaders(r *http.Request) DeviceLogin {
	return DeviceLogin{
		DeviceID: normalizeDeviceID(r.Header.Get("X-RFERP-Device-ID")),
		Name:     headerText(r.Header.Get("X-RFERP-Device-Name"), 128),
		Version:  headerText(r.Header.Get("X-RFERP-Client-Version"), 32),
		OS:       headerText(r.Header.Get("X-RFERP-Client-OS"), 64),
		IP:       clientIP(r),
	}
}

// headerText 读一个显示用头：非法 UTF-8 一律当作空。
//
// HTTP 头是字节流，没有编码声明。旧客户端/脚本用 GBK 发中文计算机名时，
// Go 会把这些字节原样带进来，JSON 序列化再把非法序列换成 U+FFFD——结果
// 设备列表里出现一串"????"，而且会被当成真名字写进库。宁可当作"没带名字"
// （服务端会保留已登记的名字），也不要存一串无法还原的替换字符。
func headerText(s string, n int) string {
	if !utf8.ValidString(s) {
		return ""
	}
	return truncate(s, n)
}

// isLoopbackRequest 判断请求是否来自本机回环。
//
// 空 RemoteAddr（httptest 直接构造的请求）按本机处理：那是测试或进程内调用，
// 不是网络对端。
func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return truncate(r.RemoteAddr, 64)
	}
	return host
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// writeCodeErr 输出带 code 的错误契约（§7.3）。
func writeCodeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}
