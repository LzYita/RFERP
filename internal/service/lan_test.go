package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"app/internal/config"
	"app/internal/license"
	"app/internal/migrate"
	"app/internal/model"
	"app/internal/repository"
	"app/internal/usecase"
)

// 局域网准入的核心口径（FR-21/22/24/25/26/28/29）。
//
// 用真实 SQLite + 真实迁移：设备额度算错只会表现为"第 3 台能登录"，
// 用假 store 测不出计数口径。

func newLANTService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := testTempDir(t)
	db, err := repository.OpenSQLite(filepath.Join(dir, "lan.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.RunSQLite(db, migrate.SQLiteOptions{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := &config.Config{DataDir: dir}
	return NewWithSnapshot(repository.NewSQLite(db), nil, cfg), dir
}

func withTestLicenseKey(t *testing.T) (ed25519.PrivateKey, func()) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	prev := license.DefaultPublicKeyBase64
	license.DefaultPublicKeyBase64 = encodePub(pub)
	return priv, func() { license.DefaultPublicKeyBase64 = prev }
}

func encodePub(pub ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(pub)
}

func newDeviceID(t *testing.T, seed string) string {
	t.Helper()
	// 64 位小写十六进制，与服务端的严格校验一致。
	sum := make([]byte, 32)
	for i := range sum {
		sum[i] = seed[0]
	}
	return hex.EncodeToString(sum)
}

