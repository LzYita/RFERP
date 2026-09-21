package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Manifest struct {
	Version     string `json:"version"`
	Channel     string `json:"channel,omitempty"`
	MinVersion  string `json:"minVersion,omitempty"`
	URL         string `json:"url"`
	Size        int64  `json:"size,omitempty"`
	SHA256      string `json:"sha256"`
	Notes       string `json:"notes,omitempty"`
	PublishedAt string `json:"publishedAt,omitempty"`
	Sig         string `json:"sig"`
}

func (m *Manifest) signingBytes() ([]byte, error) {
	c := *m
	c.Sig = ""
	return json.Marshal(c)
}

func (m *Manifest) Sign(priv ed25519.PrivateKey) error {
	b, err := m.signingBytes()
	if err != nil {
		return err
	}
	m.Sig = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, b))
	return nil
}

func (m *Manifest) Verify(pub ed25519.PublicKey) error {
	if m.Sig == "" {
		return errors.New("更新清单缺少签名")
	}
	sig, err := base64.StdEncoding.DecodeString(m.Sig)
	if err != nil {
		return fmt.Errorf("签名解码失败: %w", err)
	}
	b, err := m.signingBytes()
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, b, sig) {
		return errors.New("更新清单签名校验失败（可能被篡改）")
	}
	return nil
}

func CompareVersions(a, b string) int {
	pa := parseVersion(a)
	pb := parseVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseVersion(v string) [3]int {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		n, _ := strconv.Atoi(strings.TrimSpace(parts[i]))
		out[i] = n
	}
	return out
}
