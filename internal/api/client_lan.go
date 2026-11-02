package api

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"app/internal/model"
	"app/internal/usecase"
)

// Client 的局域网管理面：把授权/设备端点转发到主机。
//
// 为什么必须实现：客户端模式下的 apps 就是 api.Client，而设置页的
// 「授权与设备」通过 /api/license、/api/devices 访问。不转发的话，管理员
// 在客户端上看到的永远是"当前部署形态没有授权管理"——而他就是那个要在
// 客户端上撤销设备、导入新授权的人（FR-37）。
//
// 这里只做透传，不做任何判定：额度、撤销、验签都在主机上算。客户端本地
// 判一遍只会制造第二套口径，而两套口径迟早对不上。

var _ usecase.LANAdmin = (*Client)(nil)

// SetupInfo 问主机"有没有初始化、要不要令牌"（首启向导用）。
func (c *Client) SetupInfo() (bool, bool, error) {
	var out struct {
		NeedsSetup    bool `json:"needsSetup"`
		TokenRequired bool `json:"tokenRequired"`
	}
	if err := c.do(http.MethodGet, "/api/setup/status", nil, &out); err != nil {
		return false, false, err
	}
	return out.NeedsSetup, out.TokenRequired, nil
}

// EnsureHostDevice 在客户端上没有意义：主机登记由服务端自己做。
func (c *Client) EnsureHostDevice() (string, error) { return "", nil }

func (c *Client) LicenseStatus() (usecase.LicenseStatus, error) {
	var out usecase.LicenseStatus
	if err := c.do(http.MethodGet, "/api/license", nil, &out); err != nil {
		return usecase.LicenseStatus{}, err
	}
	return out, nil
}

func (c *Client) ImportLicense(raw []byte, _ string) (usecase.LicenseStatus, error) {
	// 服务端接受 {license: "<文件原文>"}。原文里的换行、中文都要原样带过去，
	// 所以按字符串放进 JSON，而不是再 base64 一层——服务端解码逻辑只认文本。
	var out usecase.LicenseStatus
	in := map[string]string{"license": string(raw)}
	if err := c.do(http.MethodPost, "/api/license/import", in, &out); err != nil {
		return usecase.LicenseStatus{}, err
	}
	return out, nil
}

func (c *Client) ListDevices() ([]model.Device, error) {
	var out struct {
		Devices []model.Device `json:"devices"`
	}
	if err := c.do(http.MethodGet, "/api/devices", nil, &out); err != nil {
		return nil, err
	}
	return out.Devices, nil
}

func (c *Client) RevokeDevice(deviceID, _ string) error {
	return c.do(http.MethodPost, "/api/devices/revoke",
		map[string]string{"device_id": deviceID}, nil)
}

func (c *Client) DeleteDevice(deviceID, _ string) error {
	return c.do(http.MethodPost, "/api/devices/delete",
		map[string]string{"device_id": deviceID}, nil)
}

// StreamBackup 以流的方式取主机上的备份文件（FR-34）。
//
// 返回 *http.Response 而不是 []byte：备份可能上百 MB，整块读进内存会在
// 客户端制造一次没必要的峰值。调用方负责关闭 Body。
func (c *Client) StreamBackup(name string) (*http.Response, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("请先选择备份文件")
	}
	// 先在客户端挡一次路径形态：主机端还会再校验，但把明显非法的名字
	// 发过去只会换回一个 400，没有意义。
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return nil, fmt.Errorf("文件名不合法")
	}
	path := "/api/backup/download?name=" + url.QueryEscape(name)
	req, err := c.newRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, responseError(resp.StatusCode, raw)
	}
	return resp, nil
}

// ListBackupsRemote 列出主机上的备份文件（客户端备份页用）。
//
// 不做本地兜底：客户端本机的"备份目录"里没有业务数据，列出它只会让用户
// 以为备份转移到了这台机器上。
func (c *Client) ListBackupsRemote() ([]backupEntry, error) {
	var out struct {
		Files []backupEntry `json:"files"`
	}
	if err := c.do(http.MethodGet, "/api/backup/files", nil, &out); err != nil {
		return nil, err
	}
	return out.Files, nil
}

// CreateBackupStream 在主机上生成一份手动备份并把它流式取回。
//
// 与 BackupDatabase 的区别：那个旧方法期望主机回 {path}，而端点早就改成
// 直接回文件流了——客户端模式下点「一键备份」会因此必然失败（JSON 解析二进制）。
// 新流程按流拷贝，主机与客户端走同一条路径。
func (c *Client) CreateBackupStream() (*http.Response, error) {
	req, err := c.newRequest(http.MethodPost, "/api/backup", map[string]any{})
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, responseError(resp.StatusCode, raw)
	}
	return resp, nil
}

// DownloadBackup 是 StreamBackup 的便捷封装，供需要在内存里处理的调用方使用。
// 大备份请直接用 StreamBackup。
func (c *Client) DownloadBackup(name string) ([]byte, error) {
	resp, err := c.StreamBackup(name)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
