//go:build windows

package deviceid

import (
	"log"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

// machineGUID 读 HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid。
//
// 读 64 位视图：32 位进程在 64 位系统上默认被重定向到 WOW6432Node，
// 而 MachineGuid 只在 64 位视图里。用 KEY_WOW64_64KEY 保证两边读到同一个值。
func machineGUID() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		log.Printf("deviceid: 读 MachineGuid 失败（指纹区分度降低）: %v", err)
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue("MachineGuid")
	if err != nil {
		log.Printf("deviceid: 读 MachineGuid 值失败: %v", err)
		return ""
	}
	return v
}

var procGetVolumeInformation = syscall.NewLazyDLL("kernel32.dll").NewProc("GetVolumeInformationW")

// systemDiskSerial 读系统盘的卷序列号。
//
// 取 %SystemDrive%（通常是 C:）。卷序列号是格式化时生成的 32 位值，
// 重装系统后可能变化——这与"重装后需要管理员撤销旧设备"的既定流程一致（R6）。
func systemDiskSerial() string {
	drive := strings.TrimSpace(getenvDefault("SystemDrive", "C:"))
	if !strings.HasSuffix(drive, `\`) {
		drive += `\`
	}
	root, err := syscall.UTF16PtrFromString(drive)
	if err != nil {
		return ""
	}
	var volumeSerial uint32
	ok, _, callErr := procGetVolumeInformation.Call(
		uintptr(unsafe.Pointer(root)),
		0, 0,
		uintptr(unsafe.Pointer(&volumeSerial)),
		0, 0,
		0, 0,
	)
	if ok == 0 {
		log.Printf("deviceid: 读卷序列号失败（%v）", callErr)
		return ""
	}
	return hex32(volumeSerial)
}

func hex32(v uint32) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		out[i] = digits[v&0xf]
		v >>= 4
	}
	return string(out)
}

func getenvDefault(key, def string) string {
	if v, ok := syscall.Getenv(key); ok && v != "" {
		return v
	}
	return def
}
