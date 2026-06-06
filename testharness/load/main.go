// testharness/load — fires requests at the demo server and reports rate limit behaviour.
//
// Usage:
//
//	go run ./testharness/load
//
// Flags:
//
//	-url     target URL (default "http://localhost:8080/ping")
//	-n       number of requests (default 20)
//	-delay   delay between requests, e.g. 200ms, 1s (default "0")
//	-c       concurrency — parallel workers (default 1, sequential)
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"
)

type result struct {
	seq        int
	status     int
	remaining  string
	reset      string
	retryAfter string
	latency    time.Duration
}

func main() {
	url := flag.String("url", "http://localhost:8080/ping", "target URL")
	n := flag.Int("n", 20, "number of requests")
	delay := flag.Duration("delay", 0, "delay between requests (0 = none)")
	concurrency := flag.Int("c", 1, "parallel workers")
	flag.Parse()

	results := make([]result, *n)
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		seq     int32
		allowed int32
		denied  int32
		errors  int32
	)

	client := &http.Client{Timeout: 5 * time.Second}

	worker := func() {
		defer wg.Done()
		for {
			i := int(atomic.AddInt32(&seq, 1)) - 1
			if i >= *n {
				return
			}
			if *delay > 0 && i > 0 {
				time.Sleep(*delay)
			}

			start := time.Now()
			resp, err := client.Get(*url)
			lat := time.Since(start)

			r := result{seq: i + 1, latency: lat}
			if err != nil {
				r.status = 0
				atomic.AddInt32(&errors, 1)
			} else {
				r.status = resp.StatusCode
				r.remaining = resp.Header.Get("X-RateLimit-Remaining")
				r.reset = formatReset(resp.Header.Get("X-RateLimit-Reset"))
				r.retryAfter = resp.Header.Get("Retry-After")
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					atomic.AddInt32(&allowed, 1)
				} else if resp.StatusCode == http.StatusTooManyRequests {
					atomic.AddInt32(&denied, 1)
				}
			}

			mu.Lock()
			results[i] = r
			mu.Unlock()
		}
	}

	wg.Add(*concurrency)
	for range *concurrency {
		go worker()
	}
	wg.Wait()

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tSTATUS\tREMAINING\tRESET\tRETRY-AFTER\tLATENCY")
	fmt.Fprintln(tw, "-\t------\t---------\t-----\t-----------\t-------")

	for _, r := range results {
		status := statusStr(r.status)
		remaining := dash(r.remaining)
		reset := dash(r.reset)
		retryAfter := dash(r.retryAfter)
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n",
			r.seq, status, remaining, reset, retryAfter, r.latency.Round(time.Millisecond))
	}
	tw.Flush()

	fmt.Printf("\n  requests: %d  allowed: %d  denied: %d  errors: %d\n\n",
		*n, allowed, denied, errors)
}

func statusStr(code int) string {
	switch code {
	case http.StatusOK:
		return "200 OK"
	case http.StatusTooManyRequests:
		return "429 TOO MANY REQUESTS"
	case 0:
		return "ERR (no response)"
	default:
		return strconv.Itoa(code)
	}
}

func formatReset(unix string) string {
	if unix == "" {
		return ""
	}
	n, err := strconv.ParseInt(unix, 10, 64)
	if err != nil {
		return unix
	}
	t := time.Unix(n, 0)
	return t.Format("15:04:05")
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
