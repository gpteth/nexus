// Package id 生成不可预测的随机标识符。
package id

import (
	"crypto/rand"
	"encoding/hex"
)

// New 返回带前缀的 32 字符十六进制标识符（16 字节随机熵）。
func New(prefix string) string {
	return prefix + randomHex(16)
}

// Short 返回 16 字符十六进制标识符，适合短名称与幂等键。
func Short() string {
	return randomHex(8)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败在现代系统上几乎不可能；退化为 panic 以暴露底层故障。
		panic("nexus: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}
