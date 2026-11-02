// rferp-license 是内部签发工具，**不随客户交付**（方案 §14）。
//
// 用法：
//
//	rferp-license genkey -dir <目录>                      生成密钥对
//	rferp-license issue -key <私钥文件> -customer 客户名 \
//	    -kind permanent -maxClients 2 -supportUntil 2027-10-29 -out license.rferplic
//
// 为什么签发工具要与服务器分开：私钥必须在签发方手里。放进服务端二进制，
// 等于把"谁能发授权"交给了每一个拿到安装包的人。
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"app/internal/license"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "genkey":
		genkey(os.Args[2:])
	case "issue":
		issue(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `RFERP 授权签发工具

  genkey -dir <目录>                 生成 Ed25519 密钥对（private.key / public.key）
  issue  -key <私钥> [选项]          签发一份 .rferplic

issue 选项：
  -customer    客户名称（必填）
  -edition     版本标识，默认 lan
  -kind        permanent | trial | subscription，默认 permanent
  -maxClients  允许的客户端设备数（不含主机）
  -supportUntil 支持到期日 YYYY-MM-DD（仅展示，不影响永久授权使用）
  -expiresAt   试用/订阅到期日 YYYY-MM-DD（trial/subscription 必填）
  -id          授权编号；留空自动生成
  -out         输出文件，默认 license.rferplic
`)
	os.Exit(2)
}

func genkey(args []string) {
	fs := flag.NewFlagSet("genkey", flag.ExitOnError)
	dir := fs.String("dir", ".", "密钥输出目录")
	_ = fs.Parse(args)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatalf("生成密钥失败：%v", err)
	}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		log.Fatalf("创建目录失败：%v", err)
	}
	privPath := filepath.Join(*dir, "private.key")
	pubPath := filepath.Join(*dir, "public.key")
	if err := os.WriteFile(privPath, []byte(base64.StdEncoding.EncodeToString(priv)), 0o600); err != nil {
		log.Fatalf("写私钥失败：%v", err)
	}
	if err := os.WriteFile(pubPath, []byte(base64.StdEncoding.EncodeToString(pub)), 0o644); err != nil {
		log.Fatalf("写公钥失败：%v", err)
	}
	fmt.Printf("私钥：%s（务必离线保存，不要进版本库）\n", privPath)
	fmt.Printf("公钥：%s\n", pubPath)
	fmt.Printf("\n内置公钥（填到 internal/license/license.go 的 DefaultPublicKeyBase64，或设环境变量 RFERP_LICENSE_PUBKEY）：\n%s\n",
		base64.StdEncoding.EncodeToString(pub))
}

func issue(args []string) {
	fs := flag.NewFlagSet("issue", flag.ExitOnError)
	keyPath := fs.String("key", "", "私钥文件")
	customer := fs.String("customer", "", "客户名称")
	edition := fs.String("edition", "lan", "版本标识")
	kind := fs.String("kind", license.KindPermanent, "permanent | trial | subscription")
	maxClients := fs.Int("maxClients", 2, "允许的客户端设备数")
	supportUntil := fs.String("supportUntil", "", "支持到期日 YYYY-MM-DD")
	expiresAt := fs.String("expiresAt", "", "试用/订阅到期日 YYYY-MM-DD")
	id := fs.String("id", "", "授权编号")
	out := fs.String("out", "license.rferplic", "输出文件")
	_ = fs.Parse(args)

	if *keyPath == "" || strings.TrimSpace(*customer) == "" {
		fmt.Fprintln(os.Stderr, "缺少 -key 或 -customer")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*keyPath)
	if err != nil {
		log.Fatalf("读取私钥：%v", err)
	}
	priv, err := license.ParsePrivateKey(string(raw))
	if err != nil {
		log.Fatalf("解析私钥：%v", err)
	}

	now := time.Now()
	k := strings.TrimSpace(*kind)
	switch k {
	case license.KindPermanent, license.KindTrial, license.KindSubscription:
	default:
		log.Fatalf("未知授权类型 %q（可选 permanent / trial / subscription）", k)
	}
	if k != license.KindPermanent && strings.TrimSpace(*expiresAt) == "" {
		log.Fatalf("%s 授权必须给 -expiresAt", k)
	}
	if *maxClients < 0 {
		log.Fatalf("-maxClients 不能为负")
	}

	licenseID := strings.TrimSpace(*id)
	if licenseID == "" {
		licenseID = fmt.Sprintf("%s-%d", k, now.Unix())
	}
	claims := license.Claims{
		LicenseID:    licenseID,
		Customer:     strings.TrimSpace(*customer),
		Edition:      strings.TrimSpace(*edition),
		Kind:         k,
		MaxClients:   *maxClients,
		IssuedAt:     now.Format(time.RFC3339),
		SupportUntil: normDate(*supportUntil),
		ExpiresAt:    normDate(*expiresAt),
		Features:     []string{"lan"},
		KeyID:        "rferp-1",
	}
	data, err := license.Sign(claims, priv)
	if err != nil {
		log.Fatalf("签名失败：%v", err)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		log.Fatalf("写授权文件：%v", err)
	}
	// 自检：写完立刻用**私钥导出的公钥**验一遍，而不是用内置公钥。
	// 用内置公钥只要环境没配好就会误报"签发失败"，而真正要证明的是
	// "这份文件与这把私钥配套，且能通过验签逻辑"。
	env, err := license.ParseEnvelope(data)
	if err != nil {
		log.Fatalf("签发结果自检失败：%v", err)
	}
	got, err := license.VerifyWithKeys(env, []ed25519.PublicKey{priv.Public().(ed25519.PublicKey)})
	if err != nil {
		log.Fatalf("签发结果自检失败：%v", err)
	}
	fmt.Printf("已签发：%s\n客户：%s\n类型：%s\n客户端数：%d\n到期：%s\n",
		*out, got.Customer, got.Kind, got.MaxClients, firstOr(got.ExpiresAt, "永久"))
}

// normDate 把 YYYY-MM-DD 归一成 RFC3339（本地时区当天 23:59:59）。
//
// 用当天结束时刻而不是 00:00：写"到期日 2027-10-29"的客户理解为
// "29 号当天还能用"，按零点算会让它提前一天失效。
func normDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t.Add(24*time.Hour - time.Second).Format(time.RFC3339)
	}
	return s
}

func firstOr(a, b string) string {
	if strings.TrimSpace(a) == "" {
		return b
	}
	return a
}
