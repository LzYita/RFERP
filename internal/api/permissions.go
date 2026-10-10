package api

import (
	"net/http"

	"app/internal/auth"
)

// handleMyPermissions 返回当前用户在各模块上的权限档位。
//
// 目的：前端导航直接由后端权限表驱动，不在前端另抄一份矩阵。
// 抄一份必然会漂移——后端改了权限表而前端没改，用户就会看到本该消失的入口，
// 或者看不到本该出现的入口，而且要等到点下去报 403 才发现。
//
// 这只是给界面用的数据，不是安全边界：真正的判定仍在每个请求上由
// usecase.Allow 按当前角色重算。前端隐藏只用来减少误操作与困惑。
func (s *Server) handleMyPermissions(w http.ResponseWriter, r *http.Request) {
	u, ok := s.userFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	role := auth.RoleFromString(u.Role)
	out := make(map[string]string, len(auth.AllModules()))
	for _, m := range auth.AllModules() {
		out[m] = accessWord(auth.AccessFor(role, m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"modules": out})
}

// accessWord 把权限档位翻译成前端使用的字。
//
// 不用整数：整数含义若调整而前端没跟上，会静默变成「错误的权限」而不是
// 明显的错误。字符串还能让前端区分「无」与「只读」——前者整个入口不显示，
// 后者显示但不给写操作。
func accessWord(a auth.Access) string {
	switch a {
	case auth.AccessWrite:
		return "write"
	case auth.AccessRead:
		return "read"
	default:
		return "none"
	}
}
