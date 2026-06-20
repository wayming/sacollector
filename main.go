package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"sacollector/internal/api"
	"sacollector/internal/client"
	"sacollector/internal/exporter"
	"sacollector/internal/financials"
	"sacollector/internal/screener"
	"sacollector/internal/store"
)

func main() {
	port := flag.String("port", "8080", "Web server port")
	outputDir := flag.String("output", "./output", "Output directory")
	workers := flag.Int("workers", 4, "Worker count")
	rateLimitMs := flag.Int("rate-limit", 1000, "Rate limit ms")
	redisAddr := flag.String("redis-addr", "127.0.0.1:6379", "Redis address")
	devMode := flag.Bool("dev", false, "Dev mode: serve CORS for localhost:5173")
	flag.Parse()

	log.Printf("=== sacollector server ===")
	log.Printf("Port: %s, Output: %s", *port, *outputDir)

	// HTTP client
	httpClient := client.NewHTTPClient(time.Duration(*rateLimitMs) * time.Millisecond)
	httpClient.SetCacheDir(*outputDir + "/raw")
	defer httpClient.Stop()

	// Redis
	redisStore := store.NewRedisStore(*redisAddr, "default")
	if err := redisStore.Ping(); err != nil {
		log.Printf("Redis not available (%s) — checkpoint disabled", *redisAddr)
	} else {
		log.Printf("Redis: connected (%s)", *redisAddr)
	}
	defer redisStore.Close()

	// Components
	scr := screener.New(httpClient)
	finCollector := financials.New(httpClient, *workers)
	exp := exporter.New(*outputDir)

	// Log broker: captures all log output and broadcasts via SSE
	logBroker := &api.LogBroker{}
	logBroker.AttachToLog()

	// API server
	srv := &api.Server{
		RedisStore: redisStore,
		Exporter:   exp,
		Collector:  finCollector,
		Screener:   scr,
		HTTPClient: httpClient,
		LogBroker:  logBroker,
		OutputDir:  *outputDir,
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// Static files (production) or CORS proxy (dev)
	if *devMode {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "http://localhost:5173")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
			http.Error(w, "Dev mode: use Vite at http://localhost:5173", http.StatusNotFound)
		})
	} else {
		webDir := "web/dist"
		if _, err := os.Stat(webDir); err == nil {
			fs := http.FileServer(http.Dir(webDir))
			mux.Handle("/", fs)
			log.Printf("Serving static files from %s", webDir)
		} else {
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/" {
					w.Write([]byte(indexPage))
					return
				}
				http.NotFound(w, r)
			})
			log.Printf("No web/dist found, serving placeholder page")
		}
	}

	log.Printf("Listening on :%s", *port)
	if err := http.ListenAndServe(":"+*port, mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

const indexPage = `<!DOCTYPE html>
<html><head><title>sacollector</title></head>
<body><h1>sacollector API</h1>
<p>Build the frontend: <code>cd web && npm run build</code></p>
<p>API: <a href="/api/health">/api/health</a></p>
</body></html>`
