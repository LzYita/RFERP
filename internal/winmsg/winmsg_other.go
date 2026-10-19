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

// ConfirmOK 同上：没有对话框就按"取消"处理。
func ConfirmOK(title, text string) bool {
	log.Printf("[CONFIRM-SKIPPED] %s: %s（当前平台无对话框，按取消处理）", title, text)
	return false
}

// AskRetryOpenPage 同上：没有对话框就按"取消"处理。
func AskRetryOpenPage(title, text string) (retry bool, openPage bool) {
	log.Printf("[CONFIRM-SKIPPED] %s: %s（当前平台无对话框，按取消处理）", title, text)
	return false, false
}

// OpenURL 在无原生对话框的平台上只记日志。
//
// 这里不开浏览器：调用方都是"用户点了确定才开"，而没有对话框就永远
// 没有那个确定，所以走到这儿说明是别的路径，不该悄悄弹一个浏览器出来。
func OpenURL(url string) error {
	log.Printf("[OPEN-URL-SKIPPED] %s（当前平台无原生对话框）", url)
	return nil
}
