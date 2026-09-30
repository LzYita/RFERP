//go:build !windows

package mysqlfind

import "errors"

type Info struct {
	ServiceName   string
	DisplayName   string
	BinDir        string
	MysqldumpPath string
	Running       bool
}

func Detect() []Info { return nil }

func ProbePort() int { return 0 }

// StartService 在非 Windows 平台没有服务概念，桩实现保持接口可用。
func StartService(name string) error { return nil }

// StartServiceElevated 非 Windows 平台无法提权启动服务。
func StartServiceElevated(name string) error {
	return &StartError{Name: name, Err: errors.New("非 Windows 平台不支持启动系统服务")}
}

// StartServiceFlag 需与 Windows 实现保持一致，见 find_windows.go。
const StartServiceFlag = "--rferp-start-mysql-service"

// RunServiceStartIfRequested 非 Windows 平台不会收到提权参数。
func RunServiceStartIfRequested(args []string) (bool, error) { return false, nil }

// ErrStartCancelled 见 find_windows.go。
var ErrStartCancelled = errors.New("用户取消了权限确认，未启动 MySQL 服务")

// StartError 见 find_windows.go。非 Windows 下不会有 SCM 错误码，
// NeedsAdmin 恒为 false。
type StartError struct {
	Name       string
	Err        error
	NeedsAdmin bool
}

func (e *StartError) Error() string {
	if e.Err != nil {
		return "启动 MySQL 服务「" + e.Name + "」失败：" + e.Err.Error()
	}
	return "启动 MySQL 服务「" + e.Name + "」失败。"
}

func (e *StartError) Unwrap() error { return e.Err }
