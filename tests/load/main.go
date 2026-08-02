// Command load is a minimal HTTP load-test tool for gateway-go — no
// external dependency (k6, Locust) required to run it, which matters
// here specifically: neither is installed in this project's own
// development environment, so a load test that needs one can never
// actually run here. Stdlib-only (net/http, sync, sort) means `go run
// tests/load/main.go` works anywhere a Go toolchain does.
//
// Usage:
//
//	go run tests/load/main.go -url http://localhost:8080/healthz -concurrency 20 -duration 10s
//	go run tests/load/main.go -url http://localhost:8080/api/v1/search?q=tesla -header "Authorization: Bearer <token>"
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	url := flag.String("url", "http://localhost:8080/healthz", "target URL")
	concurrency := flag.Int("concurrency", 20, "number of concurrent workers")
	duration := flag.Duration("duration", 10*time.Second, "how long to run")
	header := flag.String("header", "", `extra request header, e.g. "Authorization: Bearer <token>"`)
	flag.Parse()

	var reqHeader, reqValue string
	if *header != "" {
		parts := strings.SplitN(*header, ":", 2)
		if len(parts) != 2 {
			fmt.Fprintln(os.Stderr, `-header must be in the form "Name: value"`)
			os.Exit(1)
		}
		reqHeader, reqValue = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}

	var (
		mu         sync.Mutex
		latencies  []time.Duration
		successes  atomic.Int64
		errors     atomic.Int64
		statusErrs atomic.Int64
	)

	client := &http.Client{Timeout: 10 * time.Second}
	stop := time.Now().Add(*duration)

	var wg sync.WaitGroup
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(stop) {
				start := time.Now()
				req, err := http.NewRequest(http.MethodGet, *url, nil)
				if err != nil {
					errors.Add(1)
					continue
				}
				if reqHeader != "" {
					req.Header.Set(reqHeader, reqValue)
				}

				resp, err := client.Do(req)
				elapsed := time.Since(start)
				if err != nil {
					errors.Add(1)
					continue
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()

				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					successes.Add(1)
				} else {
					statusErrs.Add(1)
				}

				mu.Lock()
				latencies = append(latencies, elapsed)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	total := successes.Load() + errors.Load() + statusErrs.Load()
	fmt.Printf("target:        %s\n", *url)
	fmt.Printf("concurrency:   %d\n", *concurrency)
	fmt.Printf("duration:      %s\n", *duration)
	fmt.Printf("total requests: %d (%.1f req/s)\n", total, float64(total)/duration.Seconds())
	fmt.Printf("successes:     %d\n", successes.Load())
	fmt.Printf("non-2xx:       %d\n", statusErrs.Load())
	fmt.Printf("errors:        %d\n", errors.Load())
	if len(latencies) > 0 {
		fmt.Printf("latency p50:   %s\n", percentile(latencies, 50))
		fmt.Printf("latency p95:   %s\n", percentile(latencies, 95))
		fmt.Printf("latency p99:   %s\n", percentile(latencies, 99))
		fmt.Printf("latency max:   %s\n", latencies[len(latencies)-1])
	}
}

func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p * len(sorted)) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
