// Package security 实现管理面安全：敏感凭据加密、JWT 签发校验与 RBAC。
package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

// 安全领域错误。
var (
	// ErrInvalidKey 表示主密钥长度不合法。
	ErrInvalidKey = errors.New("security: master key must be 32 bytes")
	// ErrInvalidCiphertext 表示密文损坏或解密失败。
	ErrInvalidCiphertext = errors.New("security: invalid ciphertext")
	// ErrNoMasterKey 表示未配置主密钥。
	ErrNoMasterKey = errors.New("security: master key not configured")
)

// Cipher 用 AES-256-GCM 对上游 API key 等敏感凭据做认证加密。
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher 从 32 字节主密钥构造加密器。
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt 加密明文，返回 base64(nonce || ciphertext)。每次随机 nonce，
// 相同明文产生不同密文；GCM 内置完整性校验，篡改即解密失败。
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt 解密 Encrypt 产出的密文。
func (c *Cipher) Decrypt(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", ErrInvalidCiphertext
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return "", ErrInvalidCiphertext
	}
	nonce, ct := raw[:ns], raw[ns:]
	pt, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", ErrInvalidCiphertext
	}
	return string(pt), nil
}

// LoadMasterKey 解析主密钥：优先识别 base64 编码的 32 字节原始密钥；
// 否则把任意口令经 SHA-256 派生为 32 字节，便于通过环境变量直接配置。
func LoadMasterKey(value string) ([]byte, error) {
	if value == "" {
		return nil, ErrNoMasterKey
	}
	if raw, err := base64.StdEncoding.DecodeString(value); err == nil && len(raw) == 32 {
		return raw, nil
	}
	sum := sha256.Sum256([]byte(value))
	return sum[:], nil
}
