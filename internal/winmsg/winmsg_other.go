//go:build !windows

package winmsg

import "log"

func Error(title, text string) {
	log.Printf("[ERROR] %s: %s", title, text)
}

func Info(title, text string) {
	log.Printf("[INFO] %s: %s", title, text)
}

// Confirm 在无原生对话框的平台上不询问，直接拒绝。
//
// 取"否"而不是"是"：这是更新替换程序这类不可逆动作前的闸门，
// 没有真实对话框时假装用户同意了，比不做更糟。
func Confirm(title, text string) bool {
	log.Printf("[CONFIRM-SKIPPED] %s: %s（当前平台无对话框，按拒绝处理）", title, text)
	return false
}
