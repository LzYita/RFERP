//go:build !windows

package winmsg

import "log"

func Error(title, text string) {
	log.Printf("[ERROR] %s: %s", title, text)
}

func Info(title, text string) {
	log.Printf("[INFO] %s: %s", title, text)
}
