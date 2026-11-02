// Package deviceid 计算客户端设备指纹（局域网版 D-8）。
//
// 口径：SHA-256(MachineGuid + 系统盘卷序列号 + CPU 标识)。
//
// 为什么是这三样而不是网卡 MAC：MAC 会被虚拟网卡、USB 网卡、随机化 MAC
// 改掉，重装一次系统换一块网卡就让客户"设备被拒"。MachineGuid 重装系统会变、
// 换主板会变，这正是我们要的语义——一次授权绑定一台可识别的机器。
//
// 三样里任何一样取不到都不影响结果：取不到的就用固定占位串填，
// 宁可指纹的区分度低一点，也不能因为某台机器读不到 WMI 就完全无法登记。
package deviceid

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"runtime"
	"strings"
)

// Parts 是可解释的指纹来源，便于排障时判断"为什么这台机器换了指纹"。
type Parts struct {
	MachineGUID  string
	SystemSerial string
	CPU          string
}

// Fingerprint 返回 64 位小写十六进制哈希。
func Fingerprint() string {
	return FingerprintOf(collect())
}

// FingerprintOf 只做哈希，便于测试与"给定部件算一次"的场景。
func FingerprintOf(p Parts) string {
	join := func(s string) string {
		s = strings.TrimSpace(strings.ToLower(s))
		if s == "" {
			return "-"
		}
		return s
	}
	sum := sha256.Sum256([]byte(join(p.MachineGUID) + "|" + join(p.SystemSerial) + "|" + join(p.CPU)))
	return hex.EncodeToString(sum[:])
}

// collect 汇总三样来源；平台差异在 platform_*.go 里实现。
func collect() Parts {
	return Parts{
		MachineGUID:  machineGUID(),
		SystemSerial: systemDiskSerial(),
		CPU:          cpuIdentifier(),
	}
}

// cpuIdentifier 取 CPU 标识。PROCESSOR_IDENTIFIER 在 Windows 上由系统提供，
// 内容形如 "Intel64 Family 6 Model 154 Stepping 3, GenuineIntel"。
func cpuIdentifier() string {
	if v := os.Getenv("PROCESSOR_IDENTIFIER"); v != "" {
		return v
	}
	return runtime.GOARCH
}
