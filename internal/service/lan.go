package service

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"app/internal/deviceid"
	"app/internal/license"
	"app/internal/model"
	"app/internal/repository"
	"app/internal/usecase"
)

// 局域网版的准入实现：设备登记、授权额度、会话复核。
//
// 状态缓存的口径：设备表是**事实来源**，内存里只缓存两份"读得频繁但改得少"
// 的派生值——主机指纹与已撤销设备集合，授权正文也缓一份。每次请求都查库
// 在 SQLite 上可行（单连接、本地读），但会话复核会被每个 API 调用触发，
// 一条 SELECT 会把所有请求串到一个连接上排成队（MaxOpenConns=1）。
//
// 缓存的失效点只有三个：登记主机、撤销/删除设备、导入授权。全部集中在
// 本文件，不散落到业务代码里。

// lanState 是准入缓存的全部可变状态。字段少、失效点集中，所以用一把锁。
//
// 为什么要有 TTL：授权与额度不只由本进程改——主机管理员还可能用
// `RFERP-server.exe -license` 在服务运行中导入授权（文档里就是这么写的），
// 那种跨进程改动没有回调可以通知本进程。没有 TTL 的话，缓存的"未授权"
// 会一直卡到下一次重启。
type lanState struct {
	mu        sync.RWMutex
	hostID    string
	revoked   map[string]bool
	license   *license.Claims
	licenseAt time.Time
	runtime   usecase.DeviceRuntime
	runtimeAt time.Time
}

// lanCacheTTL 决定跨进程改动最多多久被本进程看到。
//
// 15 秒是"人感觉不到"与"不用每个请求都查库"之间的取值：权限中间件每次
// 请求都会问 Runtime()（判断是否只读），无缓存时等于每个请求一次 COUNT(*)，
// 而 SQLite 只有一条连接，会把所有请求排成队。
const lanCacheTTL = 15 * time.Second

func newLANState() *lanState {
	return &lanState{revoked: map[string]bool{}}
}

var _ usecase.DeviceGuard = (*Service)(nil)

// LANGuard 返回本服务作为准入实现使用（cmd/server 把它注入 api）。
func (s *Service) LANGuard() usecase.DeviceGuard { return s }

// EnsureHostDevice 在服务端启动时登记主机（FR-25）。
//
// 主机不占授权额度，且**永远**可登录——它是唯一能在授权锁死时救场的设备
// （R7）。所以这一步失败只记日志：登记失败会让主机被当成新客户端拒掉，
// 但至少服务能起来，管理员还能用 CLI 兜底。
func (s *Service) EnsureHostDevice() (string, error) {
	id := deviceid.Fingerprint()
	rec, err := s.repo.SaveDeviceSeen(&model.Device{
		DeviceIDHash: id,
		Name:         "主机",
		Kind:         model.DeviceKindHost,
		AppVersion:   "",
		OS:           "windows",
	})
	if err != nil {
		return id, fmt.Errorf("登记主机设备: %w", err)
	}
	s.lan.mu.Lock()
	s.lan.hostID = id
	s.lan.mu.Unlock()
	log.Printf("lan: 主机设备指纹 %s（kind=%s status=%s）", id[:12]+"…", rec.Kind, rec.Status)
	return id, nil
}

// HostDeviceID 返回主机指纹；未登记时返回空串。
func (s *Service) HostDeviceID() string {
	s.lan.mu.RLock()
	defer s.lan.mu.RUnlock()
	return s.lan.hostID
}

// IsHostDevice 判断给定指纹是否为主机。
func (s *Service) IsHostDevice(deviceID string) bool {
	if deviceID == "" {
		return false
	}
	s.lan.mu.RLock()
	host := s.lan.hostID
	s.lan.mu.RUnlock()
	return host != "" && deviceID == host
}