func TestHostAlwaysAllowedWithoutLicense(t *testing.T) {
	svc, _ := newLANTService(t)
	hostID, err := svc.EnsureHostDevice()
	if err != nil {
		t.Fatalf("EnsureHostDevice: %v", err)
	}
	res, err := svc.LoginCheck(usecase.DeviceLogin{DeviceID: hostID, Name: "主机"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Allowed || !res.Host {
		t.Fatalf("无授权时主机应放行: %+v", res)
	}

	// 客户端在无授权时被拒。
	res, err = svc.LoginCheck(usecase.DeviceLogin{DeviceID: newDeviceID(t, "a"), Name: "客户端A"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Allowed || res.Code != usecase.CodeLicenseMissing {
		t.Fatalf("无授权时应拒绝客户端: %+v", res)
	}
}

func TestQuotaAndRevoke(t *testing.T) {
	priv, restore := withTestLicenseKey(t)
	defer restore()

	svc, _ := newLANTService(t)
	if _, err := svc.EnsureHostDevice(); err != nil {
		t.Fatal(err)
	}
	raw, err := license.Sign(license.Claims{
		LicenseID: "T-1", Customer: "测试", Edition: "lan", Kind: license.KindPermanent,
		MaxClients: 1, IssuedAt: time.Now().Format(time.RFC3339),
	}, priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportLicense(raw, "tester"); err != nil {
		t.Fatalf("ImportLicense: %v", err)
	}
	if rt := svc.Runtime(); !rt.Licensed || rt.MaxClients != 1 {
		t.Fatalf("授权状态 = %+v", rt)
	}

	devA := newDeviceID(t, "a")
	devB := newDeviceID(t, "b")

	res, err := svc.LoginCheck(usecase.DeviceLogin{DeviceID: devA, Name: "A"})
	if err != nil || !res.Allowed {
		t.Fatalf("第 1 台应放行: %+v err=%v", res, err)
	}
	// 已登记设备可重复登录（额度只约束新设备）。
	res, _ = svc.LoginCheck(usecase.DeviceLogin{DeviceID: devA, Name: "A"})
	if !res.Allowed {
		t.Fatalf("已登记设备应可重复登录: %+v", res)
	}
	res, _ = svc.LoginCheck(usecase.DeviceLogin{DeviceID: devB, Name: "B"})
	if res.Allowed || res.Code != usecase.CodeDeviceLimit {
		t.Fatalf("第 2 台应被额度拒绝: %+v", res)
	}

	// 撤销 A 后：A 被拒、B 仍超额度（撤销不释放额度，删除才释放）。
	if err := svc.RevokeDevice(devA, "admin"); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	res, _ = svc.LoginCheck(usecase.DeviceLogin{DeviceID: devA, Name: "A"})
	if res.Allowed || res.Code != usecase.CodeDeviceRevoked {
		t.Fatalf("撤销后应拒绝: %+v", res)
	}
	if err := svc.ValidateSession(devA); err == nil {
		t.Fatal("撤销后会话复核应失败")
	}
	res, _ = svc.LoginCheck(usecase.DeviceLogin{DeviceID: devB, Name: "B"})
	if res.Allowed {
		t.Fatalf("撤销不等于释放额度: %+v", res)
	}

	// 删除 A 的记录 → 额度释放，B 可登记。
	if err := svc.DeleteDevice(devA, "admin"); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	res, _ = svc.LoginCheck(usecase.DeviceLogin{DeviceID: devB, Name: "B"})
	if !res.Allowed {
		t.Fatalf("释放额度后 B 应放行: %+v", res)
	}
}

// 主机记录不许删除：删了它下次启动会重新登记成 client，白占一个名额。
func TestHostCannotBeDeleted(t *testing.T) {
	svc, _ := newLANTService(t)
	hostID, err := svc.EnsureHostDevice()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteDevice(hostID, "admin"); err == nil {
		t.Fatal("主机设备应拒绝删除")
	}
}

// 授权文件被篡改时不覆盖已生效的授权（客户现场最常见的误操作）。
func TestBadLicenseDoesNotOverwrite(t *testing.T) {
	priv, restore := withTestLicenseKey(t)
	defer restore()

	svc, _ := newLANTService(t)
	good, err := license.Sign(license.Claims{
		LicenseID: "G-1", Customer: "甲", Kind: license.KindPermanent, MaxClients: 2,
	}, priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportLicense(good, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportLicense([]byte(`{"payload":"e30=","signature":"AAAA"}`), "tester"); err == nil {
		t.Fatal("伪造授权应被拒绝")
	}
	if rt := svc.Runtime(); rt.Customer != "甲" || rt.MaxClients != 2 {
		t.Fatalf("失败导入不应覆盖原授权: %+v", rt)
	}
}

// 主机不能被撤销：撤销会把唯一能在授权锁死时救场的设备锁在门外（R7）。
func TestHostCannotBeRevoked(t *testing.T) {
	svc, _ := newLANTService(t)
	hostID, err := svc.EnsureHostDevice()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeDevice(hostID, "admin"); err == nil {
		t.Fatal("主机设备应拒绝撤销")
	}
	// 撤销失败后主机仍然可登录。
	res, err := svc.LoginCheck(usecase.DeviceLogin{DeviceID: hostID, Name: "主机"})
	if err != nil || !res.Allowed || !res.Host {
		t.Fatalf("主机应仍可登录: %+v err=%v", res, err)
	}
}

// VerifyDevice 是会话周期性回库复核用的：必须能认出"另一个进程撤销了设备"。
func TestVerifyDeviceDetectsCrossProcessRevoke(t *testing.T) {
	svc, dir := newLANTService(t)
	hostID, err := svc.EnsureHostDevice()
	if err != nil {
		t.Fatal(err)
	}
	dev := newDeviceID(t, "z")
	if _, err := svc.repo.SaveDeviceSeen(&model.Device{
		DeviceIDHash: dev, Name: "Z", Kind: model.DeviceKindClient,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifyDevice(dev); err != nil {
		t.Fatalf("未撤销时应通过: %v", err)
	}
	// 模拟另一个进程直接改库（CLI 撤销）：本进程的 revoked 集合并不知道。
	other, err := repository.OpenSQLite(filepath.Join(dir, "lan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := repository.NewSQLite(other).RevokeDevice(dev, "cli"); err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifyDevice(dev); err == nil {
		t.Fatal("回库复核应发现跨进程撤销")
	}
	// 主机永远通过。
	if err := svc.VerifyDevice(hostID); err != nil {
		t.Fatalf("主机应始终通过复核: %v", err)
	}
}

// 空名字的登录不能把已登记的显示名抹掉（命令行/脚本登录不带计算机名）。
func TestEmptyNameKeepsExistingDeviceName(t *testing.T) {
	svc, _ := newLANTService(t)
	hostID, err := svc.EnsureHostDevice()
	if err != nil {
		t.Fatal(err)
	}
	// 主机默认名是「主机」；模拟一次不带名字的登录。
	if _, err := svc.LoginCheck(usecase.DeviceLogin{DeviceID: hostID}); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range list {
		if d.DeviceIDHash == hostID {
			if d.Name != "主机" {
				t.Fatalf("主机名被覆盖为 %q", d.Name)
			}
			return
		}
	}
	t.Fatal("没有找到主机记录")
}
