package security

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time          { return f.t }
func (f *fakeClock) Advance(d time.Duration) { f.t = f.t.Add(d) }

// ---- 加密 ----

func TestCipher_RoundTrip(t *testing.T) {
	c, err := NewCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	enc, err := c.Encrypt("sk-secret-123")
	if err != nil {
		t.Fatal(err)
	}
	if enc == "sk-secret-123" {
		t.Fatal("plaintext should not be stored")
	}
	dec, err := c.Decrypt(enc)
	if err != nil || dec != "sk-secret-123" {
		t.Fatalf("dec=%q err=%v", dec, err)
	}
	enc2, _ := c.Encrypt("sk-secret-123")
	if enc == enc2 {
		t.Fatal("random nonce should produce different ciphertext")
	}
}

func TestCipher_WrongKeyAndTamper(t *testing.T) {
	c1, _ := NewCipher(bytes.Repeat([]byte{1}, 32))
	c2, _ := NewCipher(bytes.Repeat([]byte{2}, 32))
	enc, _ := c1.Encrypt("secret")
	if _, err := c2.Decrypt(enc); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("wrong key err=%v", err)
	}
	bad := enc[:len(enc)-1]
	if strings.HasSuffix(enc, "A") {
		bad += "B"
	} else {
		bad += "A"
	}
	if _, err := c1.Decrypt(bad); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("tamper err=%v", err)
	}
}

func TestCipher_InvalidKey(t *testing.T) {
	if _, err := NewCipher([]byte("short")); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadMasterKey(t *testing.T) {
	raw := bytes.Repeat([]byte{7}, 32)
	k, err := LoadMasterKey(base64.StdEncoding.EncodeToString(raw))
	if err != nil || !bytes.Equal(k, raw) {
		t.Fatal("base64 key should round-trip")
	}
	k2, err := LoadMasterKey("my-passphrase")
	if err != nil || len(k2) != 32 {
		t.Fatal("passphrase should derive 32 bytes")
	}
	if _, err := LoadMasterKey(""); !errors.Is(err, ErrNoMasterKey) {
		t.Fatalf("err=%v", err)
	}
}

// ---- JWT ----

func TestJWT_RoundTrip(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	jm := NewJWTManager([]byte("secret"), "nexus", time.Hour, clock.Now)
	tok, err := jm.Issue(Claims{Subject: "u1", TenantID: "t1", Roles: []string{RoleAdmin}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := jm.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "u1" || c.TenantID != "t1" || len(c.Roles) != 1 {
		t.Fatalf("claims=%+v", c)
	}
}

func TestJWT_Expired(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	jm := NewJWTManager([]byte("secret"), "nexus", time.Hour, clock.Now)
	tok, _ := jm.Issue(Claims{Subject: "u1"})
	clock.Advance(2 * time.Hour)
	if _, err := jm.Verify(tok); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("err=%v", err)
	}
}

func TestJWT_BadSignatureAndMalformed(t *testing.T) {
	jm1 := NewJWTManager([]byte("secret1"), "nexus", time.Hour, time.Now)
	jm2 := NewJWTManager([]byte("secret2"), "nexus", time.Hour, time.Now)
	tok, _ := jm1.Issue(Claims{Subject: "u1"})
	if _, err := jm2.Verify(tok); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err=%v", err)
	}
	jm := NewJWTManager([]byte("secret"), "nexus", time.Hour, time.Now)
	if _, err := jm.Verify("not-a-jwt"); !errors.Is(err, ErrMalformedToken) {
		t.Fatalf("err=%v", err)
	}
}

// ---- RBAC ----

func TestRBAC_Roles(t *testing.T) {
	rbac := NewRBAC()
	if rbac.Allowed([]string{RoleViewer}, PermNodesWrite) {
		t.Fatal("viewer cannot write nodes")
	}
	if !rbac.Allowed([]string{RoleViewer}, PermNodesRead) {
		t.Fatal("viewer can read nodes")
	}
	if !rbac.Allowed([]string{RoleOperator}, PermNodesWrite) {
		t.Fatal("operator can write nodes")
	}
	if rbac.Allowed([]string{RoleOperator}, PermKeysWrite) {
		t.Fatal("operator cannot write keys")
	}
	if !rbac.Allowed([]string{RoleAdmin}, PermKeysWrite) {
		t.Fatal("admin has all permissions")
	}
}

func TestRBAC_Grant(t *testing.T) {
	rbac := NewRBAC()
	rbac.Grant("custom", PermNodesRead)
	if !rbac.Allowed([]string{"custom"}, PermNodesRead) {
		t.Fatal("granted permission should apply")
	}
	if rbac.Allowed([]string{"custom"}, PermNodesWrite) {
		t.Fatal("ungranted permission should be denied")
	}
}

// ---- 中间件端到端 ----

func TestMiddleware_EndToEnd(t *testing.T) {
	jm := NewJWTManager([]byte("secret"), "nexus", time.Hour, time.Now)
	rbac := NewRBAC()

	mux := http.NewServeMux()
	mux.Handle("GET /nodes", rbac.RequirePermission(PermNodesRead)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	mux.Handle("POST /nodes", rbac.RequirePermission(PermNodesWrite)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	handler := jm.Middleware(mux)

	// 无 token → 401。
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/nodes", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token got %d want 401", rec.Code)
	}

	viewerTok, _ := jm.Issue(Claims{Subject: "v", Roles: []string{RoleViewer}})
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+viewerTok)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer GET got %d want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+viewerTok)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer POST got %d want 403", rec.Code)
	}

	adminTok, _ := jm.Issue(Claims{Subject: "a", Roles: []string{RoleAdmin}})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin POST got %d want 200", rec.Code)
	}
}
