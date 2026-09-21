//go:build windows

package mysqlfind

import (
	"net"
	"os"
	"os/exec"
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

func StartService(name string) error {
	out, err := exec.Command("net", "start", name).CombinedOutput()
	if err != nil {
		out2, err2 := exec.Command("sc", "start", name).CombinedOutput()
		if err2 != nil {
			return &startError{msg: strings.TrimSpace(string(out)) + "\n" + strings.TrimSpace(string(out2))}
		}
	}
	return nil
}

type startError struct{ msg string }

func (e *startError) Error() string {
	if e.msg == "" {
		return "启动服务失败（可能需要管理员权限）"
	}
	return e.msg
}

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
