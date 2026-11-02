package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"
)

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func sign(t *testing.T, priv ed25519.PrivateKey, c Claims) []byte {
	t.Helper()
	raw, err := Sign(c, priv)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return raw
}

func TestSignAndVerify(t *testing.T) {
	pub, priv := newKey(t)
	raw := sign(t, priv, Claims{
		LicenseID: "L-1", Customer: "客户甲", Edition: "lan",
		Kind: KindPermanent, MaxClients: 2, IssuedAt: time.Now().Format(time.RFC3339),
	})
	env, err := ParseEnvelope(raw)
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	got, err := VerifyWithKeys(env, []ed25519.PublicKey{pub})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Customer != "客户甲" || got.MaxClients != 2 {
		t.Fatalf("claims = %+v", got)
	}
}

// 篡改任一字段都必须被拒——这是授权体系唯一的防线（R8）。
func TestTamperedPayloadRejected(t *testing.T) {
	pub, priv := newKey(t)
	raw := sign(t, priv, Claims{LicenseID: "L-1", Kind: KindPermanent, MaxClients: 2})
	env, err := ParseEnvelope(raw)
	if err != nil {
		t.Fatal(err)
	}
	// 把 maxClients 改成 99：payload 变了，签名对不上。
	forged := base64.StdEncoding.EncodeToString([]byte(`{"licenseId":"L-1","kind":"permanent","maxClients":99}`))
	env.Payload = forged
	if _, err := VerifyWithKeys(env, []ed25519.PublicKey{pub}); err == nil {
		t.Fatal("篡改 payload 后仍然验签通过")
	}
}

func TestEmptySignatureRejected(t *testing.T) {
	_, priv := newKey(t)
	raw := sign(t, priv, Claims{Kind: KindPermanent, MaxClients: 1})
	env, _ := ParseEnvelope(raw)
	env.Signature = ""
	if _, err := ParseEnvelope([]byte(`{"payload":"e30=","signature":""}`)); err == nil {
		_ = env
		t.Fatal("空签名应被 ParseEnvelope 拒绝")
	}
}

// 永久授权不因支持期到期而停用（D-6/D-10）。
func TestPermanentIgnoresSupportUntil(t *testing.T) {
	past := time.Now().Add(-48 * time.Hour).Format(time.RFC3339)
	st := Evaluate(&Claims{
		Kind: KindPermanent, MaxClients: 2, SupportUntil: past,
	}, time.Now())
	if st.ReadOnly || !st.Licensed {
		t.Fatalf("永久授权不应因支持期到期转只读: %+v", st)
	}
}

// 试用到期转只读。
func TestTrialExpiredReadOnly(t *testing.T) {
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	st := Evaluate(&Claims{Kind: KindTrial, MaxClients: 3, ExpiresAt: past}, time.Now())
	if !st.ReadOnly {
		t.Fatalf("过期试用应转只读: %+v", st)
	}
	future := time.Now().Add(time.Hour).Format(time.RFC3339)
	st = Evaluate(&Claims{Kind: KindTrial, MaxClients: 3, ExpiresAt: future}, time.Now())
	if st.ReadOnly {
		t.Fatalf("未到期试用不应只读: %+v", st)
	}
}

// 未导入授权：不受任何额度限制也没说，只表达"未授权"。
func TestNoLicenseState(t *testing.T) {
	st := Evaluate(nil, time.Now())
	if st.Licensed || st.MaxClients != 0 {
		t.Fatalf("无授权时的状态 = %+v", st)
	}
	if st.Message == "" {
		t.Fatal("无授权应给出可显示的原因")
	}
}

// 未知类型按只读处理：签发工具写错一个词，不能变成无限额度。
func TestUnknownKindReadOnly(t *testing.T) {
	st := Evaluate(&Claims{Kind: "lifetime", MaxClients: 99}, time.Now())
	if !st.ReadOnly {
		t.Fatalf("未知授权类型应转只读: %+v", st)
	}
}
