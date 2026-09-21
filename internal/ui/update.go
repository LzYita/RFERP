package ui

import (
	"fmt"
	"log"
	"os"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"app/internal/update"
)

// StartUpdateCheck runs an update check in the background and, when a newer
// signed version is found, asks the user whether to install it.
func StartUpdateCheck(w fyne.Window, manifestURL, currentVersion string) {
	go func() {
		time.Sleep(3 * time.Second)
		checker, err := update.NewChecker(manifestURL, currentVersion)
		if err != nil {
			log.Printf("update check disabled: %v", err)
			return
		}
		m, err := checker.Check()
		if err != nil {
			log.Printf("update check failed: %v", err)
			return
		}
		if m == nil {
			return
		}
		log.Printf("update available: %s (current %s)", m.Version, currentVersion)
		msg := fmt.Sprintf("发现新版本 %s（当前 %s）", m.Version, currentVersion)
		if m.Notes != "" {
			msg += "\n\n" + m.Notes
		}
		msg += "\n\n是否立即更新？"
		dialog.ShowConfirm("软件更新", msg, func(ok bool) {
			if !ok {
				return
			}
			downloadAndApply(w, checker, m)
		}, w)
	}()
}

func downloadAndApply(w fyne.Window, checker *update.Checker, m *update.Manifest) {
	bar := widget.NewProgressBar()
	label := widget.NewLabel("正在下载更新，请勿关闭程序...")
	label.Wrapping = fyne.TextWrapWord
	label.Alignment = fyne.TextAlignCenter

	d := dialog.NewCustomWithoutButtons("正在更新", container.NewVBox(label, bar), w)
	d.Resize(fyne.NewSize(580, 180))
	d.Show()

	go func() {
		start := time.Now()
		zipPath, err := checker.Download(m, os.TempDir(), func(done, total int64) {
			if total > 0 {
				bar.SetValue(float64(done) / float64(total))
			}
			secs := time.Since(start).Seconds()
			speed := int64(0)
			if secs > 0.5 {
				speed = int64(float64(done) / secs)
			}
			if total > 0 {
				label.SetText(fmt.Sprintf("正在下载更新（%s / %s，%s/s），请勿关闭程序...",
					humanSize(done), humanSize(total), humanSize(speed)))
			} else {
				label.SetText(fmt.Sprintf("正在下载更新（已下载 %s），请勿关闭程序...", humanSize(done)))
			}
		})
		if err != nil {
			d.Hide()
			log.Printf("update download failed: %v", err)
			dialog.ShowError(fmt.Errorf("更新失败：%v", err), w)
			return
		}
		if err := update.ApplyAndRestart(zipPath); err != nil {
			d.Hide()
			log.Printf("update apply failed: %v", err)
			dialog.ShowError(fmt.Errorf("安装更新失败：%v", err), w)
			return
		}
		os.Exit(0)
	}()
}

func humanSize(n int64) string {
	if n < 0 {
		n = 0
	}
	const mb = 1024 * 1024
	if n >= mb {
		return fmt.Sprintf("%.1f MB", float64(n)/mb)
	}
	return fmt.Sprintf("%.0f KB", float64(n)/1024)
}
