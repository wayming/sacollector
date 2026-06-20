package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"sacollector/internal/parser"
)

// RedisStore manages the work queue and state via Redis.
type RedisStore struct {
	client *redis.Client
	prefix string // "Exchange:HKG"
}

// NewRedisStore creates a new RedisStore. addr is "host:port".
func NewRedisStore(addr, exchange string) *RedisStore {
	client := redis.NewClient(&redis.Options{
		Addr:        addr,
		DialTimeout: 5 * time.Second,
	})
	return &RedisStore{
		client: client,
		prefix: fmt.Sprintf("Exchange:%s", exchange),
	}
}

// Client returns the underlying Redis client (for creating exchange-specific stores).
func (s *RedisStore) Client() *redis.Client { return s.client }

// NewRedisStoreFromClient creates a new RedisStore sharing an existing client with a different prefix.
func NewRedisStoreFromClient(client *redis.Client, exchange string) *RedisStore {
	return &RedisStore{
		client: client,
		prefix: fmt.Sprintf("Exchange:%s", exchange),
	}
}

// Ping checks the Redis connection.
func (s *RedisStore) Ping() error {
	return s.client.Ping(context.Background()).Err()
}

// Close closes the Redis connection.
func (s *RedisStore) Close() error {
	return s.client.Close()
}

// --- keys ---

func (s *RedisStore) queueKey() string            { return s.prefix + ":queue" }
func (s *RedisStore) processingKey() string       { return s.prefix + ":processing" }
func (s *RedisStore) metaKey() string             { return s.prefix + ":meta" }
func (s *RedisStore) doneKey(code string) string  { return s.prefix + ":done:" + code }
func (s *RedisStore) errorQueueKey() string       { return s.prefix + ":errors" }
func (s *RedisStore) retryKey(code string) string { return s.prefix + ":retry:" + code }

var maxRetries = 3

// --- Phase 1: Enqueue ---

// EnqueueAll pushes all stock codes into the Redis queue and sets meta.
func (s *RedisStore) EnqueueAll(stocks []parser.StockInfo) error {
	ctx := context.Background()

	// Flush old state for this exchange, but first recover any stuck processing items
	s.recoverProcessing(ctx)
	s.client.Del(ctx, s.queueKey(), s.processingKey(), s.metaKey(), s.errorQueueKey())

	if len(stocks) == 0 {
		return nil
	}

	// RPUSH all codes
	codes := make([]interface{}, len(stocks))
	for i, st := range stocks {
		codes[i] = st.Code
	}
	if err := s.client.RPush(ctx, s.queueKey(), codes...).Err(); err != nil {
		return fmt.Errorf("enqueuing stocks: %w", err)
	}

	// Store name mapping as a hash for -continue lookups
	nameMap := make(map[string]interface{}, len(stocks))
	for _, st := range stocks {
		nameMap[st.Code] = st.Name
	}
	pipe := s.client.Pipeline()
	pipe.HSet(ctx, s.metaKey(), "total", len(stocks), "processed", 0, "exchange", s.prefix)
	pipe.HSet(ctx, s.metaKey()+"_names", nameMap)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("setting meta: %w", err)
	}

	log.Printf("[Redis] Enqueued %d stocks to %s", len(stocks), s.queueKey())
	return nil
}

// --- Phase 2: Workers ---

// Dequeue atomically moves the next stock from queue to processing list.
// On resume, orphaned processing items are recovered back to the queue.
func (s *RedisStore) Dequeue(timeout time.Duration) (parser.StockInfo, bool) {
	ctx := context.Background()

	// BLMove: queue → processing (atomic, crash-safe)
	code, err := s.client.BLMove(ctx, s.queueKey(), s.processingKey(), "RIGHT", "LEFT", timeout).Result()
	if err != nil || code == "" {
		return parser.StockInfo{}, false
	}

	name, _ := s.client.HGet(ctx, s.metaKey()+"_names", code).Result()

	return parser.StockInfo{Code: code, Name: name}, true
}

// MarkDone marks a stock as successfully processed, removes from processing, and clears any stale errors.
func (s *RedisStore) MarkDone(code string) {
	ctx := context.Background()
	s.clearErrorForCode(ctx, code)
	pipe := s.client.Pipeline()
	pipe.Set(ctx, s.doneKey(code), "ok", 0)
	pipe.HIncrBy(ctx, s.metaKey(), "processed", 1)
	pipe.LRem(ctx, s.processingKey(), 1, code)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("[Redis] MarkDone error: %v", err)
	}
}

