package main

import "testing"

// 阶段 A 是单机桌面工具：窗口必须自己适应屏幕，而不是假设用户有一块大屏。
// 原先固定 1360×860，在 1366×768 的笔记本上高度已经超屏，标题栏会被顶没。
func TestFitWindowCapsToWorkArea(t *testing.T) {
	cases := []struct {
		name         string
		workW, workH int
		wantW, wantH int
		wantMaxW     int
		wantMaxH     int
	}{
		{"大屏不放大", 2560, 1440, 1360, 860, 2304, 1296},
		{"1080p 保持期望值", 1920, 1040, 1360, 860, 1728, 936},
		{"1366x768 笔记本收敛", 1366, 728, 1360, 860, 1229, 655},
		{"超小屏不超出工作区", 1024, 700, 1360, 860, 1024, 700},
		{"竖屏不产生负值", 768, 1366, 1360, 860, 768, 1229},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotW, gotH := fitTo(c.workW, c.workH, 1360, 860)

			if gotW > c.wantMaxW || gotH > c.wantMaxH {
				t.Errorf("窗口 %dx%d 超出工作区的 90%%（上限 %dx%d）",
					gotW, gotH, c.wantMaxW, c.wantMaxH)
			}
			if gotW < 640 || gotH < 480 {
				t.Errorf("窗口 %dx%d 小到无法使用", gotW, gotH)
			}
			if gotW <= 0 || gotH <= 0 {
				t.Errorf("窗口尺寸非正: %dx%d", gotW, gotH)
			}
		})
	}
}

// 大屏上不应把窗口拉大——用户没要求最大化，就不该替他最大化。
func TestFitWindowDoesNotEnlargeOnBigScreens(t *testing.T) {
	gotW, gotH := fitTo(2560, 1440, 1360, 860)
	if gotW != 1360 || gotH != 860 {
		t.Errorf("大屏上得到 %dx%d，应保持 1360x860", gotW, gotH)
	}
}

// 工作区低于可用下限时不再强行撑到下限——那只会让窗口超出屏幕。
func TestFitWindowRespectsTinyWorkArea(t *testing.T) {
	gotW, gotH := fitTo(800, 600, 1360, 860)
	if gotW > 800 || gotH > 600 {
		t.Errorf("工作区 800x600 却得到 %dx%d，窗口会超出屏幕", gotW, gotH)
	}
}

// 真实环境取到的必须是正数——取不到时要有兜底，不能返回 0 宽窗口。
func TestWorkAreaIsUsable(t *testing.T) {
	w, h := workArea()
	if w <= 0 || h <= 0 {
		t.Fatalf("workArea() 返回 %dx%d", w, h)
	}
	winW, winH := fitTo(w, h, 1360, 860)
	t.Logf("当前工作区 %dx%d -> 窗口 %dx%d", w, h, winW, winH)
}