// LoginCheck 是登录门禁的唯一判定点（FR-21/22/24/26）。
//
// 顺序很重要：先判"主机豁免"，再判授权缺失，最后才判额度。反过来写会出现
// "没导入授权时主机也进不去"——客户连导入授权的界面都到不了。
func (s *Service) LoginCheck(in usecase.DeviceLogin) (usecase.DeviceLoginResult, error) {
	if strings.TrimSpace(in.DeviceID) == "" {
		return usecase.DeviceLoginResult{Code: usecase.CodeDeviceMissing, Message: "缺少设备标识，请使用新版客户端"}, nil
	}
	host := s.IsHostDevice(in.DeviceID)

	rec, err := s.repo.GetDeviceByHash(in.DeviceID)
	if err != nil {
		return usecase.DeviceLoginResult{}, err
	}
	if rec != nil && rec.Status == model.DeviceStatusRevoked {
		s.recordEvent("login_revoked", in.Name, in.DeviceID, in.IP)
		return usecase.DeviceLoginResult{Code: usecase.CodeDeviceRevoked, Message: "该设备已被管理员撤销，请联系管理员"}, nil
	}

	state := s.licenseState()
	if host {
		// 主机永远放行，且不占额度；顺手刷新最近在线。
		//
		// 名字为空时保留已登记的：客户端正常会带计算机名，但命令行/脚本登录
		// 不带——以前那种请求会把"主机"这个名字抹成空，列表里只剩指纹前缀。
		if _, err := s.repo.SaveDeviceSeen(&model.Device{
			DeviceIDHash: in.DeviceID, Name: displayName(in.Name, rec),
			Kind: model.DeviceKindHost, AppVersion: in.Version, OS: in.OS, LastIP: in.IP,
		}); err != nil {
			log.Printf("lan: 刷新主机在线状态失败: %v", err)
		}
		return usecase.DeviceLoginResult{Allowed: true, Host: true}, nil
	}

	if !state.Licensed {
		s.recordEvent("login_no_license", in.Name, in.DeviceID, in.IP)
		return usecase.DeviceLoginResult{Code: usecase.CodeLicenseMissing,
			Message: "尚未导入授权：当前仅主机可登录"}, nil
	}

	err = s.repo.RegisterClientDevice(&model.Device{
		DeviceIDHash: in.DeviceID, Name: displayName(in.Name, rec), Kind: model.DeviceKindClient,
		AppVersion: in.Version, OS: in.OS, LastIP: in.IP,
	}, state.MaxClients)
	if errors.Is(err, repository.ErrDeviceLimit) {
		s.recordEvent("login_over_quota", in.Name, in.DeviceID, in.IP)
		return usecase.DeviceLoginResult{Code: usecase.CodeDeviceLimit,
			Message: "授权客户端数已满，请联系管理员删除闲置设备以释放名额"}, nil
	}
	if errors.Is(err, repository.ErrDeviceRevoked) {
		return usecase.DeviceLoginResult{Code: usecase.CodeDeviceRevoked,
			Message: "该设备已被管理员撤销，请联系管理员"}, nil
	}
	if err != nil {
		return usecase.DeviceLoginResult{}, err
	}
	if rec == nil {
		s.recordEvent("device_register", in.Name, in.DeviceID, in.IP)
	}
	// 新设备占用了名额：额度快照立刻失效，否则登录响应里的 used_clients
	// 会停留在一个过时的数字上，管理员会以为没登记成功。
	s.invalidateRuntime()
	return usecase.DeviceLoginResult{Allowed: true}, nil
}

// ValidateSession 在每次已认证请求上复核设备。
//
// 只查两件轻量事实：设备是否被撤销、当前设备是否就是会话绑定的那台。
// 不查额度——额度只约束"新设备能不能进来"，把老设备在每次请求上重判，
// 会让"授权数改小"变成"正在干活的人突然被踢"，与 D-9 相反。
func (s *Service) ValidateSession(deviceID string) error {
	if deviceID == "" {
		return fmt.Errorf("%s: 缺少设备标识", usecase.CodeDeviceMissing)
	}
	s.lan.mu.RLock()
	revoked := s.lan.revoked[deviceID]
	s.lan.mu.RUnlock()
	if revoked {
		return fmt.Errorf("%s: 设备已被撤销", usecase.CodeDeviceRevoked)
	}
	return nil
}

// VerifyDevice 对设备状态做一次**回库**校验。
//
// 供会话周期性复核使用：进程内的 revoked 集合只知道本进程的撤销，
// 而主机管理员可能用 CLI 在另一个进程里撤销设备。api 层每分钟查一次库，
// 把这类跨进程撤销的生效时间从"下次重启"压到"一分钟内"。
func (s *Service) VerifyDevice(deviceID string) error {
	if s.IsHostDevice(deviceID) {
		return nil
	}
	rec, err := s.repo.GetDeviceByHash(deviceID)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("%s: 设备未登记", usecase.CodeDeviceMissing)
	}
	if rec.Status == model.DeviceStatusRevoked {
		return fmt.Errorf("%s: 设备已被撤销", usecase.CodeDeviceRevoked)
	}
	return nil
}

