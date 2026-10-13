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
	// ModuleSystem 是**应用自身**的维护：检查/下载/替换可执行文件。
	//
	// 单独一个模块，而不是挂到 Backup 或 Users 下面：备份与用户管理都有
	// 合理的非管理员档位，而「替换正在运行的程序」不是——那等于把程序控制权
	// 交给任何登录的人。写权限只给管理员。
	ModuleSystem = "system"
)

// moduleGrant 是一个模块及其四个角色的档位。
type moduleGrant struct {
	module string
	access map[Role]Access
}

// modules 是权限矩阵的**唯一来源**：模块清单、逐角色档位、权限端点下发给前端的
// 键，全部从这一张表派生。
//
// 为什么不是「一个 map + 一份 AllModules() 列表」：那正是出过的事故。
// 新增 ModuleSystem 时只往 map 里加了条目，忘了同步列表，于是
// /api/me/permissions 少返回一个模块，前端永远看不到它——而代码注释当时还写着
// 「模块增减必须同时反映到权限表、导航与界面上，从一处派生才不会漂移」。
// 把清单做成这张表的键之后，加模块不可能只加到一半。
var modules = []moduleGrant{
	{ModuleDashboard, map[Role]Access{RoleAdmin: AccessRead, RoleWarehouse: AccessRead, RoleProduction: AccessRead, RoleViewer: AccessRead}},
	{ModuleStats, map[Role]Access{RoleAdmin: AccessRead, RoleWarehouse: AccessRead, RoleProduction: AccessRead, RoleViewer: AccessRead}},
	{ModuleProducts, map[Role]Access{RoleAdmin: AccessWrite, RoleWarehouse: AccessRead, RoleProduction: AccessRead, RoleViewer: AccessRead}},
	{ModuleParts, map[Role]Access{RoleAdmin: AccessWrite, RoleWarehouse: AccessWrite, RoleProduction: AccessRead, RoleViewer: AccessRead}},
	{ModuleBOM, map[Role]Access{RoleAdmin: AccessWrite, RoleWarehouse: AccessRead, RoleProduction: AccessRead, RoleViewer: AccessRead}},
	{ModuleBatch, map[Role]Access{RoleAdmin: AccessWrite, RoleWarehouse: AccessRead, RoleProduction: AccessWrite, RoleViewer: AccessRead}},
	// 操作记录含操作人账号、时间与具体动作，2026-10-10 定：只读角色不得查看。
	// 改成 AccessNone 后，usecase.Allow 与 HTTP 层自动收紧，前端导航也据此隐藏入口。
	{ModuleAudit, map[Role]Access{RoleAdmin: AccessRead, RoleWarehouse: AccessRead, RoleProduction: AccessRead, RoleViewer: AccessNone}},
	{ModuleBackup, map[Role]Access{RoleAdmin: AccessWrite, RoleWarehouse: AccessRead, RoleProduction: AccessNone, RoleViewer: AccessNone}},
	{ModuleUsers, map[Role]Access{RoleAdmin: AccessWrite, RoleWarehouse: AccessNone, RoleProduction: AccessNone, RoleViewer: AccessNone}},
	// 应用维护：只有管理员能写（替换可执行文件），其他角色无权限。
	//
	// 注意 AccessNone 是**每个角色都要显式写出来**的：缺键会经 AccessFor 落回
	// AccessNone，看起来一样，但「显式写 None」和「忘了写」在评审时无法区分。
	// TestEveryModuleCoversEveryRole 会挡住漏写。
	{ModuleSystem, map[Role]Access{RoleAdmin: AccessWrite, RoleWarehouse: AccessNone, RoleProduction: AccessNone, RoleViewer: AccessNone}},
}

var moduleAccess = func() map[string]map[Role]Access {
	m := make(map[string]map[Role]Access, len(modules))
	for _, g := range modules {
		m[g.module] = g.access
	}
	return m
}()

func AccessFor(role Role, module string) Access {
	if m, ok := moduleAccess[module]; ok {
		if a, ok := m[role]; ok {
			return a
		}
	}
	return AccessNone
}

// AllModules 返回全部权限模块的键，顺序与 modules 的声明顺序一致。
//
// 前端导航要靠它枚举模块，但不该自己维护一份模块列表——模块增减必须同时
// 反映到权限表、导航与界面上。它就是上面那张表的键，不可能再漏。
func AllModules() []string {
	out := make([]string, 0, len(modules))
	for _, g := range modules {
		out = append(out, g.module)
	}
	return out
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
