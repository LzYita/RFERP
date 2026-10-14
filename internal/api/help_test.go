package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"app/internal/auth"
	"app/internal/help"
)

// helpPayload 是 /api/help 的线协议形状，与前端 HelpPayload 对应。
//
// 这里按**字段名**反序列化而不是直接复用 handleHelp 里的匿名结构：
// 字段名写错时前端只会读到零值（空字符串 / 空数组），页面变成空白，
// 而服务端测试全绿。这个结构把形状钉在测试里。
type helpPayload struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	Guide          string `json:"guide"`
	FAQ            string `json:"faq"`
	Versions       []struct {
		Version     string `json:"version"`
		PublishedAt string `json:"publishedAt"`
		Sections    []struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		} `json:"sections"`
	} `json:"versions"`
}

// 设置屏三个页签的原料必须一次给全：少任何一份，对应页签就是空白页。
func TestHelpServesAllThreeTabs(t *testing.T) {
	s := New(&sessionApps{}, WithVersion("9.9.9"))
	// 只读账号也要能看帮助：它是界面上的文档，不是业务数据。
	tok := issueAs(t, s, auth.RoleViewer)

	w := postJSON(t, s, "GET", "/api/help", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got helpPayload
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.CurrentVersion != "9.9.9" {
		t.Errorf("currentVersion=%q，期望进程自报的 9.9.9（前端据此显示版本，不能是写死的常量）",
			got.CurrentVersion)
	}
	if got.LatestVersion == "" || got.LatestVersion != help.LatestVersion() {
		t.Errorf("latestVersion=%q，期望内置更新说明里的 %q", got.LatestVersion, help.LatestVersion())
	}
	if got.Guide == "" {
		t.Error("guide 为空——使用指南页签会显示成空白")
	}
	if got.FAQ == "" {
		t.Error("faq 为空——常见问题页签会显示成空白")
	}
	if len(got.Versions) == 0 {
		t.Fatal("versions 为空——更新说明页签会显示成空白")
	}
	if got.Versions[0].Version != got.LatestVersion {
		t.Errorf("versions[0]=%q，最新版本是 %q —— 列表必须从新到旧（前端默认展开第一条）",
			got.Versions[0].Version, got.LatestVersion)
	}
	// 小节必须真的切出来：只回原始 notes 的话，前端要自己再实现一份 ParseNotes，
	// 两边对同一份文本迟早切出不同的段落。
	hasSections := false
	for _, v := range got.Versions {
		for _, sec := range v.Sections {
			if sec.Body != "" {
				hasSections = true
			}
		}
	}
	if !hasSections {
		t.Error("所有版本都没有切出正文小节——ParseNotes 没有接上")
	}
}

// 帮助内容不要求任何模块权限，但**要求有会话**：它是登录后界面的内容。
func TestHelpNeedsASession(t *testing.T) {
	s := New(&sessionApps{})
	w := postJSON(t, s, "GET", "/api/help", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("无会话访问 /api/help 返回 %d，期望 401", w.Code)
	}
}
