//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// 验签必须**真的**区分签名与未签名，否则它就是一段看着安全、实际恒真的代码。
//
// 两条都要有：
//   - 未签名 → 必须拒绝（证明检查不是恒过）；
//   - Microsoft 签名 → 必须通过（证明检查不是恒拒，否则所有人都装不了）。
//
// 第二条依赖机器上有已签名的微软二进制（WebView2 运行时随 Edge 分发）；
// 找不到就跳过，而不是伪造一个"通过"。
func TestVerifyAuthenticodeRejectsUnsigned(t *testing.T) {
	// 测试二进制本身没有签名，正好当作反面样本。
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyAuthenticode(exe); err == nil {
		t.Fatal("未签名的文件通过了验签 —— 这个检查等于没有")
	}
}

func TestVerifyAuthenticodeRejectsCorruptSignature(t *testing.T) {
	// 更贴近真实攻击：拿一个已签名文件，改掉最后一个字节（证书表之外的区域
	// 通常不会被校验，所以改文件头附近的长度字段更容易破坏签名）。
	// 这里用简单办法：复制一个已签名文件并截断，哈希与签名必然对不上。
	signed := findSignedMicrosoftBinary(t)
	if signed == "" {
		t.Skip("机器上没有可用的微软签名二进制")
	}
	data, err := os.ReadFile(signed)
	if err != nil {
		t.Skipf("读取样本失败: %v", err)
	}
	if len(data) < 2 {
		t.Skip("样本太小")
	}
	// 截掉尾部 4KB：Authenticode 摘要覆盖整个文件，任何改动都会失配。
	cut := len(data) - 4096
	if cut <= 0 {
		t.Skip("样本太小")
	}
	dst := filepath.Join(t.TempDir(), "truncated.exe")
	if err := os.WriteFile(dst, data[:cut], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyAuthenticode(dst); err == nil {
		t.Fatal("被截断的已签名文件通过了验签 —— 摘要没有被真正校验")
	}
}

func TestVerifyAuthenticodeAcceptsMicrosoftBinary(t *testing.T) {
	signed := findSignedMicrosoftBinary(t)
	if signed == "" {
		t.Skip("机器上没有可用的微软签名二进制")
	}
	if err := verifyAuthenticode(signed); err != nil {
		t.Fatalf("微软签名的 %s 没通过验签（%v）—— 这会让所有用户装不了 WebView2",
			filepath.Base(signed), err)
	}
}

// findSignedMicrosoftBinary 找一个已签名的微软二进制作正面样本。
func findSignedMicrosoftBinary(t *testing.T) string {
	t.Helper()
	roots := []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "EdgeWebView", "Application"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "EdgeWebView", "Application"),
	}
	for _, root := range roots {
		matches, _ := filepath.Glob(filepath.Join(root, "*", "msedgewebview2.exe"))
		for _, m := range matches {
			if _, err := os.Stat(m); err == nil {
				return m
			}
		}
	}
	// 退路：系统目录里的 notepad.exe（微软签名）。
	if p := filepath.Join(os.Getenv("SystemRoot"), "System32", "notepad.exe"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
