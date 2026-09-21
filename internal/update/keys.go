package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
)

// PublicKeyB64 is the base64-encoded Ed25519 public key used to verify update
// manifests. It is filled in by the maintainer after running cmd/keygen.
var PublicKeyB64 = "t8gfb3JleA69ZnK5vTHgyl7KuikRRV90psjv4HcasEk="

func PublicKey() (ed25519.PublicKey, error) {
	if PublicKeyB64 == "" {
		return nil, errors.New("更新公钥未配置")
	}
	b, err := base64.StdEncoding.DecodeString(PublicKeyB64)
	if err != nil {
		return nil, err
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, errors.New("更新公钥长度无效")
	}
	return ed25519.PublicKey(b), nil
}
