package agent

import "errors"

// 领域错误。
var (
	// ErrNotFound 表示聚合不存在。
	ErrNotFound = errors.New("agent: aggregate not found")
	// ErrDuplicate 表示事件已被接收过。
	ErrDuplicate = errors.New("agent: duplicate trigger")
	// ErrInvalidOutput 表示模型返回的结构化输出无法解析。
	ErrInvalidOutput = errors.New("agent: invalid model output")
	// ErrNoTicket 表示没有可处理该工单的派工。
	ErrNoTicket = errors.New("agent: no live ticket")
)
