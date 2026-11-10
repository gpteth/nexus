package compute

import "errors"

// 算力调度领域错误。
var (
	// ErrNotFound 表示节点或任务不存在。
	ErrNotFound = errors.New("compute: not found")
	// ErrDuplicate 表示节点已注册。
	ErrDuplicate = errors.New("compute: duplicate node")
	// ErrQuotaExceeded 表示超出租户配额。
	ErrQuotaExceeded = errors.New("compute: quota exceeded")
	// ErrNoCapacity 表示当前没有可承载任务的节点。
	ErrNoCapacity = errors.New("compute: no capacity")
	// ErrInvalidState 表示任务状态不允许该操作。
	ErrInvalidState = errors.New("compute: invalid job state")
)