// Runtime 返回授权与设备额度快照（设置页 + 状态条 + 只读判定）。
//
// 带 TTL：权限中间件在每个已认证请求上都会调它一次，无缓存等于每个请求
// 一次 COUNT(*)。缓存 15 秒的代价是"撤销设备后额度数字最多晚 15 秒更新"，
// 而准入判定（LoginCheck）本身不走这个缓存。
func (s *Service) Runtime() usecase.DeviceRuntime {
	now := time.Now()
	s.lan.mu.RLock()
	cached, at := s.lan.runtime, s.lan.runtimeAt
	s.lan.mu.RUnlock()
	if !at.IsZero() && now.Sub(at) < lanCacheTTL {
		return cached
	}

	st := s.licenseState()
	rt := usecase.DeviceRuntime{
		Licensed:      st.Licensed,
		MaxClients:    st.MaxClients,
		ReadOnly:      st.ReadOnly,
		PolicyMessage: st.Message,
	}
	if st.Claims != nil {
		rt.Kind = st.Claims.Kind
		rt.Customer = st.Claims.Customer
		rt.Edition = st.Claims.Edition
		rt.SupportUntil = st.Claims.SupportUntil
		rt.ExpiresAt = st.Claims.ExpiresAt
	}
	if used, err := s.repo.CountDevices(model.DeviceKindClient, ""); err == nil {
		rt.UsedClients = used
	}
	s.lan.mu.Lock()
	s.lan.runtime, s.lan.runtimeAt = rt, now
	s.lan.mu.Unlock()
	return rt
}

// invalidateRuntime 在会改变额度/授权状态的操作后立刻失效缓存。
func (s *Service) invalidateRuntime() {
	s.lan.mu.Lock()
	s.lan.runtimeAt = time.Time{}
	s.lan.licenseAt = time.Time{}
	s.lan.mu.Unlock()
}

// LicenseStatus 是 GET /api/license 的返回体。
func (s *Service) LicenseStatus() (usecase.LicenseStatus, error) {
	out := usecase.LicenseStatus{Runtime: s.Runtime()}
	rec, err := s.repo.GetLicense()
	if err != nil {
		return out, err
	}
	if rec != nil {
		out.KeyID = rec.KeyID
		out.ImportedAt = rec.ImportedAt.Format("2006-01-02 15:04:05")
		out.ImportedBy = rec.ImportedBy
	}
	return out, nil
}

// ImportLicense 校验并落盘一份授权文件（FR-23）。
//
// 校验失败**不覆盖**已生效的授权：这是客户现场最可能的误操作——把测试授权
// 或别的客户的授权文件拖进来了，不能因此把正在用的授权弄没。
func (s *Service) ImportLicense(raw []byte, actor string) (usecase.LicenseStatus, error) {
	env, err := license.ParseEnvelope(raw)
	if err != nil {
		return usecase.LicenseStatus{}, err
	}
	claims, err := license.VerifyWithKeys(env, license.PublicKeys())
	if err != nil {
		return usecase.LicenseStatus{}, err
	}
	if err := s.repo.SaveLicense(&model.LicenseRecord{
		Payload:    env.Payload,
		Signature:  env.Signature,
		KeyID:      firstNonEmpty(env.KeyID, claims.KeyID),
		ImportedAt: time.Now(),
		ImportedBy: actor,
	}); err != nil {
		return usecase.LicenseStatus{}, fmt.Errorf("保存授权: %w", err)
	}
	s.lan.mu.Lock()
	s.lan.license = claims
	s.lan.licenseAt = time.Now()
	s.lan.runtimeAt = time.Time{}
	s.lan.mu.Unlock()

	s.recordEvent("license_import", actor,
		fmt.Sprintf("%s %s 客户端数=%d", claims.Customer, claims.Kind, claims.MaxClients), "")
	return s.LicenseStatus()
}

// ListDevices 供设备管理页（FR-31）。
func (s *Service) ListDevices() ([]model.Device, error) { return s.repo.ListDevices() }

