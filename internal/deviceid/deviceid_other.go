//go:build !windows

package deviceid

import (
	"net"
	"os"
)

// 非 Windows 只用于开发与 CI：没有 MachineGuid，用 hostname 兜底。
// 生产目标平台是 Windows 10/11 64 位（方案 §8.1），这里不追求等价语义。
func machineGUID() string {
	h, _ := os.Hostname()
	return h
}

func systemDiskSerial() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, i := range ifaces {
		if i.Flags&net.FlagLoopback != 0 || len(i.HardwareAddr) == 0 {
			continue
		}
		return i.HardwareAddr.String()
	}
	return ""
}
