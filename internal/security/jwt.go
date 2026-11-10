package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// JWT 相关错误。
var (
	// ErrMalformedToken 表示令牌格式不合法。
	ErrMalformedToken = errors.New("security: malformed token")
	// ErrBadSignature 表示签名不匹配。
	ErrBadSignature = errors.New("security: bad signature")
	// ErrTokenExpired 表示令牌已过期。
	ErrTokenExpired = errors.New("security: token expired")
)

// Claims 是管理面令牌的声明。
type Claims struct {
	Subject   string
	TenantID  string
	Roles     []string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

type jwtBody struct {
	Iss      string   `json:"iss"`
	Sub      string   `json:"sub"`
	TenantID string   `json:"tenant,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	Iat      int64    `json:"iat"`
	Exp      int64    `json:"exp"`
}

// JWTManager 用 HS256（HMAC-SHA256）签发与校验管理面令牌。
type JWTManager struct {
	secret []byte
	issuer string
	ttl    time.Duration
	now    func() time.Time
}

// NewJWTManager 构造令牌管理器。
func NewJWTManager(secret []byte, issuer string, ttl time.Duration, now func() time.Time) *JWTManager {
	return &JWTManager{secret: secret, issuer: issuer, ttl: ttl, now: now}
}

// Issue 签发包含给定声明的令牌。
func (m *JWTManager) Issue(c Claims) (string, error) {
	hb, err := json.Marshal(jwtHeader{Alg: "HS256", Typ: "JWT"})
	if err != nil {
		return "", err
	}
	now := m.now()
	bb, err := json.Marshal(jwtBody{
		Iss:      m.issuer,
		Sub:      c.Subject,
		TenantID: c.TenantID,
		Roles:    c.Roles,
		Iat:      now.Unix(),
		Exp:      now.Add(m.ttl).Unix(),
	})
	if err != nil {
		return "", err
	}
	signing := encode(hb) + "." + encode(bb)
	return signing + "." + encode(m.sign([]byte(signing))), nil
}

// Verify 校验签名与有效期并返回声明。
func (m *JWTManager) Verify(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrMalformedToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrMalformedToken
	}
	if !hmac.Equal(sig, m.sign([]byte(parts[0]+"."+parts[1]))) {
		return nil, ErrBadSignature
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrMalformedToken
	}
	var body jwtBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, ErrMalformedToken
	}
	if !m.now().Before(time.Unix(body.Exp, 0)) {
		return nil, ErrTokenExpired
	}
	return &Claims{
		Subject:   body.Sub,
		TenantID:  body.TenantID,
		Roles:     body.Roles,
		IssuedAt:  time.Unix(body.Iat, 0),
		ExpiresAt: time.Unix(body.Exp, 0),
	}, nil
}

func (m *JWTManager) sign(data []byte) []byte {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write(data)
	return mac.Sum(nil)
}

func encode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
