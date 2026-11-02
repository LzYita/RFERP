package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"app/internal/model"
	"app/internal/usecase"
)

// 局域网版的 HTTP 契约：设备门禁、会话绑定、只读降级、初始化令牌、
// 备份下载的路径穿越。

type fakeGuard struct {
	rt      usecase.DeviceRuntime
	result  usecase.DeviceLoginResult
	revoked map[string]bool
	lastIn  usecase.DeviceLogin
}

func (g *fakeGuard) LoginCheck(in usecase.DeviceLogin) (usecase.DeviceLoginResult, error) {
	g.lastIn = in
	return g.result, nil
}

func (g *fakeGuard) ValidateSession(id string) error {
	if g.revoked[id] {
		return errors.New("revoked")
	}
	return nil
}

func (g *fakeGuard) IsHostDevice(string) bool       { return false }
func (g *fakeGuard) Runtime() usecase.DeviceRuntime { return g.rt }
func (g *fakeGuard) HostDeviceID() string           { return "" }

const testDevice = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const otherDevice = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

// DataDir 供 /api/datadir 的本机分支使用（远程分支不碰它）。
func (f *fakeApps) DataDir() string { return `C:	est\data` }

func lanApps() *fakeApps {
	return &fakeApps{loginUser: testWarehouseUser(), userCount: 1}
}

func doJSONWithHeaders(t *testing.T, s *Server, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

// 登录必须带设备标识；缺了就是 400，而不是悄悄当成"单机"放行。
func TestLoginRequiresDeviceHeader(t *testing.T) {
	g := &fakeGuard{result: usecase.DeviceLoginResult{Allowed: true}}
	s := New(lanApps(), WithDeviceGuard(g))

	w := doJSONWithHeaders(t, s, "POST", "/api/login",
		map[string]string{"username": "wh", "password": "x"}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), usecase.CodeDeviceMissing) {
		t.Fatalf("body = %s", w.Body.String())
	}
}

// 门禁拒绝时把服务端文案与 code 原样带回（FR-14：不能变成 500）。
func TestLoginPassesGuardCode(t *testing.T) {
	g := &fakeGuard{result: usecase.DeviceLoginResult{
		Code: usecase.CodeDeviceLimit, Message: "授权客户端数已满（1/1）",
	}}
	s := New(lanApps(), WithDeviceGuard(g))

	w := doJSONWithHeaders(t, s, "POST", "/api/login",
		map[string]string{"username": "wh", "password": "x"},
		map[string]string{"X-RFERP-Device-ID": testDevice})
	if w.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["code"] != usecase.CodeDeviceLimit || !strings.Contains(body["error"], "已满") {
		t.Fatalf("body = %v", body)
	}
	// 设备头被完整传给门禁（名称/版本用于登记）。
	if g.lastIn.DeviceID != testDevice {
		t.Fatalf("guard 收到的设备标识 = %q", g.lastIn.DeviceID)
	}
}

// 会话绑定设备：换机器用同一 token 一律 401（FR-28）。
func TestSessionBindsDevice(t *testing.T) {
	g := &fakeGuard{result: usecase.DeviceLoginResult{Allowed: true}, revoked: map[string]bool{}}
	s := New(lanApps(), WithDeviceGuard(g))

	w := doJSONWithHeaders(t, s, "POST", "/api/login",
		map[string]string{"username": "wh", "password": "x"},
		map[string]string{"X-RFERP-Device-ID": testDevice})
	if w.Code != http.StatusOK {
		t.Fatalf("login = %d %s", w.Code, w.Body.String())
	}
	var lr struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &lr)
	if lr.Token == "" {
		t.Fatal("没有拿到 token")
	}

	// 不带设备头
	w = doJSONWithHeaders(t, s, "GET", "/api/me", nil,
		map[string]string{"Authorization": "Bearer " + lr.Token})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("无设备头应 401，得到 %d", w.Code)
	}
	// 换一台设备
	w = doJSONWithHeaders(t, s, "GET", "/api/me", nil,
		map[string]string{"Authorization": "Bearer " + lr.Token, "X-RFERP-Device-ID": otherDevice})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("设备不匹配应 401，得到 %d", w.Code)
	}
	// 原设备可以
	w = doJSONWithHeaders(t, s, "GET", "/api/me", nil,
		map[string]string{"Authorization": "Bearer " + lr.Token, "X-RFERP-Device-ID": testDevice})
	if w.Code != http.StatusOK {
		t.Fatalf("原设备应 200，得到 %d", w.Code)
	}
	// 设备被撤销后同一 token 立即失效（FR-29）
	g.revoked[testDevice] = true
	w = doJSONWithHeaders(t, s, "GET", "/api/me", nil,
		map[string]string{"Authorization": "Bearer " + lr.Token, "X-RFERP-Device-ID": testDevice})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("撤销后应 401，得到 %d", w.Code)
	}
}

