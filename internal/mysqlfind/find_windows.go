//go:build windows

package mysqlfind

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type Info struct {
	ServiceName   string
	DisplayName   string
	BinDir        string
	MysqldumpPath string
	Running       bool
}

func Detect() []Info {
	var out []Info
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Services`,
		registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		return out
	}
	defer k.Close()

	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return out
	}
	for _, name := range names {
		lname := strings.ToLower(name)
		if !strings.Contains(lname, "mysql") && !strings.Contains(lname, "mariadb") {
			continue
		}
		sk, err := registry.OpenKey(k, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		img, _, imgErr := sk.GetStringValue("ImagePath")
		display, _, _ := sk.GetStringValue("DisplayName")
		sk.Close()
		if imgErr != nil {
			continue
		}
		binDir := filepath.Dir(parseImagePath(img))
		info := Info{
			ServiceName: name,
			DisplayName: display,
			BinDir:      binDir,
			Running:     serviceRunning(name),
		}
		md := filepath.Join(binDir, "mysqldump.exe")
		if _, err := os.Stat(md); err == nil {
			info.MysqldumpPath = md
		}
		out = append(out, info)
	}
	return out
}

func ProbePort() int {
	for _, p := range []int{3306, 3307, 3308, 3309} {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(p)), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return p
		}
	}
	return 0
}

// StartService 启动指定的 Windows 服务。
//
// 走服务控制管理器（SCM）API 而不是 `net start` / `sc start`：
// 那两个命令的输出是控制台代码页（中文系统为 GBK/CP936），此前直接当 UTF-8
// 使用，导致「拒绝访问。」之类的错误信息在界面和日志里都显示为乱码。
// SCM API 返回的是 Win32 错误码，没有编码问题。
func StartService(name string) error {
	err := startServiceSCM(name)
	if err == nil {
		return nil
	}
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return &StartError{Name: name, Err: err, NeedsAdmin: true}
	}
	return &StartError{Name: name, Err: err}
}

// StartServiceFlag 是提权子进程用于识别「本次启动只为拉起 MySQL 服务、随后立即退出」
// 的命令行参数前缀，形如 --rferp-start-mysql-service=MySQL80。
//
// 用它而不是 net.exe / sc.exe，是为了 UAC 弹窗的可读性：UAC 显示的是可执行文件的
// 描述信息，net.exe 会显示成「网络命令」，用户完全看不出这是在做什么；
// 提权重启自身则显示应用自己的名字，语义明确也更可信。
const StartServiceFlag = "--rferp-start-mysql-service"

// StartServiceElevated 通过 UAC 提权启动服务。
//
// 启动 Windows 服务需要管理员权限，普通权限下 SCM 调用会返回 ERROR_ACCESS_DENIED。
// 这里不要求整个应用以管理员身份运行，而是**提权重启自身**并带上 StartServiceFlag，
// 由那个子进程启动服务后立即退出（见 cmd/desktop/main.go 的处理）。
// 因此正常使用时不会出现提权提示——只有 MySQL 确实没在运行时才会弹一次 UAC。
//
// 返回 nil 只表示已发起提权启动，不代表服务一定起来；调用方应轮询端口确认。
// 用户取消 UAC 时返回 ERROR_CANCELLED。
func StartServiceElevated(name string) error {
	self, err := os.Executable()
	if err != nil {
		return &StartError{Name: name, Err: err, NeedsAdmin: true}
	}

	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(self)
	if err != nil {
		return err
	}
	args, err := windows.UTF16PtrFromString(StartServiceFlag + "=" + name)
	if err != nil {
		return err
	}
	// 复用当前工作目录：服务由 SCM 启动，与工作目录无关。
	cwd, err := windows.UTF16PtrFromString("")
	if err != nil {
		return err
	}
	if err := windows.ShellExecute(0, verb, file, args, cwd, windows.SW_HIDE); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return &StartError{Name: name, Err: ErrStartCancelled}
		}
		return &StartError{Name: name, Err: err, NeedsAdmin: true}
	}
	return nil
}

// ErrStartCancelled 表示用户在 UAC 弹窗里点了「否」。
var ErrStartCancelled = errors.New("用户取消了权限确认，未启动 MySQL 服务")

// RunServiceStartIfRequested 供主程序在启动最早期调用：
// 若命令行带有 StartServiceFlag，就只启动该服务然后退出，不进入任何界面逻辑。
// 提权子进程专用。
func RunServiceStartIfRequested(args []string) (handled bool, err error) {
	for _, a := range args {
		if strings.HasPrefix(a, StartServiceFlag+"=") {
			name := strings.TrimPrefix(a, StartServiceFlag+"=")
			if name == "" {
				return true, &StartError{Name: name}
			}
			return true, StartService(name)
		}
	}
	return false, nil
}

// startServiceSCM 用 SCM API 启动服务。
func startServiceSCM(name string) error {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return err
	}
	defer windows.CloseServiceHandle(scm)

	namep, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	// 只申请 SERVICE_START：非管理员会在这里拿到 ERROR_ACCESS_DENIED，
	// 从而可以据此判断是否需要提权，而不必等启动失败。
	h, err := windows.OpenService(scm, namep, windows.SERVICE_START)
	if err != nil {
		return err
	}
	defer windows.CloseServiceHandle(h)

	return windows.StartService(h, 0, nil)
}

// StartError 是启动服务失败的类型化错误。
type StartError struct {
	Name       string
	Err        error
	NeedsAdmin bool
}

func (e *StartError) Error() string {
	if e.NeedsAdmin {
		return "启动 MySQL 服务「" + e.Name + "」需要管理员权限。\n\n" +
			"可以让 RFERP 提权后重试，或右键 RFERP.exe 选择「以管理员身份运行」。"
	}
	if e.Err != nil {
		return "启动 MySQL 服务「" + e.Name + "」失败：" + e.Err.Error()
	}
	return "启动 MySQL 服务「" + e.Name + "」失败。"
}

func (e *StartError) Unwrap() error { return e.Err }

func serviceRunning(name string) bool {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false
	}
	defer windows.CloseServiceHandle(scm)
	namep, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false
	}
	h, err := windows.OpenService(scm, namep, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return false
	}
	defer windows.CloseServiceHandle(h)
	var st windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(h, &st); err != nil {
		return false
	}
	return st.CurrentState == windows.SERVICE_RUNNING
}

func parseImagePath(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, `"`) {
		if i := strings.Index(s[1:], `"`); i >= 0 {
			return s[1 : 1+i]
		}
	}
	if i := strings.Index(s, " --"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	if i := strings.Index(s, " -"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
