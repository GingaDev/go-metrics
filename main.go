package main

import (
	"context"
	"log"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// A counter: total number of handled requests, labeled by route + status.
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "example_http_requests_total",
			Help: "Total number of HTTP requests processed.",
		},
		[]string{"route", "status"},
	)

	// A histogram: request duration in seconds.
	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "example_http_request_duration_seconds",
			Help:    "Duration of HTTP requests.",
			Buckets: prometheus.DefBuckets, // good default buckets
		},
		[]string{"route"},
	)

	// A gauge: number of in-flight requests (tracked via middleware).
	inflightRequests = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "example_inflight_requests",
		Help: "Current number of in-flight HTTP requests.",
	})

	// A counter updated by a background job.
	backgroundJobsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "example_background_jobs_total",
		Help: "Total background jobs executed.",
	})
)

// Global logger (set in main)
var logger *slog.Logger

func metricsMiddleware(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inflightRequests.Inc()
		start := time.Now()
		rr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		// Request start (DEBUG)
		logger.LogAttrs(r.Context(), slog.LevelDebug, "request.start",
			slog.String("route", route),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("remote_ip", clientIP(r)),
		)

		next.ServeHTTP(rr, r)
		inflightRequests.Dec()

		dur := time.Since(start)
		httpRequestDuration.WithLabelValues(route).Observe(dur.Seconds())
		httpRequestsTotal.WithLabelValues(route, http.StatusText(rr.status)).Inc()

		// Request end: INFO for <500, WARN for 4xx, ERROR for 5xx
		level := slog.LevelInfo
		if rr.status >= 500 {
			level = slog.LevelError
		} else if rr.status >= 400 {
			level = slog.LevelWarn
		}
		logger.LogAttrs(r.Context(), level, "request.end",
			slog.String("route", route),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("remote_ip", clientIP(r)),
			slog.Int("status", rr.status),
			slog.Int64("duration_ms", dur.Milliseconds()),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func main() {
	// --- Logger setup ---
	level := parseLevel(getEnv("LOG_LEVEL", "info"))
	var handler slog.Handler
	switch strings.ToLower(getEnv("LOG_FORMAT", "json")) {
	case "text":
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	default:
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	}
	logger = slog.New(handler)
	slog.SetDefault(logger) // optional: makes slog.X helpers use our default

	mux := http.NewServeMux()

	// Health endpoint
	mux.Handle("/", metricsMiddleware("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.LogAttrs(r.Context(), slog.LevelInfo, "health.check", slog.String("path", r.URL.Path))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})))

	// Example business endpoint (simulates some latency + random error)
	mux.Handle("/work", metricsMiddleware("/work", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// simulate variable latency
		sleepMs := 50 + rand.Intn(200)
		time.Sleep(time.Duration(sleepMs) * time.Millisecond)

		// Log at DEBUG to show internal timing choice
		logger.LogAttrs(r.Context(), slog.LevelDebug, "work.simulating",
			slog.Int("latency_ms", sleepMs),
		)

		if rand.Float64() < 0.1 {
			// example of an ERROR level application log
			errMsg := "random error"
			logger.LogAttrs(r.Context(), slog.LevelError, "work.failed",
				slog.String("error", errMsg),
			)
			http.Error(w, errMsg, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("did some work\n"))
		// Business success (INFO)
		logger.LogAttrs(r.Context(), slog.LevelInfo, "work.done")
	})))

	// Prometheus scrape endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// background job that increments a counter
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				backgroundJobsTotal.Inc()
				// Background heartbeat (DEBUG)
				logger.LogAttrs(context.Background(), slog.LevelDebug, "job.tick",
					slog.String("job", "background_counter"),
					slog.Time("ts", time.Now()),
				)
			case <-ctx.Done():
				logger.LogAttrs(context.Background(), slog.LevelInfo, "job.stopping",
					slog.String("job", "background_counter"),
				)
				return
			}
		}
	}()

	addr := getEnv("ADDR", ":8080")
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	logger.LogAttrs(context.Background(), slog.LevelInfo, "server.listening", slog.String("addr", addr))
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// Fatal server error
			logger.LogAttrs(context.Background(), slog.LevelError, "server.error", slog.String("error", err.Error()))
			// keep using log.Fatalf to ensure non-zero exit when needed
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	logger.LogAttrs(context.Background(), slog.LevelWarn, "server.shutting_down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.LogAttrs(context.Background(), slog.LevelError, "server.shutdown_error", slog.String("error", err.Error()))
	} else {
		logger.LogAttrs(context.Background(), slog.LevelInfo, "server.stopped")
	}
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func parseLevel(s string) slog.Leveler {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		// allow numeric levels too (e.g., LOG_LEVEL=-4 for debug)
		if n, err := strconv.Atoi(s); err == nil {
			l := slog.Level(n)
			return l
		}
		return slog.LevelInfo
	}
}

func clientIP(r *http.Request) string {
	// Common proxy headers first
	hdrs := []string{"X-Forwarded-For", "X-Real-IP", "CF-Connecting-IP"}
	for _, h := range hdrs {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			parts := strings.Split(v, ",")
			return strings.TrimSpace(parts[0])
		}
	}
	// Fallback to RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}