// 到期只读：业务写入被拒，但读与管理/备份类照常（D-10）。
func TestReadOnlyBlocksBusinessWrites(t *testing.T) {
	g := &fakeGuard{
		result:  usecase.DeviceLoginResult{Allowed: true},
		revoked: map[string]bool{},
		rt:      usecase.DeviceRuntime{Licensed: true, ReadOnly: true, PolicyMessage: "授权已到期"},
	}
	apps := lanApps()
	s := New(apps, WithDeviceGuard(g))

	tok, err := s.issueSession(testWarehouseUser(), testDevice)
	if err != nil {
		t.Fatal(err)
	}
	h := map[string]string{"Authorization": "Bearer " + tok, "X-RFERP-Device-ID": testDevice}

	if w := doJSONWithHeaders(t, s, "POST", "/api/parts/stock-in",
		map[string]any{"part_id": 1, "qty": 1}, h); w.Code != http.StatusForbidden {
		t.Fatalf("只读下业务写入应 403，得到 %d", w.Code)
	} else if !strings.Contains(w.Body.String(), usecase.CodeLicenseExpired) {
		t.Fatalf("body = %s", w.Body.String())
	}
	if w := doJSONWithHeaders(t, s, "GET", "/api/parts", nil, h); w.Code != http.StatusOK {
		t.Fatalf("只读下读取应 200，得到 %d", w.Code)
	}
}

// 初始化令牌（FR-41）：空库首建必须带令牌。
func TestBootstrapTokenRequired(t *testing.T) {
	apps := &fakeApps{userCount: 0}
	token := "TOKEN" + strings.Repeat("9", 4)
	s := New(apps, WithBootstrapToken(token))

	w := doJSONWithHeaders(t, s, "POST", "/api/bootstrap-admin",
		map[string]string{"username": "admin", "password": "1234567890"}, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("无令牌应 403，得到 %d", w.Code)
	}
	w = doJSONWithHeaders(t, s, "POST", "/api/bootstrap-admin",
		map[string]string{"username": "admin", "password": "1234567890"},
		map[string]string{"X-Bootstrap-Token": token})
	if w.Code != http.StatusOK {
		t.Fatalf("带正确令牌应 200，得到 %d %s", w.Code, w.Body.String())
	}
}

