// bench 是自包含的 HTTP 压测工具：零第三方依赖，可复现地测量吞吐、错误率与延迟分位。
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type result struct {
	latency time.Duration
	ok      bool
	status  int
}

func main() {
	baseURL := flag.String("url", "http://127.0.0.1:8080", "target base URL")
	path := flag.String("path", "/healthz", "request path")
	method := flag.String("method", "GET", "HTTP method")
	body := flag.String("body", "", "request body")
	key := flag.String("key", "", "bearer API key")
	concurrency := flag.Int("c", 50, "concurrent workers")
	total := flag.Int("n", 10000, "total requests; 0 uses duration mode")
	duration := flag.Duration("d", 10*time.Second, "duration when n=0")
	mockMode := flag.Bool("mock-server", false, "run a high-concurrency OpenAI-compatible mock and exit")
	mockAddr := flag.String("mock-addr", "127.0.0.1:9100", "address for mock-server mode")
	flag.Parse()

	if *mockMode {
		if err := runMockServer(*mockAddr); err != nil {
			fmt.Println("mock server error:", err)
		}
		return
	}

	target := *baseURL + *path
	var remaining int64 = int64(*total)
	deadline := time.Now().Add(*duration)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	results := make(chan result, *concurrency*8)
	var wg sync.WaitGroup
	gate := make(chan struct{})

	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			client := &http.Client{Transport: &http.Transport{
				MaxIdleConns:        *concurrency,
				MaxIdleConnsPerHost: *concurrency,
				IdleConnTimeout:     30 * time.Second,
			}}
			for {
				if *total > 0 {
					if atomic.AddInt64(&remaining, -1) < 0 {
						return
					}
				} else if time.Now().After(deadline) {
					return
				}
				var reqBody io.Reader
				if *body != "" {
					reqBody = bytes.NewBufferString(*body)
				}
				req, _ := http.NewRequestWithContext(ctx, *method, target, reqBody)
				if *key != "" {
					req.Header.Set("Authorization", "Bearer "+*key)
				}
				if *body != "" {
					req.Header.Set("Content-Type", "application/json")
				}

				start := time.Now()
				resp, err := client.Do(req)
				lat := time.Since(start)
				if err != nil {
					results <- result{latency: lat, ok: false}
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				code := resp.StatusCode
				_ = resp.Body.Close()
				results <- result{latency: lat, ok: code >= 200 && code < 400, status: code}
			}
		}()
	}

	benchStart := time.Now()
	close(gate)
	go func() {
		wg.Wait()
		close(results)
	}()

	var (
		latencies       []time.Duration
		success, failed int64
		codes           = map[int]int64{}
	)
	for r := range results {
		latencies = append(latencies, r.latency)
		if r.ok {
			success++
		} else {
			failed++
		}
		codes[r.status]++
	}

	report(time.Since(benchStart), latencies, success, failed, codes)
}

func report(elapsed time.Duration, lats []time.Duration, success, failed int64, codes map[int]int64) {
	sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
	total := success + failed

	fmt.Println("================ BENCHMARK REPORT ================")
	fmt.Printf("total=%d success=%d failed=%d elapsed=%s\n",
		total, success, failed, elapsed.Round(time.Millisecond))
	fmt.Printf("throughput=%.0f req/s error-rate=%.2f%%\n",
		float64(success)/elapsed.Seconds(), 100*float64(failed)/float64(total))
	if len(lats) > 0 {
		var sum time.Duration
		for _, d := range lats {
			sum += d
		}
		fmt.Printf("latency min=%s mean=%s p50=%s p90=%s p99=%s p999=%s max=%s\n",
			lats[0], sum/time.Duration(len(lats)),
			percentile(lats, .50), percentile(lats, .90),
			percentile(lats, .99), percentile(lats, .999), lats[len(lats)-1])
	}
	fmt.Print("status codes:")
	for c, n := range codes {
		fmt.Printf(" %d=%d", c, n)
	}
	fmt.Println()
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

// runMockServer 启动高并发的 OpenAI 兼容上游，用于排除 Python mock 的性能干扰。
func runMockServer(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"go-mock","object":"chat.completion","model":"mock-model","usage":{"prompt_tokens":11,"completion_tokens":22,"total_tokens":33}}`)
	})
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"mock-model","object":"model"}]}`)
	})
	fmt.Println("go mock upstream listening on", addr)
	return http.ListenAndServe(addr, mux)
}
