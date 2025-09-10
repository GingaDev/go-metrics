package main

import (
	"context"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
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

func metricsMiddleware(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inflightRequests.Inc()
		start := time.Now()
		rr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rr, r)
		inflightRequests.Dec()

		httpRequestDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())
		httpRequestsTotal.WithLabelValues(route, http.StatusText(rr.status)).Inc()
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
	mux := http.NewServeMux()

	// Health endpoint
	mux.Handle("/", metricsMiddleware("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})))

	// Example business endpoint (simulates some latency + random error)
	mux.Handle("/work", metricsMiddleware("/work", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// simulate variable latency
		time.Sleep(time.Duration(50+rand.Intn(200)) * time.Millisecond)
		if rand.Float64() < 0.1 {
			http.Error(w, "random error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("did some work\n"))
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
			case <-ctx.Done():
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

	log.Printf("listening on %s", addr)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}