// 备份下载不能穿越目录（§10.2）。
func TestBackupDownloadRejectsTraversal(t *testing.T) {
	s := New(lanApps())
	tok, err := s.issueSession(&model.User{ID: 1, Username: "a", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	w := doJSONWithHeaders(t, s, "GET", "/api/backup/download?name=..%5Csecret.txt", nil,
		map[string]string{"Authorization": "Bearer " + tok})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("路径穿越应 400，得到 %d %s", w.Code, w.Body.String())
	}
}

// /readyz 反映依赖状态，而不是进程状态。
func TestReadyzReflectsDependency(t *testing.T) {
	ok := New(lanApps(), WithReadiness(func() error { return nil }))
	if w := doJSONWithHeaders(t, ok, "GET", "/readyz", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("就绪时应 200，得到 %d", w.Code)
	}
	bad := New(lanApps(), WithReadiness(func() error { return errors.New("db locked") }))
	if w := doJSONWithHeaders(t, bad, "GET", "/readyz", nil, nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("依赖异常时应 503，得到 %d", w.Code)
	}
}

// 远程调用方不得拿到服务器目录布局（R14 / FR-16）。
func TestDataDirRedactedForRemoteCaller(t *testing.T) {
	apps := lanApps()
	s := New(apps)
	tok, err := s.issueSession(&model.User{ID: 1, Username: "a", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}

	// 局域网对端：只回 remote 标记。
	req := httptest.NewRequest("GET", "/api/datadir", nil)
	req.RemoteAddr = "192.168.1.50:51234"
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "backupDir") || !strings.Contains(w.Body.String(), "remote") {
		t.Fatalf("远程响应泄露了目录：%s", w.Body.String())
	}

	// 本机回环：保留完整目录（主机 UI 要显示备份位置）。
	req = httptest.NewRequest("GET", "/api/datadir", nil)
	req.RemoteAddr = "127.0.0.1:51234"
	req.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "dataDir") {
		t.Fatalf("本机请求应返回目录：%s", w.Body.String())
	}
}

// 匿名状态端点不得泄露客户名与额度（商业信息）。
func TestLANStatusHidesLicenseForAnonymous(t *testing.T) {
	g := &fakeGuard{
		rt: usecase.DeviceRuntime{
			Licensed: true, MaxClients: 2, UsedClients: 1, Customer: "某客户",
		},
	}
	s := New(lanApps(), WithDeviceGuard(g))

	w := doJSONWithHeaders(t, s, "GET", "/api/lan/status", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "某客户") || strings.Contains(body, "max_clients") {
		t.Fatalf("匿名响应泄露了授权信息：%s", body)
	}
	if !strings.Contains(body, `"deployment":"lan"`) {
		t.Fatalf("缺少部署形态：%s", body)
	}

	// 登录后可以拿到（设备列表页要用）。局域网会话绑定设备，
	// 所以这里必须同时带设备头，否则 401——那正是 FR-28 的行为。
	tok, err := s.issueSession(testViewerUser(), testDevice)
	if err != nil {
		t.Fatal(err)
	}
	w = doJSONWithHeaders(t, s, "GET", "/api/lan/status", nil,
		map[string]string{"Authorization": "Bearer " + tok, "X-RFERP-Device-ID": testDevice})
	if !strings.Contains(w.Body.String(), "max_clients") {
		t.Fatalf("登录后应返回额度：%s", w.Body.String())
	}
}

// setup/status 只回答"要不要初始化"，不回答额度。
func TestSetupStatusDoesNotLeakQuota(t *testing.T) {
	apps := &fakeApps{userCount: 0}
	s := New(apps)
	w := doJSONWithHeaders(t, s, "GET", "/api/setup/status", nil, nil)
	body := w.Body.String()
	if !strings.Contains(body, `"needsSetup":true`) {
		t.Fatalf("body = %s", body)
	}
	if strings.Contains(body, "maxClients") || strings.Contains(body, "usedClients") {
		t.Fatalf("setup/status 泄露了额度：%s", body)
	}
}

// 客户端形态：首建管理员时把令牌原样转发给主机。
type tokenApps struct {
	*fakeApps
	gotToken string
}

func (a *tokenApps) CreateInitialAdminWithToken(username, password, displayName, token string) (*model.User, error) {
	a.gotToken = token
	return &model.User{ID: 1, Username: username, Role: "admin"}, nil
}

func TestBootstrapTokenForwardedOnClient(t *testing.T) {
	apps := &tokenApps{fakeApps: &fakeApps{userCount: 0}}
	s := New(apps)
	w := doJSONWithHeaders(t, s, "POST", "/api/bootstrap-admin",
		map[string]string{"username": "admin", "password": "1234567890"},
		map[string]string{"X-Bootstrap-Token": "FORWARDME"})
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
	if apps.gotToken != "FORWARDME" {
		t.Fatalf("转发给主机的令牌 = %q", apps.gotToken)
	}
}

// 客户端形态（apps 是远程代理、没有本地 deviceGuard）：
// 授权/设备端点必须能注册、能转发，且不能因为 deviceGuard 为空 panic。
type clientApps struct {
	*fakeApps
	devices []model.Device
}

