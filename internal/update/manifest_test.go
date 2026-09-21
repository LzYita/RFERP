package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestSignVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := &Manifest{Version: "1.2.3", URL: "http://example/y.zip", SHA256: "abc"}
	if err := m.Sign(priv); err != nil {
		t.Fatal(err)
	}
	if err := m.Verify(pub); err != nil {
		t.Fatalf("verify: %v", err)
	}
	m.Version = "9.9.9"
	if err := m.Verify(pub); err == nil {
		t.Fatal("expected verification to fail after tampering")
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.2.3", "1.2.2", 1},
		{"v1.2.3", "1.2.4", -1},
		{"2.0", "1.9.9", 1},
		{"1.0", "1.0.0", 0},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCheckAndDownload(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("fake zip payload")
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases.json":
			m := Manifest{Version: "1.1.0", URL: srv.URL + "/pkg.zip", Size: int64(len(payload)), SHA256: hexSum}
			if err := m.Sign(priv); err != nil {
				t.Errorf("sign: %v", err)
			}
			json.NewEncoder(w).Encode(m)
		case "/pkg.zip":
			w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Checker{ManifestURL: srv.URL + "/releases.json", CurrentVersion: "1.0.0", PublicKey: pub, Client: srv.Client()}
	m, err := c.Check()
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		t.Fatal("expected an update to be available")
	}
	path, err := c.Download(m, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatal("downloaded payload mismatch")
	}

	c2 := &Checker{ManifestURL: srv.URL + "/releases.json", CurrentVersion: "2.0.0", PublicKey: pub, Client: srv.Client()}
	m2, err := c2.Check()
	if err != nil {
		t.Fatal(err)
	}
	if m2 != nil {
		t.Fatal("expected no update for a newer current version")
	}
}

func TestCheckRejectsWrongKey(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	payload := []byte("x")
	sum := sha256.Sum256(payload)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := Manifest{Version: "1.1.0", URL: srv.URL + "/pkg.zip", SHA256: hex.EncodeToString(sum[:])}
		m.Sign(priv)
		json.NewEncoder(w).Encode(m)
	}))
	defer srv.Close()

	c := &Checker{ManifestURL: srv.URL, CurrentVersion: "1.0.0", PublicKey: otherPub, Client: srv.Client()}
	if _, err := c.Check(); err == nil {
		t.Fatal("expected signature verification to fail with a wrong public key")
	}
}
