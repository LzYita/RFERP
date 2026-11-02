// Package license 实现局域网版的离线授权：Ed25519 签名的 JSON envelope。
//
// 为什么是离线签名而不是在线激活（D-7）：客户主机可能根本不连公网，
// 而授权检查发生在每次登录。签名文件的验证只依赖内嵌公钥，不需要任何网络。
//
// 文件形态（.rferplic，就是一段 JSON）：
//
//	{
//	  "payload":   "<Base64(claims JSON 原始字节)>",
//	  "signature": "<Base64(Ed25519 签名)>",
//	  "keyId":     "dev-1"
//	}
//
// 签名对象是 **payload 解码后的原始字节**，不是重新序列化的结构体：
// 重新序列化会依赖字段顺序与时间格式，签发端与校验端的 Go 版本一换就可能
// 对不上，表现为"授权突然无效"。
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// decodePEM 取出第一个 PEM 块的 DER 正文。
func decodePEM(s string) ([]byte, error) {
	block, _ := pem.Decode([]byte(s))
	if block == nil {
		return nil, errors.New("不是合法的 PEM 块")
	}
	return block.Bytes, nil
}

// Kind 是授权类型。
const (
	KindPermanent    = "permanent"
	KindTrial        = "trial"
	KindSubscription = "subscription"
)

// Claims 是授权正文。时间一律用 RFC3339 字符串：
// 用 time.Time 序列化会把时区、纳秒位与 Go 版本行为带进签名，
// 而签发工具与校验端不必是同一个进程。
type Claims struct {
	LicenseID    string   `json:"licenseId"`
	Customer     string   `json:"customer"`
	Edition      string   `json:"edition"`
	Kind         string   `json:"kind"`
	MaxClients   int      `json:"maxClients"`
	IssuedAt     string   `json:"issuedAt"`
	SupportUntil string   `json:"supportUntil,omitempty"`
	ExpiresAt    string   `json:"expiresAt,omitempty"`
	Features     []string `json:"features,omitempty"`
	KeyID        string   `json:"keyId,omitempty"`
}

// Envelope 是落盘的授权文件。
type Envelope struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
	KeyID     string `json:"keyId,omitempty"`
}

// Errors 供上层映射成错误契约（code）。
var (
	ErrMalformed      = errors.New("授权文件格式不正确")
	ErrBadSignature   = errors.New("授权签名校验失败（文件被修改或不是本产品签发）")
	ErrUnknownKey     = errors.New("授权使用了未配置的密钥")
	ErrEmptyPayload   = errors.New("授权正文为空")
	ErrInvalidMaxClie = errors.New("授权客户端数不合法")
)

// DefaultPublicKeyBase64 是内置的公钥（Ed25519 原始 32 字节的 Base64）。
//
// 留空是刻意的：私钥在签发方手里，公钥跟着发布走，两者都不该硬编码进
// 一份会进版本库的源码。发布构建用
//
//	go build -ldflags "-X app/internal/license.DefaultPublicKeyBase64=<公钥>"
//
// 注入；安装现场也可以用 RFERP_LICENSE_PUBKEY 环境变量或数据目录下的
// license-public.key 文件补上。三者是**叠加**关系：换钥匙时旧公钥仍要能
// 验旧授权，所以新公钥只追加、不替换。
var DefaultPublicKeyBase64 = ""

// LoadAdditionalKeys 读取追加公钥：环境变量（逗号分隔）+ 数据目录公钥文件。
func LoadAdditionalKeys() []ed25519.PublicKey {
	var out []ed25519.PublicKey
	for _, part := range strings.Split(os.Getenv("RFERP_LICENSE_PUBKEY"), ",") {
		if k, err := ParsePublicKey(strings.TrimSpace(part)); err == nil {
			out = append(out, k)
		}
	}
	if p := publicKeyFile(); p != "" {
		if raw, err := os.ReadFile(p); err == nil {
			if k, err := ParsePublicKey(string(raw)); err == nil {
				out = append(out, k)
			}
		}
	}
	return out
}

