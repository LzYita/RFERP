package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

type Role string

const (
	RoleAdmin      Role = "admin"
	RoleWarehouse  Role = "warehouse"
	RoleProduction Role = "production"
	RoleViewer     Role = "viewer"
)

func (r Role) Label() string {
	switch r {
	case RoleAdmin:
		return "管理员"
	case RoleWarehouse:
		return "仓管"
	case RoleProduction:
		return "生产"
	case RoleViewer:
		return "只读"
	}
	return string(r)
}

func RoleFromString(s string) Role {
	switch s {
	case "admin":
		return RoleAdmin
	case "warehouse":
		return RoleWarehouse
	case "production":
		return RoleProduction
	default:
		return RoleViewer
	}
}

// Roles 返回可分配的角色（用于下拉框）。
func Roles() []Role {
	return []Role{RoleAdmin, RoleWarehouse, RoleProduction, RoleViewer}
}

type Access int

const (
	AccessNone Access = iota
	AccessRead
	AccessWrite
)

const (
	ModuleDashboard = "dashboard"
	ModuleStats     = "stats"
	ModuleProducts  = "products"
	ModuleParts     = "parts"
	ModuleBOM       = "bom"
	ModuleBatch     = "batch"
	ModuleAudit     = "audit"
	ModuleBackup    = "backup"
	ModuleUsers     = "users"
)

var moduleAccess = map[string]map[Role]Access{
	ModuleDashboard: {RoleAdmin: AccessRead, RoleWarehouse: AccessRead, RoleProduction: AccessRead, RoleViewer: AccessRead},
	ModuleStats:     {RoleAdmin: AccessRead, RoleWarehouse: AccessRead, RoleProduction: AccessRead, RoleViewer: AccessRead},
	ModuleProducts:  {RoleAdmin: AccessWrite, RoleWarehouse: AccessRead, RoleProduction: AccessRead, RoleViewer: AccessRead},
	ModuleParts:     {RoleAdmin: AccessWrite, RoleWarehouse: AccessWrite, RoleProduction: AccessRead, RoleViewer: AccessRead},
	ModuleBOM:       {RoleAdmin: AccessWrite, RoleWarehouse: AccessRead, RoleProduction: AccessRead, RoleViewer: AccessRead},
	ModuleBatch:     {RoleAdmin: AccessWrite, RoleWarehouse: AccessRead, RoleProduction: AccessWrite, RoleViewer: AccessRead},
	ModuleAudit:     {RoleAdmin: AccessRead, RoleWarehouse: AccessRead, RoleProduction: AccessRead, RoleViewer: AccessRead},
	ModuleBackup:    {RoleAdmin: AccessWrite, RoleWarehouse: AccessRead, RoleProduction: AccessNone, RoleViewer: AccessNone},
	ModuleUsers:     {RoleAdmin: AccessWrite, RoleWarehouse: AccessNone, RoleProduction: AccessNone, RoleViewer: AccessNone},
}

func AccessFor(role Role, module string) Access {
	if m, ok := moduleAccess[module]; ok {
		if a, ok := m[role]; ok {
			return a
		}
	}
	return AccessNone
}

const (
	hashIter = 120000
	hashLen  = 32
	saltLen  = 16
)

// HashPassword 生成 PBKDF2-SHA256 密码哈希（格式: pbkdf2_sha256$iter$salt$hash）。
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := pbkdf2SHA256([]byte(password), salt, hashIter, hashLen)
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s",
		hashIter,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword 校验密码是否与哈希匹配。
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	key := pbkdf2SHA256([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(key, want) == 1
}

// pbkdf2SHA256 是 PBKDF2-HMAC-SHA256 的标准实现（避免依赖 go1.24 的 crypto/pbkdf2）。
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	var buf [4]byte
	dk := make([]byte, 0, numBlocks*hashLen)
	u := make([]byte, hashLen)
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		buf[0] = byte(block >> 24)
		buf[1] = byte(block >> 16)
		buf[2] = byte(block >> 8)
		buf[3] = byte(block)
		prf.Write(buf[:4])
		dk = prf.Sum(dk)
		t := dk[len(dk)-hashLen:]
		copy(u, t)
		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(u)
			u = u[:0]
			u = prf.Sum(u)
			for x := range u {
				t[x] ^= u[x]
			}
		}
	}
	return dk[:keyLen]
}

// ValidatePassword 返回密码是否满足最低要求。
func ValidatePassword(password string) error {
	if len([]rune(password)) < 4 {
		return fmt.Errorf("密码至少 4 位")
	}
	return nil
}
