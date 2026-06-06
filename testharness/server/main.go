// testharness/server — demo HTTP server wired with the ratelimiter library.
//
// Usage:
//
//	go run ./testharness/server
//
// Flags:
//
//	-addr   listen address (default ":8080")
//	-limit  requests allowed per window (default 5)
//	-window window duration, e.g. 10s, 1m (default "10s")
//	-burst  burst size >= limit (default == limit)
//	-algo   algorithm: tokenbucket|fixedwindow|slidinglog|slidingcounter (default "tokenbucket")
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/justinclev/slow-your-roll/algorithm/fixedwindow"
	"github.com/justinclev/slow-your-roll/algorithm/slidingwindowcounter"
	"github.com/justinclev/slow-your-roll/algorithm/slidingwindowlog"
	"github.com/justinclev/slow-your-roll/algorithm/tokenbucket"
	"github.com/justinclev/slow-your-roll/domain"
	"github.com/justinclev/slow-your-roll/limiter"
	"github.com/justinclev/slow-your-roll/middleware"
	"github.com/justinclev/slow-your-roll/store/memory"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	limit := flag.Int("limit", 5, "requests allowed per window")
	window := flag.Duration("window", 10*time.Second, "window duration")
	burst := flag.Int("burst", 0, "burst size (0 = same as limit)")
	algo := flag.String("algo", "tokenbucket", "algorithm: tokenbucket|fixedwindow|slidinglog|slidingcounter")
	flag.Parse()

	if *burst == 0 {
		*burst = *limit
	}

	policy, err := domain.NewPolicy(*limit, *window, *burst)
	if err != nil {
		log.Fatalf("invalid policy: %v", err)
	}

	store := memory.New()
	defer store.Close()

	l := limiter.New(policy, pickAlgo(*algo), store)

	mux := http.NewServeMux()

	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"healthy"}`)
	})

	limited := middleware.Handler(l, middleware.IPKeyFunc)(mux)

	log.Printf("server listening on %s  limit=%d/%s burst=%d algo=%s",
		*addr, *limit, *window, *burst, *algo)

	if err := http.ListenAndServe(*addr, limited); err != nil {
		log.Fatal(err)
	}
}

func pickAlgo(name string) domain.Algorithm {
	switch name {
	case "fixedwindow":
		return fixedwindow.New()
	case "slidinglog":
		return slidingwindowlog.New()
	case "slidingcounter":
		return slidingwindowcounter.New()
	default:
		return tokenbucket.New()
	}
}