// publicKeyFile 返回数据目录下的公钥文件路径（存在才返回）。
func publicKeyFile() string {
	dir := strings.TrimSpace(os.Getenv("RFERP_DATA_DIR"))
	if dir == "" {
		// 不 import paths：license 是叶子包，反向依赖会让装配顺序变复杂。
		// 这里只认环境变量与安装器写入的固定位置。
		return ""
	}
	p := filepath.Join(dir, "license-public.key")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// ParsePublicKey 接受 Base64 原始公钥或 PEM（"PUBLIC KEY" 块）。
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ErrUnknownKey
	}
	if strings.Contains(s, "-----BEGIN") {
		der, err := decodePEM(s)
		if err != nil {
			return nil, fmt.Errorf("解析公钥 PEM: %w", err)
		}
		// SubjectPublicKeyInfo: 12 字节头 + 32 字节原始公钥。
		if len(der) != ed25519.PublicKeySize+12 {
			return nil, fmt.Errorf("公钥长度异常: %d", len(der))
		}
		return ed25519.PublicKey(der[12:]), nil
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("公钥 Base64: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("公钥长度异常: %d，期望 %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// ParsePrivateKey 读取签发私钥（Base64 原始 64 字节或 PEM）。
func ParsePrivateKey(s string) (ed25519.PrivateKey, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("私钥为空")
	}
	if strings.Contains(s, "-----BEGIN") {
		der, err := decodePEM(s)
		if err != nil {
			return nil, fmt.Errorf("解析私钥 PEM: %w", err)
		}
		// PKCS#8 里 Ed25519 私钥是 32 字节种子，末尾 32 字节即种子。
		if len(der) < 32 {
			return nil, fmt.Errorf("私钥长度异常: %d", len(der))
		}
		seed := der[len(der)-32:]
		return ed25519.NewKeyFromSeed(seed), nil
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("私钥解码失败: %w", err)
	}
	// 兼容两种写法：64 字节完整私钥，或 32 字节种子。
	switch len(raw) {
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	}
	return nil, fmt.Errorf("私钥长度异常: %d", len(raw))
}

// Sign 用私钥签发一份授权文件。
func Sign(claims Claims, priv ed25519.PrivateKey) ([]byte, error) {
	if claims.MaxClients < 0 {
		return nil, ErrInvalidMaxClie
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return nil, err
	}
	sig := ed25519.Sign(priv, payload)
	env := Envelope{
		Payload:   base64.StdEncoding.EncodeToString(payload),
		Signature: base64.StdEncoding.EncodeToString(sig),
		KeyID:     claims.KeyID,
	}
	return json.MarshalIndent(env, "", "  ")
}

// PublicKeys 返回当前可用的公钥集合（内置 + 环境变量追加）。
func PublicKeys() []ed25519.PublicKey {
	var out []ed25519.PublicKey
	if DefaultPublicKeyBase64 != "" {
		if k, err := ParsePublicKey(DefaultPublicKeyBase64); err == nil {
			out = append(out, k)
		}
	}
	out = append(out, LoadAdditionalKeys()...)
	return out
}

// ParseEnvelope 解析授权文件字节。
func ParseEnvelope(raw []byte) (*Envelope, error) {
	raw = []byte(strings.TrimPrefix(string(raw), "\ufeff"))
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if strings.TrimSpace(env.Payload) == "" || strings.TrimSpace(env.Signature) == "" {
		return nil, ErrEmptyPayload
	}
	return &env, nil
}

// VerifyWithKeys 用给定公钥集合验签并解析正文。任一公钥通过即可（支持换钥匙）。
func VerifyWithKeys(env *Envelope, keys []ed25519.PublicKey) (*Claims, error) {
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: payload Base64 无效", ErrMalformed)
	}
	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		return nil, fmt.Errorf("%w: signature Base64 无效", ErrMalformed)
	}
	if len(keys) == 0 {
		return nil, ErrUnknownKey
	}
	ok := false
	for _, k := range keys {
		if len(sig) == ed25519.SignatureSize && ed25519.Verify(k, payload, sig) {
			ok = true
			break
		}
	}
	if !ok {
		return nil, ErrBadSignature
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("%w: 正文不是合法 JSON: %v", ErrMalformed, err)
	}
	if c.MaxClients < 0 {
		return nil, ErrInvalidMaxClie
	}
	return &c, nil
}

// ParseAndVerify 用当前配置的公钥解析并验签。
func ParseAndVerify(raw []byte) (*Claims, error) {
	env, err := ParseEnvelope(raw)
	if err != nil {
		return nil, err
	}
	return VerifyWithKeys(env, PublicKeys())
}

// State 是授权在当前时刻的判定结果。
type State struct {
	Licensed bool
	// ReadOnly 为真表示业务写入应被拒绝（trial/订阅到期）。
	ReadOnly bool
	// MaxClients 是允许的客户端设备数；-1 表示不受限（用于内部/试用场景）。
	MaxClients int
	Claims     *Claims
	// Message 是给界面显示的一句话原因（到期、未导入等）。
	Message string
}

// Evaluate 按锁定口径判定授权状态（D-6/D-10）。
//
// 关键口径：
//   - 永久授权**不因 supportUntil 到期停用**：支持期是服务承诺，
//     不是使用权期限。把两者绑在一起会让"买了永久版、第二年没续支持"
//     变成软件打不开，那是产品事故而不是授权策略。
//   - 只有 trial / subscription 到期才转只读；只读不是停用，数据仍可查、
//     可备份、可导出。
//   - 未导入授权不是"过期"：此时主机仍可登录（否则客户连导入授权的界面
//     都进不去），只拒绝客户端设备。
func Evaluate(c *Claims, now time.Time) State {
	if c == nil {
		return State{Licensed: false, MaxClients: 0, Message: "尚未导入授权：仅主机可登录"}
	}
	st := State{Licensed: true, MaxClients: c.MaxClients, Claims: c}

	switch c.Kind {
	case KindTrial, KindSubscription:
		if exp := parseTime(c.ExpiresAt); exp != nil && now.After(*exp) {
			st.ReadOnly = true
			st.Message = "授权已到期，当前为只读模式（数据可查询与备份）"
		}
	case KindPermanent, "":
		// 永久授权不看任何日期。
	default:
		// 未知类型按最保守处理：当作已授权但只读，宁可让客户打电话，
		// 也不能因为签发工具写错一个词就让额度无限。
		st.ReadOnly = true
		st.Message = "授权类型无法识别，已进入只读模式"
	}
	return st
}

// parseTime 解析 RFC3339；空串或格式错误返回 nil（视为无此期限）。
func parseTime(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			t := t
			return &t
		}
	}
	return nil
}