func (a *clientApps) RemoteDeployment() bool { return true }
func (a *clientApps) LicenseStatus() (usecase.LicenseStatus, error) {
	return usecase.LicenseStatus{Runtime: usecase.DeviceRuntime{Licensed: true, MaxClients: 2}}, nil
}
func (a *clientApps) ImportLicense([]byte, string) (usecase.LicenseStatus, error) {
	return usecase.LicenseStatus{}, nil
}
func (a *clientApps) ListDevices() ([]model.Device, error) { return a.devices, nil }
func (a *clientApps) RevokeDevice(string, string) error    { return nil }
func (a *clientApps) DeleteDevice(string, string) error    { return nil }
func (a *clientApps) EnsureHostDevice() (string, error)    { return "", nil }

func TestClientDeploymentProxiesDevices(t *testing.T) {
	apps := &clientApps{
		fakeApps: &fakeApps{loginUser: testWarehouseUser(), userCount: 1},
		devices:  []model.Device{{DeviceIDHash: testDevice, Kind: "client", Status: "active"}},
	}
	s := New(apps) // 注意：不传 deviceGuard
	tok, err := s.issueSession(&model.User{ID: 1, Username: "a", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	h := map[string]string{"Authorization": "Bearer " + tok}

	w := doJSONWithHeaders(t, s, "GET", "/api/devices", nil, h)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), testDevice) {
		t.Fatalf("没有转发设备列表：%s", w.Body.String())
	}
	if w := doJSONWithHeaders(t, s, "POST", "/api/devices/revoke",
		map[string]string{"device_id": testDevice}, h); w.Code != http.StatusOK {
		t.Fatalf("revoke code = %d body=%s", w.Code, w.Body.String())
	}
	if w := doJSONWithHeaders(t, s, "GET", "/api/license", nil, h); w.Code != http.StatusOK {
		t.Fatalf("license code = %d body=%s", w.Code, w.Body.String())
	}
}

// 客户端形态：登录失败要带回主机的原因（额度满/设备撤销），不能压成"密码错"。
func TestLoginPropagatesUpstreamCode(t *testing.T) {
	apps := &fakeApps{
		loginErr: &APIError{Status: 403, Code: usecase.CodeDeviceLimit, Message: "授权客户端数已满（2/2）"},
	}
	s := New(apps)
	w := doJSONWithHeaders(t, s, "POST", "/api/login",
		map[string]string{"username": "u", "password": "p"}, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), usecase.CodeDeviceLimit) {
		t.Fatalf("body = %s", w.Body.String())
	}
}

// 版本门禁（FR-15）：低于最低版本的客户端在登录与已认证请求上都应 426。
func TestVersionGate(t *testing.T) {
	g := &fakeGuard{result: usecase.DeviceLoginResult{Allowed: true}, revoked: map[string]bool{}}
	s := New(lanApps(), WithDeviceGuard(g), WithMinClientVersion("1.5.0"))

	// 登录：旧版本被拒（不是 401，用户才不会反复试密码）。
	w := doJSONWithHeaders(t, s, "POST", "/api/login",
		map[string]string{"username": "wh", "password": "x"},
		map[string]string{"X-RFERP-Device-ID": testDevice, "X-RFERP-Client-Version": "1.4.9"})
	if w.Code != http.StatusUpgradeRequired {
		t.Fatalf("旧版本登录应 426，得到 %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), usecase.CodeVersionTooOld) {
		t.Fatalf("body = %s", w.Body.String())
	}

	// 新版登录放行，并拿到 token。
	w = doJSONWithHeaders(t, s, "POST", "/api/login",
		map[string]string{"username": "wh", "password": "x"},
		map[string]string{"X-RFERP-Device-ID": testDevice, "X-RFERP-Client-Version": "1.5.0"})
	if w.Code != http.StatusOK {
		t.Fatalf("新版登录应 200，得到 %d %s", w.Code, w.Body.String())
	}
	var lr struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &lr)

	// 已认证请求同样要拦（客户端可能升级到一半）。
	w = doJSONWithHeaders(t, s, "GET", "/api/me", nil,
		map[string]string{"Authorization": "Bearer " + lr.Token,
			"X-RFERP-Device-ID": testDevice, "X-RFERP-Client-Version": "1.0.0"})
	if w.Code != http.StatusUpgradeRequired {
		t.Fatalf("旧版本请求应 426，得到 %d", w.Code)
	}
	w = doJSONWithHeaders(t, s, "GET", "/api/me", nil,
		map[string]string{"Authorization": "Bearer " + lr.Token,
			"X-RFERP-Device-ID": testDevice, "X-RFERP-Client-Version": "1.6.2"})
	if w.Code != http.StatusOK {
		t.Fatalf("新版请求应 200，得到 %d", w.Code)
	}
}

