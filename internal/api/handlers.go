package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"sacollector/internal/client"
	"sacollector/internal/exporter"
	"sacollector/internal/financials"
	"sacollector/internal/parser"
	"sacollector/internal/screener"
	"sacollector/internal/store"
)

// Server holds all dependencies for the API.
type Server struct {
	RedisStore *store.RedisStore
	Exporter   *exporter.Exporter
	Collector  *financials.Collector
	Screener   *screener.Screener
	HTTPClient *client.HTTPClient
	LogBroker  *LogBroker
	OutputDir  string

	mu         sync.Mutex
	jobRunning bool
	jobCancel  context.CancelFunc
	jobStatus  *JobStatus
	jobStore   *store.RedisStore
}

// JobStatus represents the current job state.
type JobStatus struct {
	Exchange  string `json:"exchange"`
	State     string `json:"state"` // running, stopping, done
	Total     int    `json:"total"`
	Done      int    `json:"done"`
	StartedAt string `json:"started_at"`
}

// StartJobRequest is the POST body for starting a job.
type StartJobRequest struct {
	Exchange string `json:"exchange"`
	Limit    int    `json:"limit,omitempty"`
	Symbol   string `json:"symbol,omitempty"`  // single stock mode
	Workers  int    `json:"workers,omitempty"`
	Cookie      string `json:"cookie,omitempty"`
	BypassCache bool   `json:"bypassCache,omitempty"`
	RateLimit int    `json:"rateLimit,omitempty"`
}

// RegisterRoutes sets up all API routes on the given mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/jobs", s.handleJobs)
	mux.HandleFunc("/api/jobs/status", s.handleJobStatus)
	mux.HandleFunc("/api/errors", s.handleErrors)
	mux.HandleFunc("/api/stocks", s.handleStocks)
	mux.HandleFunc("/api/output/list", s.handleOutputList)
	mux.HandleFunc("/api/output/stock/", s.handleOutputStock)
	if s.LogBroker != nil {
		mux.HandleFunc("/api/logs", s.LogBroker.HandleLogs)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	running := s.jobRunning
	s.mu.Unlock()

	redisOK := s.RedisStore.Ping() == nil
	status := "idle"
	if running {
		status = "running"
	}

	writeJSON(w, map[string]interface{}{
		"redis":  redisOK,
		"status": status,
	})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.startJob(w, r)
	case http.MethodDelete:
		s.stopJob(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) startJob(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.jobRunning {
		s.mu.Unlock()
		writeJSON(w, map[string]string{"error": "Job already running"})
		w.WriteHeader(http.StatusConflict)
		return
	}
	s.mu.Unlock()

	var req StartJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Exchange = strings.ToUpper(req.Exchange)
	if req.Exchange == "" {
		req.Exchange = "HKG"
	}

	ctx, cancel := context.WithCancel(context.Background())

	s.mu.Lock()
	s.jobRunning = true
	s.HTTPClient.SetRate(req.RateLimit) // 0 = no limit
	s.HTTPClient.SetCookie(req.Cookie)
	s.HTTPClient.SetBypassCache(req.BypassCache)
	s.jobCancel = cancel
	s.jobStatus = &JobStatus{
		Exchange:  req.Exchange,
		State:     "running",
		StartedAt: time.Now().Format(time.RFC3339),
	}
	s.mu.Unlock()

	// Create per-job Redis store with exchange prefix
	jobRedis := store.NewRedisStoreFromClient(s.RedisStore.Client(), req.Exchange)

	s.mu.Lock()
	s.jobStore = jobRedis
	s.mu.Unlock()

	// Run collector in background goroutine
	go func() {
		defer func() {
			s.mu.Lock()
			s.jobRunning = false
			if s.jobStatus != nil {
				s.jobStatus.State = "done"
			}
			s.mu.Unlock()
		}()

		exchangeLower := strings.ToLower(req.Exchange)

		// Determine which stocks to process
		var allStocks []parser.StockInfo

		if req.Symbol != "" {
			allStocks = []parser.StockInfo{{Code: req.Symbol, Name: req.Symbol}}
			log.Printf("[API] Single symbol mode: %s", req.Symbol)
		} else {
			log.Printf("[API] Phase 1: Fetching %s stocks...", req.Exchange)
			allPages, err := s.Screener.FetchAllStocks(req.Exchange)
			if err != nil {
				log.Printf("[API] Phase 1 failed: %v", err)
				return
			}
			for i, pageStocks := range allPages {
				stockMap := parser.BuildStockMap(pageStocks)
				s.Exporter.ExportStockList(req.Exchange, stockMap, i+1)
				allStocks = append(allStocks, pageStocks...)
			}
			if req.Limit > 0 && req.Limit < len(allStocks) {
				allStocks = allStocks[:req.Limit]
			}
		}

		// Enqueue
		jobRedis.EnqueueAll(allStocks)

		// Phase 2
		log.Printf("[API] Phase 2: Processing %d stocks...", len(allStocks))
		collector := s.Collector
	if req.Workers > 0 {
		collector = financials.New(s.HTTPClient, req.Workers)
	}
	collector.FetchFromQueueWithContext(ctx, jobRedis, exchangeLower, s.Exporter)
		log.Printf("[API] Job complete: %s", req.Exchange)
	}()

	writeJSON(w, s.jobStatus)
}

func (s *Server) stopJob(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.jobRunning {
		writeJSON(w, map[string]string{"error": "No job running"})
		w.WriteHeader(http.StatusNotFound)
		return
	}

	s.jobCancel()
	if s.jobStatus != nil {
		s.jobStatus.State = "stopping"
	}
	writeJSON(w, map[string]string{"status": "stopping"})
}

func (s *Server) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	store := s.jobStore
	if s.jobStatus == nil || store == nil {
		writeJSON(w, map[string]interface{}{
			"state": "idle",
			"done":  0,
			"total": 0,
		})
		return
	}

	done, total := store.Progress()
	s.jobStatus.Done = done
	s.jobStatus.Total = total

	remaining := store.GetRemainingQueue()
	errors := store.GetErrors()

	writeJSON(w, map[string]interface{}{
		"state":     s.jobStatus.State,
		"exchange":  s.jobStatus.Exchange,
		"done":      done,
		"total":     total,
		"remaining": remaining,
		"errors":    errors,
		"started":   s.jobStatus.StartedAt,
	})
}

