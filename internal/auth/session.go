package auth

import (
	"encoding/json"
	"os"
	"path/filepath"

	"app/internal/model"
	"app/internal/secret"
)

var current *model.User

func Current() *model.User { return current }

func SetCurrent(u *model.User) { current = u }

func Logout() {
	current = nil
	ClearRemembered()
}

func CurrentRole() Role {
	if current == nil {
		return RoleViewer
	}
	return RoleFromString(current.Role)
}

// OperatorName 返回用于审计记录的操作人名称。
func OperatorName() string {
	if current == nil {
		return "未知"
	}
	if current.DisplayName != nil && *current.DisplayName != "" {
		return *current.DisplayName
	}
	return current.Username
}

func CanRead(module string) bool {
	if current == nil {
		return false
	}
	return AccessFor(CurrentRole(), module) >= AccessRead
}

func CanWrite(module string) bool {
	if current == nil {
		return false
	}
	return AccessFor(CurrentRole(), module) == AccessWrite
}

// ---- 记住登录（DPAPI 加密存本机） ----

type remembered struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func sessionPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return "session.json"
	}
	return filepath.Join(dir, "RFERP", "session.json")
}

func SaveRemembered(username, password string) error {
	b, err := json.Marshal(remembered{Username: username, Password: password})
	if err != nil {
		return err
	}
	enc, err := secret.Protect(b)
	if err != nil {
		return err
	}
	p := sessionPath()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	return os.WriteFile(p, enc, 0600)
}

func LoadRemembered() (string, string, bool) {
	b, err := os.ReadFile(sessionPath())
	if err != nil {
		return "", "", false
	}
	dec, err := secret.Unprotect(b)
	if err != nil {
		return "", "", false
	}
	var r remembered
	if err := json.Unmarshal(dec, &r); err != nil || r.Username == "" {
		return "", "", false
	}
	return r.Username, r.Password, true
}

func ClearRemembered() {
	os.Remove(sessionPath())
}