// 不报版本号不能绕过门禁（空串曾因"无法解析就不拦"被放行）。
func TestVersionGateRejectsMissingVersion(t *testing.T) {
	g := &fakeGuard{result: usecase.DeviceLoginResult{Allowed: true}, revoked: map[string]bool{}}
	s := New(lanApps(), WithDeviceGuard(g), WithMinClientVersion("1.5.0"))
	w := doJSONWithHeaders(t, s, "POST", "/api/login",
		map[string]string{"username": "wh", "password": "x"},
		map[string]string{"X-RFERP-Device-ID": testDevice})
	if w.Code != http.StatusUpgradeRequired {
		t.Fatalf("缺版本头应 426，得到 %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), usecase.CodeVersionTooOld) {
		t.Fatalf("body = %s", w.Body.String())
	}
}

// 单机形态不设版本门禁：那里没有"客户端版本"这回事。
func TestVersionGateOffWithoutGuard(t *testing.T) {
	apps := &fakeApps{loginUser: testWarehouseUser(), userCount: 1}
	s := New(apps, WithMinClientVersion("9.9.9"))
	w := doJSONWithHeaders(t, s, "POST", "/api/login",
		map[string]string{"username": "wh", "password": "x"},
		map[string]string{"X-RFERP-Client-Version": "1.0.0"})
	if w.Code != http.StatusOK {
		t.Fatalf("单机形态不应受版本门禁影响，得到 %d %s", w.Code, w.Body.String())
	}
}

// 非法 UTF-8 的设备名按"没带名字"处理，不要把替换字符写进设备表。
func TestInvalidUTF8DeviceNameIgnored(t *testing.T) {
	g := &fakeGuard{result: usecase.DeviceLoginResult{Allowed: true}, revoked: map[string]bool{}}
	s := New(lanApps(), WithDeviceGuard(g))
	w := doJSONWithHeaders(t, s, "POST", "/api/login",
		map[string]string{"username": "wh", "password": "x"},
		map[string]string{"X-RFERP-Device-ID": testDevice, "X-RFERP-Device-Name": "\xd6\xf7\xbb\xfa"})
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	if g.lastIn.Name != "" {
		t.Fatalf("非法 UTF-8 名字应被忽略，得到 %q", g.lastIn.Name)
	}
}

// 提前拒绝（未读 body）不能让客户端看到连接重置。
//
// 真实故障：bootstrap 令牌错误时处理器在读 body 之前就返回 403，net/http
// 关连接；客户端（urllib/Go/WebView）此时还在发 body，Windows 上收到 RST，
// 界面显示"网络错误"而不是"令牌不正确"。本机实测约 5% 触发。
func TestEarlyRejectDoesNotResetConnection(t *testing.T) {
	apps := &fakeApps{userCount: 0}
	s := New(apps, WithBootstrapToken("REALTOKEN"))
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < 30; i++ {
		req, err := http.NewRequest("POST", ts.URL+"/api/bootstrap-admin",
			strings.NewReader(`{"username":"admin","password":"x"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Bootstrap-Token", "WRONGTOKEN")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("第 %d 次请求出现连接级错误（应拿到 403）: %v", i+1, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("第 %d 次状态 = %d，期望 403", i+1, resp.StatusCode)
		}
	}
}