func (s *Server) handleErrors(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	store := s.jobStore
	s.mu.Unlock()

	var errors []string
	if store != nil {
		errors = store.GetErrors()
	}
	writeJSON(w, map[string]interface{}{
		"errors": errors,
	})
}

// handleStocks returns the stock list for an exchange (for the frontend picker).
func (s *Server) handleStocks(w http.ResponseWriter, r *http.Request) {
	exchange := r.URL.Query().Get("exchange")
	if exchange == "" {
		exchange = "HKG"
	}
	exchange = strings.ToUpper(exchange)

	allPages, err := s.Screener.FetchAllStocks(exchange)
	if err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}

	var stocks []map[string]string
	seen := make(map[string]bool)
	for _, page := range allPages {
		for _, st := range page {
			if seen[st.Code] {
				continue
			}
			seen[st.Code] = true
			stocks = append(stocks, map[string]string{
				"code": st.Code,
				"name": st.Name,
			})
		}
	}

	writeJSON(w, map[string]interface{}{
		"exchange": exchange,
		"stocks":   stocks,
	})
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}


func (s *Server) handleOutputList(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(s.OutputDir, "financials")
	exchanges, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, map[string]interface{}{"stocks": []interface{}{}})
		return
	}
	type se struct { Code string `json:"code"`; Exchange string `json:"exchange"`; Files []string `json:"files"` }
	var stocks []se
	for _, ex := range exchanges {
		if !ex.IsDir() { continue }
		codes, _ := os.ReadDir(filepath.Join(dir, ex.Name()))
		for _, e := range codes {
			if !e.IsDir() { continue }
			files, _ := os.ReadDir(filepath.Join(dir, ex.Name(), e.Name()))
			var names []string
			for _, f := range files { names = append(names, f.Name()) }
			stocks = append(stocks, se{Code: e.Name(), Exchange: strings.ToUpper(ex.Name()), Files: names})
		}
	}
	sort.Slice(stocks, func(i, j int) bool { return stocks[i].Code > stocks[j].Code })
	writeJSON(w, map[string]interface{}{"stocks": stocks})
}

func (s *Server) handleOutputStock(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/output/stock/"), "/")
	if len(parts) < 2 { http.Error(w, "need exchange/code", http.StatusBadRequest); return }
	exchange, code := parts[0], parts[1]
	dir := filepath.Join(s.OutputDir, "financials", strings.ToLower(exchange), code)
	entries, err := os.ReadDir(dir)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	type fc struct { Name string `json:"name"`; Data interface{} `json:"data"` }
	var files []fc
	for _, e := range entries {
		if e.IsDir() { continue }
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil { continue }
		var d interface{}
		json.Unmarshal(raw, &d)
		files = append(files, fc{Name: e.Name(), Data: d})
	}
	writeJSON(w, map[string]interface{}{"code": code, "files": files})
}