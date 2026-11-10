// Package sse 提供 Server-Sent Events 的逐事件读取与写入，用于无损流式透传。
package sse

import (
	"bufio"
	"io"
	"strings"
)

// DoneMarker 是 OpenAI 兼容流的结束标记。
const DoneMarker = "data: [DONE]"

// Reader 按 SSE 空行分隔逐事件读取。
type Reader struct {
	br *bufio.Reader
}

// NewReader 创建 SSE 读取器，支持较大的事件负载。
func NewReader(r io.Reader) *Reader {
	br := bufio.NewReaderSize(r, 256*1024)
	return &Reader{br: br}
}

// Next 返回下一个事件块（不含结尾空行），无更多事件时 ok 为 false。
func (r *Reader) Next() (block string, ok bool, err error) {
	var lines []string
	for {
		line, readErr := r.br.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\r\n")
			if trimmed == "" {
				if len(lines) > 0 {
					return strings.Join(lines, "\n"), true, nil
				}
				// 跳过前导或连续空行。
			} else {
				lines = append(lines, trimmed)
			}
		}
		if readErr != nil {
			if len(lines) > 0 {
				return strings.Join(lines, "\n"), true, nil
			}
			if readErr == io.EOF {
				return "", false, nil
			}
			return "", false, readErr
		}
	}
}

// Flusher 是支持立即刷新的写入器（通常是 http.ResponseWriter）。
type Flusher interface {
	Flush()
}

// Writer 写出 SSE 事件并在每个事件后刷新。
type Writer struct {
	w io.Writer
	f Flusher
}

// NewWriter 创建 SSE 写入器。
func NewWriter(w io.Writer, f Flusher) *Writer {
	return &Writer{w: w, f: f}
}

// WriteEvent 写出一个事件块并追加空行，随后立即 flush。
func (sw *Writer) WriteEvent(block string) error {
	if _, err := io.WriteString(sw.w, block+"\n\n"); err != nil {
		return err
	}
	if sw.f != nil {
		sw.f.Flush()
	}
	return nil
}

// IsDone 判断事件块是否为结束标记。
func IsDone(block string) bool {
	return strings.HasPrefix(block, DoneMarker)
}