// clearErrorForCode removes any error entries for a stock from the error queue.
func (s *RedisStore) clearErrorForCode(ctx context.Context, code string) {
	prefix := code + ":"
	items, err := s.client.LRange(ctx, s.errorQueueKey(), 0, -1).Result()
	if err != nil || len(items) == 0 {
		return
	}
	for _, item := range items {
		if len(item) >= len(prefix) && item[:len(prefix)] == prefix {
			s.client.LRem(ctx, s.errorQueueKey(), 1, item)
		}
	}
}

// MarkFailed marks a stock as failed (no financial data), writes reason to error queue.
func (s *RedisStore) MarkFailed(code string, reason string) {
	ctx := context.Background()
	pipe := s.client.Pipeline()
	pipe.Set(ctx, s.doneKey(code), "failed", 0)
	pipe.HIncrBy(ctx, s.metaKey(), "processed", 1)
	pipe.LRem(ctx, s.processingKey(), 1, code)
	pipe.RPush(ctx, s.errorQueueKey(), fmt.Sprintf("%s: %s", code, reason))
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("[Redis] MarkFailed error: %v", err)
	}
	log.Printf("[Redis] Failed: %s — %s", code, reason)
}

// Requeue moves a stock from processing back to the pending queue (server error, retry).
// Returns false if max retries exceeded (in which case it marks as failed).
func (s *RedisStore) Requeue(code string, reason string) bool {
	ctx := context.Background()

	retries, _ := s.client.Incr(ctx, s.retryKey(code)).Result()
	if retries > int64(maxRetries) {
		s.MarkFailed(code, fmt.Sprintf("max retries exceeded: %s", reason))
		return false
	}

	// Move from processing back to pending queue
	pipe := s.client.Pipeline()
	pipe.LRem(ctx, s.processingKey(), 1, code)
	pipe.LPush(ctx, s.queueKey(), code)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("[Redis] Requeue error: %v", err)
		return false
	}

	log.Printf("[Redis] Requeue %s (retry %d/%d) — %s", code, retries, maxRetries, reason)
	return true
}

// GetErrors returns all error entries from the error queue.
func (s *RedisStore) GetErrors() []string {
	ctx := context.Background()
	items, err := s.client.LRange(ctx, s.errorQueueKey(), 0, -1).Result()
	if err != nil {
		return nil
	}
	return items
}

// --- Continue / State ---

// HasQueue returns true if there are pending items in the queue (or processing).
func (s *RedisStore) HasQueue() bool {
	ctx := context.Background()
	// First, recover orphaned processing items
	s.recoverProcessing(ctx)

	n, err := s.client.LLen(ctx, s.queueKey()).Result()
	if err != nil {
		return false
	}
	return n > 0
}

// recoverProcessing moves orphaned items from the processing list back to the queue.
func (s *RedisStore) recoverProcessing(ctx context.Context) {
	for {
		val, err := s.client.RPopLPush(ctx, s.processingKey(), s.queueKey()).Result()
		if err != nil || val == "" {
			break
		}
		log.Printf("[Redis] Recovered orphaned item: %s (moved back to queue)", val)
	}
}

// Progress returns (done, total) counts.
func (s *RedisStore) Progress() (int, int) {
	ctx := context.Background()
	vals, err := s.client.HMGet(ctx, s.metaKey(), "processed", "total").Result()
	if err != nil || len(vals) < 2 {
		return 0, 0
	}
	done, _ := strconv.Atoi(safeString(vals[0]))
	total, _ := strconv.Atoi(safeString(vals[1]))
	return done, total
}

// LoadStockList reads the name mapping from Redis for -continue mode.
func (s *RedisStore) LoadStockList() ([]parser.StockInfo, error) {
	ctx := context.Background()
	names, err := s.client.HGetAll(ctx, s.metaKey()+"_names").Result()
	if err != nil {
		return nil, fmt.Errorf("loading stock names: %w", err)
	}

	var stocks []parser.StockInfo
	for code, name := range names {
		stocks = append(stocks, parser.StockInfo{Code: code, Name: name})
	}
	return stocks, nil
}

// GetRemainingQueue returns the stock infos still in the queue (for display).
func (s *RedisStore) GetRemainingQueue() int {
	n, _ := s.client.LLen(context.Background(), s.queueKey()).Result()
	return int(n)
}

// --- internal ---

func safeString(v interface{}) string {
	if v == nil {
		return "0"
	}
	if b, ok := v.(string); ok {
		return b
	}
	return fmt.Sprintf("%v", v)
}

// Ensure json is used
var _ = json.Marshal
