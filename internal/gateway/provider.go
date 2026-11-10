package gateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// RawResponse 是上游的原始响应，统一承载流式与非流式。Release 必须在处理结束后调用。
type RawResponse struct {
	StatusCode  int
	Header      http.Header
	Body        io.ReadCloser
	upstream    string
	releaseOnce sync.Once
	release     func()
}

// UpstreamName 返回产生该响应的上游名。
func (r *RawResponse) UpstreamName() string { return r.upstream }

// Release 关闭响应体并释放占用的并发槽，可安全重复调用。
func (r *RawResponse) Release() {
	r.releaseOnce.Do(func() {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		if r.release != nil {
			r.release()
		}
	})
}

// Provider 是模型上游的抽象端口。
type Provider interface {
	Name() string
	Models() []string
	Weight() int
	Active() int
	Healthy() bool
	Chat(ctx context.Context, body []byte) (*RawResponse, error)
	Ping(ctx context.Context) bool
}

// UpstreamError 表示上游返回了非 2xx 状态或请求失败。
type UpstreamError struct {
	Upstream   string
	StatusCode int
	Body       string
	err        error
}

func (e *UpstreamError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("upstream %s: %v", e.Upstream, e.err)
	}
	return fmt.Sprintf("upstream %s returned %d", e.Upstream, e.StatusCode)
}

// Unwrap 返回底层网络错误。
func (e *UpstreamError) Unwrap() error { return e.err }

// Retryable 判断该错误是否可安全重试（仅幂等读取类状态码与网络错误）。
func (e *UpstreamError) Retryable() bool {
	if e.err != nil {
		return true
	}
	return e.StatusCode == http.StatusRequestTimeout ||
		e.StatusCode == http.StatusTooManyRequests ||
		e.StatusCode >= 500
}