// RevokeDevice 撤销设备，会话立即失效（FR-29）。
func (s *Service) RevokeDevice(deviceID, actor string) error {
	if strings.TrimSpace(deviceID) == "" {
		return fmt.Errorf("设备标识不能为空")
	}
	// 主机不能撤销：它是授权锁死时唯一的救场设备（R7）。允许撤销等于给了
	// 一条"把自己锁在门外"的路径，而且没有任何场景需要它——主机就一台。
	protected, err := s.protectedHostDevice(deviceID)
	if err != nil {
		return err
	}
	if protected {
		return fmt.Errorf("主机设备不能撤销")
	}
	if err := s.repo.RevokeDevice(deviceID, actor); err != nil {
		if err == repository.ErrNotFound {
			return fmt.Errorf("设备不存在")
		}
		return err
	}
	s.lan.mu.Lock()
	if s.lan.revoked == nil {
		s.lan.revoked = map[string]bool{}
	}
	s.lan.revoked[deviceID] = true
	s.lan.runtimeAt = time.Time{}
	s.lan.mu.Unlock()

	s.recordEvent("device_revoke", actor, shortID(deviceID), "")
	return nil
}

// DeleteDevice 删除设备记录以释放额度（§7.4）。
func (s *Service) DeleteDevice(deviceID, actor string) error {
	// 主机记录不允许删除：删了它，主机下一次启动会重新登记成 client，
	// 白占一个授权名额（而且它本该豁免）。
	protected, err := s.protectedHostDevice(deviceID)
	if err != nil {
		return err
	}
	if protected {
		return fmt.Errorf("主机设备不能删除")
	}
	if err := s.repo.DeleteDevice(deviceID); err != nil {
		if err == repository.ErrNotFound {
			return fmt.Errorf("设备不存在")
		}
		return err
	}
	s.lan.mu.Lock()
	delete(s.lan.revoked, deviceID)
	s.lan.runtimeAt = time.Time{}
	s.lan.mu.Unlock()

	s.recordEvent("device_delete", actor, shortID(deviceID), "")
	return nil
}

func (s *Service) protectedHostDevice(deviceID string) (bool, error) {
	if s.IsHostDevice(deviceID) {
		return true, nil
	}
	rec, err := s.repo.GetDeviceByHash(deviceID)
	return rec != nil && rec.Kind == model.DeviceKindHost, err
}

// licenseState 读缓存，必要时从库加载并验签。
//
// 库里签名验不过时按"未授权"处理并在日志里说清楚：这多半意味着库被改过
// （R8），此时最安全的动作是让客户打电话，而不是继续按字段值放行。
func (s *Service) licenseState() license.State {
	now := time.Now()
	s.lan.mu.RLock()
	claims, at := s.lan.license, s.lan.licenseAt
	s.lan.mu.RUnlock()
	if now.Sub(at) >= lanCacheTTL {
		rec, err := s.repo.GetLicense()
		if err != nil {
			log.Printf("lan: 读取授权失败: %v", err)
		}
		var loaded *license.Claims
		if rec != nil {
			env := &license.Envelope{Payload: rec.Payload, Signature: rec.Signature, KeyID: rec.KeyID}
			if c, err := license.VerifyWithKeys(env, license.PublicKeys()); err != nil {
				log.Printf("lan: 库内授权验签失败（按未授权处理）: %v", err)
			} else {
				loaded = c
			}
		}
		s.lan.mu.Lock()
		s.lan.license, s.lan.licenseAt = loaded, now
		s.lan.mu.Unlock()
		claims = loaded
	}
	return license.Evaluate(claims, now)
}

// displayName 决定这次登录要写回的名字：本次请求没带名字时保留旧值。
func displayName(incoming string, existing *model.Device) string {
	if strings.TrimSpace(incoming) != "" {
		return incoming
	}
	if existing != nil {
		return existing.Name
	}
	return ""
}

func (s *Service) recordEvent(kind, actor, detail, ip string) {
	if err := s.repo.AppendSecurityEvent(&model.SecurityEvent{
		EventType: kind, Actor: actor, Detail: detail, IP: ip, CreatedAt: time.Now(),
	}); err != nil {
		log.Printf("lan: 写安全事件失败: %v", err)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12] + "…"
}
