//go:build windows

package mysqlfind

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"app/internal/noconsole"
	"golang.org/x/sys/windows"
)

// FindMysqldump 定位一个可用的 mysqldump.exe，返回路径与尝试过的候选。
//
// 解析顺序（命中即停）：
//  1. 配置里写的路径（完整路径，或只写命令名交给 PATH）
//  2. 已注册的 MySQL/MariaDB 服务所在 bin 目录；调用方指定了服务名时优先它
//  3. 正在运行的 mysqld.exe 同目录（便携版、手动启动、未注册服务）
//  4. %ProgramFiles% / %ProgramFiles(x86)% 下的 MySQL、MariaDB 安装目录
//  5. PATH
//
// 每个候选都要真的跑一次 `mysqldump --version`：升级前备份是 fail-closed，
// 宁可继续找，也不能把一个坏文件交回去。tried 只收录路径，不含任何凭据。
func FindMysqldump(configured, serviceHint string) (path string, tried []string) {
	for _, c := range dumpCandidates(configured, serviceHint) {
		tried = append(tried, c)
		if dumpWorks(c) {
			return c, tried
		}
	}
	return "", tried
}

func dumpCandidates(configured, serviceHint string) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		key := strings.ToLower(p)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, p)
	}

	if configured != "" {
		if strings.ContainsAny(configured, `/\`) {
			add(configured)
		} else if p, err := exec.LookPath(configured); err == nil {
			add(p)
		} else {
			// 让 tried 里能看到用户填了什么，错误提示才有指向。
			add(configured)
		}
	}

	var hinted, others []Info
	for _, info := range Detect() {
		if info.MysqldumpPath == "" {
			continue
		}
		if strings.EqualFold(info.ServiceName, serviceHint) {
			hinted = append(hinted, info)
		} else {
			others = append(others, info)
		}
	}
	for _, info := range append(hinted, others...) {
		add(info.MysqldumpPath)
	}

	for _, dir := range runningMysqldBinDirs() {
		add(filepath.Join(dir, "mysqldump.exe"))
	}
	for _, p := range programFilesDumpPaths() {
		add(p)
	}
	if p, err := exec.LookPath("mysqldump"); err == nil {
		add(p)
	}
	return out
}

// runningMysqldBinDirs 用进程快照找正在运行的 mysqld.exe。
//
// 注册表服务扫描覆盖不了便携版/用命令行起的 MySQL，而这两种恰恰最可能
// 只把 bin 放在某个目录里、从未进 PATH。
func runningMysqldBinDirs() []string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	var out []string
	for err := windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if !strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), "mysqld.exe") {
			continue
		}
		h, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pe.ProcessID)
		if openErr != nil {
			continue
		}
		buf := make([]uint16, windows.MAX_PATH)
		size := uint32(len(buf))
		queryErr := windows.QueryFullProcessImageName(h, 0, &buf[0], &size)
		windows.CloseHandle(h)
		if queryErr != nil {
			continue
		}
		out = append(out, filepath.Dir(windows.UTF16ToString(buf[:size])))
	}
	return out
}

func programFilesDumpPaths() []string {
	var out []string
	for _, root := range []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("ProgramW6432"),
	} {
		if root == "" {
			continue
		}
		for _, pat := range []string{
			filepath.Join(root, "MySQL", "*", "bin", "mysqldump.exe"),
			filepath.Join(root, "MariaDB*", "bin", "mysqldump.exe"),
		} {
			if m, err := filepath.Glob(pat); err == nil {
				out = append(out, m...)
			}
		}
	}
	return out
}

func dumpWorks(path string) bool {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return false
	}
	// --version 不需要连库，是最便宜的"这个文件确实能跑"判据。
	// 超时防止某个坏候选把启动卡住。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	noconsole.Hide(cmd)
	return cmd.Run() == nil
}
