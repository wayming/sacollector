package client

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// HTTPClient wraps http.Client with rate limiting, default headers, and optional raw cache.
type HTTPClient struct {
	client      *http.Client
	limiter     *time.Ticker
	cacheDir    string
	rateMs      int
	active      int32
	cookie      string
	bypassCache bool
}

// NewHTTPClient creates a new HTTPClient with the given rate limit interval.
func NewHTTPClient(rateLimit time.Duration) *HTTPClient {
	ms := int(rateLimit / time.Millisecond)
	c := &HTTPClient{
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		rateMs: ms,
	}
	c.resetLimiter()
	return c
}

func (c *HTTPClient) resetLimiter() {
	if c.limiter != nil {
		c.limiter.Stop()
		c.limiter = nil
	}
	if c.rateMs > 0 {
		c.limiter = time.NewTicker(time.Duration(c.rateMs) * time.Millisecond)
	}
}

// SetCacheDir enables raw response caching to the given directory.
func (c *HTTPClient) SetCacheDir(dir string) {
	c.cacheDir = dir
}

// SetCookie sets the Cookie header for all requests.
func (c *HTTPClient) SetCookie(cookie string) { c.cookie = cookie }

// HasCookie returns true if a session cookie is set.
func (c *HTTPClient) HasCookie() bool { return c.cookie != "" }

// SetBypassCache enables/disables cache bypass.
func (c *HTTPClient) SetBypassCache(b bool) { c.bypassCache = b }

// SetRate adjusts the rate limiter interval. 0 disables rate limiting.
func (c *HTTPClient) SetRate(ms int) {
	c.rateMs = ms
	c.resetLimiter()
}

// Wait blocks until the next rate-limit slot is available. No-op if rate is 0.
func (c *HTTPClient) Wait() {
	if c.limiter == nil {
		return
	}
	<-c.limiter.C
}

// Get performs a GET request and returns the response body as bytes.
func (c *HTTPClient) Get(url string) ([]byte, error) {
	return c.getInternal(url, nil)
}

// GetWithHeaders performs a GET request with additional headers.
func (c *HTTPClient) GetWithHeaders(url string, headers map[string]string) ([]byte, error) {
	return c.getInternal(url, headers)
}

// GetWithCacheContext performs a GET with context cancellation, headers, and cache.
func (c *HTTPClient) GetWithCacheContext(ctx context.Context, url string, headers map[string]string, cacheKey string) ([]byte, error) {
	if !c.bypassCache && c.cacheDir != "" && cacheKey != "" {
		cachePath := filepath.Join(c.cacheDir, cacheKey)
		if data, err := os.ReadFile(cachePath); err == nil {
			log.Printf("[Cache] Hit: %s", cachePath)
			return data, nil
		}
	}
	data, err := c.getInternalContext(ctx, url, headers)
	if err != nil {
		return nil, err
	}
	if c.cacheDir != "" && cacheKey != "" {
		cachePath := filepath.Join(c.cacheDir, cacheKey)
		os.MkdirAll(filepath.Dir(cachePath), 0755)
		if err := os.WriteFile(cachePath, data, 0644); err != nil {
			log.Printf("[Cache] Failed to write: %v", err)
		} else {
			log.Printf("[Cache] Saved: %s", cachePath)
		}
	}
	return data, nil
}

// GetWithCache performs a GET request with headers and caches the raw response.
// cacheKey is a filesystem-friendly path like "hkg/0700/balance-sheet.json".
func (c *HTTPClient) GetWithCache(url string, headers map[string]string, cacheKey string) ([]byte, error) {
	// Try cache first
	if c.cacheDir != "" && cacheKey != "" {
		cachePath := filepath.Join(c.cacheDir, cacheKey)
		if data, err := os.ReadFile(cachePath); err == nil {
			log.Printf("[Cache] Hit: %s", cachePath)
			return data, nil
		}
	}

	// Fetch from network
	data, err := c.getInternal(url, headers)
	if err != nil {
		return nil, err
	}

	// Save to cache (skip if bypassing)
	if !c.bypassCache && c.cacheDir != "" && cacheKey != "" {
		cachePath := filepath.Join(c.cacheDir, cacheKey)
		if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
			log.Printf("[Cache] Failed to create dir: %v", err)
		} else if err := os.WriteFile(cachePath, data, 0644); err != nil {
			log.Printf("[Cache] Failed to write: %v", err)
		} else {
			log.Printf("[Cache] Saved: %s", cachePath)
		}
	}

	return data, nil
}

func (c *HTTPClient) getInternal(url string, headers map[string]string) ([]byte, error) {
	return c.getInternalContext(context.Background(), url, headers)
}

func (c *HTTPClient) getInternalContext(ctx context.Context, url string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}

	// Track concurrency
	n := atomic.AddInt32(&c.active, 1)
	defer atomic.AddInt32(&c.active, -1)

	// Log request
	log.Printf("[HTTP #%d] GET %s", n, url)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body[:min(len(body), 500)]))
	}

	return body, nil
}

// ProbeURL does a lightweight GET to check if a URL exists.
func (c *HTTPClient) ProbeURL(url string) bool {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	io.Copy(io.Discard, resp.Body)
	body := string(buf[:n])

	if strings.Contains(body, `"type":"error"`) || strings.Contains(body, `"message":"Unauthorized"`) {
		return false
	}

	return true
}

// GetJSON performs a GET request and expects a JSON response (Status 200).
func (c *HTTPClient) GetJSON(url string) ([]byte, error) {
	return c.Get(url)
}

// Active returns the current in-flight request count.
func (c *HTTPClient) Active() int32 {
	return atomic.LoadInt32(&c.active)
}

// Stop releases the rate limiter resources.
func (c *HTTPClient) Stop() {
	if c.limiter != nil {
		c.limiter.Stop()
	}
}
