// testharness/compare — runs the same request pattern through all four algorithms
// in-process and prints a side-by-side comparison.
//
// Usage:
//
//	go run ./testharness/compare
//
// Flags:
//
//	-limit   requests allowed per window (default 5)
//	-window  window duration, e.g. 10s, 1m (default "10s")
//	-burst   burst size >= limit (default == limit)
//	-n       requests to fire per burst (default 8)
//	-bursts  number of bursts to fire (default 3)
//	-gap     time to advance clock between bursts (default == window/2)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/justinclev/slow-your-roll/algorithm/fixedwindow"
	"github.com/justinclev/slow-your-roll/algorithm/slidingwindowcounter"
	"github.com/justinclev/slow-your-roll/algorithm/slidingwindowlog"
	"github.com/justinclev/slow-your-roll/algorithm/tokenbucket"
	"github.com/justinclev/slow-your-roll/domain"
	"github.com/justinclev/slow-your-roll/internal/testclock"
	"github.com/justinclev/slow-your-roll/limiter"
	"github.com/justinclev/slow-your-roll/store/memory"
)

type entry struct {
	burst     int
	req       int
	allowed   bool
	remaining int
	retryMs   int64
}

type suite struct {
	name    string
	entries []entry
}

func main() {
	limitN := flag.Int("limit", 5, "requests allowed per window")
	window := flag.Duration("window", 10*time.Second, "window duration")
	burst := flag.Int("burst", 0, "burst size (0 = same as limit)")
	n := flag.Int("n", 8, "requests per burst")
	bursts := flag.Int("bursts", 3, "number of bursts")
	gap := flag.Duration("gap", 0, "clock advance between bursts (0 = window/2)")
	flag.Parse()

	if *burst == 0 {
		*burst = *limitN
	}
	if *gap == 0 {
		*gap = *window / 2
	}

	policy, err := domain.NewPolicy(*limitN, *window, *burst)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid policy: %v\n", err)
		os.Exit(1)
	}

	algos := []struct {
		name string
		algo domain.Algorithm
	}{
		{"tokenbucket", tokenbucket.New()},
		{"fixedwindow", fixedwindow.New()},
		{"slidinglog", slidingwindowlog.New()},
		{"slidingcounter", slidingwindowcounter.New()},
	}

	origin := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	key := domain.Key("test-subject")
	ctx := context.Background()

	suites := make([]suite, len(algos))
	for i, a := range algos {
		clk := testclock.New(origin)
		store := memory.New()
		store.WithClock(clk.Now)
		l := limiter.New(policy, a.algo, store, limiter.WithClock(clk))

		var entries []entry
		for b := range *bursts {
			for r := range *n {
				result, err := l.Allow(ctx, key)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[%s] Allow error: %v\n", a.name, err)
					os.Exit(1)
				}
				entries = append(entries, entry{
					burst:     b + 1,
					req:       r + 1,
					allowed:   result.Allowed,
					remaining: result.Remaining,
					retryMs:   result.RetryAfter.Milliseconds(),
				})
			}
			clk.Advance(*gap)
		}
		store.Close()
		suites[i] = suite{name: a.name, entries: entries}
	}

	printComparison(suites, policy, *window, *gap, *n, *bursts)
}

func printComparison(suites []suite, policy domain.Policy, window, gap time.Duration, n, bursts int) {
	total := n * bursts

	fmt.Printf("\nPolicy: limit=%d  window=%s  burst=%d\n", policy.Limit(), window, policy.Burst())
	fmt.Printf("Pattern: %d burst(s) × %d requests, clock +%s between bursts\n\n", bursts, n, gap)

	// Per-request detail per algorithm
	for _, s := range suites {
		fmt.Printf("─── %s ─────────────────────────────\n", s.name)
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "BURST\tREQ\tSTATUS\tREMAINING\tRETRY-AFTER")
		for _, e := range s.entries {
			status := allowedStr(e.allowed)
			retry := "-"
			if e.retryMs > 0 {
				retry = fmt.Sprintf("%dms", e.retryMs)
			}
			fmt.Fprintf(tw, "%d\t%d\t%s\t%d\t%s\n", e.burst, e.req, status, e.remaining, retry)
		}
		tw.Flush()
		fmt.Println()
	}

	// Summary table
	fmt.Println("─── Summary ─────────────────────────────────────────")
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ALGORITHM\tALLOWED\tDENIED\tALLOW RATE\tPATTERN")
	fmt.Fprintln(tw, "---------\t-------\t------\t----------\t-------")
	for _, s := range suites {
		allowed, denied := 0, 0
		for _, e := range s.entries {
			if e.allowed {
				allowed++
			} else {
				denied++
			}
		}
		rate := float64(allowed) / float64(total) * 100
		pattern := buildPattern(s.entries)
		fmt.Fprintf(tw, "%s\t%d\t%d\t%.0f%%\t%s\n", s.name, allowed, denied, rate, pattern)
	}
	tw.Flush()
	fmt.Println()
}

func allowedStr(ok bool) string {
	if ok {
		return "✓ allowed"
	}
	return "✗ denied "
}

// buildPattern renders a compact visual: ✓ for allowed, ✗ for denied, | between bursts.
func buildPattern(entries []entry) string {
	var sb strings.Builder
	prev := 0
	for _, e := range entries {
		if e.burst != prev {
			if prev != 0 {
				sb.WriteString(" | ")
			}
			prev = e.burst
		}
		if e.allowed {
			sb.WriteRune('✓')
		} else {
			sb.WriteRune('✗')
		}
	}
	return sb.String()
}
