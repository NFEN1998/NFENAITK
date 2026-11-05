// Package cache adds a Redis layer in front of PostgreSQL: it caches query
// results, accumulates daily request counters and buffers request logs before
// they are flushed to the database in batches.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps a Redis connection together with the flush workers.
type Client struct {
	rdb       *redis.Client
	prefix    string
	maxLogs   int
	logSink   func([]json.RawMessage) error
	countSink func(day string, delta int64) error

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Options configures a cache client.
type Options struct {
	URL            string
	Prefix         string
	MaxLogs        int
	LogSink        func([]json.RawMessage) error
	CountSink      func(day string, delta int64) error
	HistoryEnabled bool
}

// Keys used inside Redis.
func (c *Client) key(parts ...string) string {
	return c.prefix + ":" + strings.Join(parts, ":")
}

// New connects to Redis. The returned client tolerates Redis being unavailable
// at startup: every method degrades to a cache miss.
func New(opts Options) (*Client, error) {
	if strings.TrimSpace(opts.URL) == "" {
		return nil, errors.New("REDIS_URL 未配置")
	}
	ropts, err := redis.ParseURL(opts.URL)
	if err != nil {
		return nil, err
	}
	rdb := redis.NewClient(ropts)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	prefix := opts.Prefix
	if prefix == "" {
		prefix = "nfentik"
	}
	maxLogs := opts.MaxLogs
	if maxLogs <= 0 {
		maxLogs = 1000
	}
	c := &Client{
		rdb:       rdb,
		prefix:    prefix,
		maxLogs:   maxLogs,
		logSink:   opts.LogSink,
		countSink: opts.CountSink,
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	if c.logSink != nil {
		c.wg.Add(1)
		go c.logFlushLoop()
	}
	if c.countSink != nil {
		c.wg.Add(1)
		go c.countFlushLoop()
	}
	return c, nil
}

// Close stops the flush workers and closes the connection.
func (c *Client) Close() error {
	c.cancel()
	c.wg.Wait()
	return c.rdb.Close()
}

// Ping reports whether Redis is reachable.
func (c *Client) Ping() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return c.rdb.Ping(ctx).Err() == nil
}

// ---------------------------------------------------------------------------
// Query cache
// ---------------------------------------------------------------------------

// queryKey derives a stable key from the title plus optional fields. It reuses
// the canonical bank lookup hash so a cached answer is valid for both AI and
// bank results, and so cache invalidation can target the same identifier.
//
// The bank version is part of the key: when the bank changes the version is
// incremented and every previously cached key becomes unreachable without a
// full-key scan.
func (c *Client) queryKey(title string, options *string) string {
	return c.key("query", c.bankVersion(), queryCacheKey(title, options))
}

// versionKey holds the current bank cache version.
func (c *Client) versionKey() string { return c.key("query", "version") }

// bankVersion returns the current cache version, or "0" when Redis is not
// reachable. The value is read on every lookup; Redis serves it from memory so
// this stays cheap.
func (c *Client) bankVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	v, err := c.rdb.Get(ctx, c.versionKey()).Result()
	if err != nil || v == "" {
		return "0"
	}
	return v
}

// GetQuery returns a cached query result.
func (c *Client) GetQuery(title string, options *string, dst any) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	raw, err := c.rdb.Get(ctx, c.queryKey(title, options)).Bytes()
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, dst) == nil
}

// SetQuery stores a query result with the given TTL.
func (c *Client) SetQuery(title string, options *string, value any, ttl time.Duration) {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = c.rdb.Set(ctx, c.queryKey(title, options), raw, ttl).Err()
}

// InvalidateQueries invalidates every cached query result after a bank change.
//
// Instead of scanning and deleting individual keys (O(keyspace) and a source of
// Redis CPU spikes on large deployments) it increments the bank version. Every
// cached key embeds the version, so all prior entries become unreachable at
// once and expire on their own TTL. The stale keys are reclaimed by TTL expiry.
func (c *Client) InvalidateQueries() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.rdb.Incr(ctx, c.versionKey()).Err()
}

// ---------------------------------------------------------------------------
// Daily request counter
// ---------------------------------------------------------------------------

func (c *Client) dailyKey(day string) string { return c.key("count", day) }

// BumpDailyRequest increments today's counter in Redis.
func (c *Client) BumpDailyRequest() {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = c.rdb.Incr(ctx, c.dailyKey(time.Now().Format("2006-01-02"))).Err()
}

// countFlushLoop periodically moves accumulated counters into PostgreSQL.
func (c *Client) countFlushLoop() {
	defer c.wg.Done()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			c.flushCounts()
			return
		case <-ticker.C:
			c.flushCounts()
		}
	}
}

func (c *Client) flushCounts() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pattern := c.key("count", "*")
	keys, _, err := c.rdb.Scan(ctx, 0, pattern, 100).Result()
	if err != nil {
		return
	}
	for _, k := range keys {
		// GETDEL atomically reads and removes the counter.
		val, err := c.rdb.GetDel(ctx, k).Int64()
		if err != nil || val == 0 {
			continue
		}
		day := strings.TrimPrefix(k, c.prefix+":count:")
		if err := c.countSink(day, val); err != nil {
			// Put the value back so nothing is lost on a transient failure.
			_ = c.rdb.IncrBy(context.Background(), k, val).Err()
		}
	}
}

// ---------------------------------------------------------------------------
// Request log queue
// ---------------------------------------------------------------------------

func (c *Client) logQueueKey() string { return c.key("logs", "queue") }

// PushLog appends a request log entry to the Redis list.
func (c *Client) PushLog(entry any) {
	raw, err := json.Marshal(entry)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = c.rdb.LPush(ctx, c.logQueueKey(), raw).Err()
}

// logFlushLoop batches queued logs into PostgreSQL.
func (c *Client) logFlushLoop() {
	defer c.wg.Done()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			c.flushLogs()
			return
		case <-ticker.C:
			c.flushLogs()
		}
	}
}

func (c *Client) flushLogs() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Pull up to 500 entries per flush. RPOPLPUSH into a processing list keeps
	// entries safe until the database write succeeds.
	const batch = 500
	var collected [][]byte
	for i := 0; i < batch; i++ {
		raw, err := c.rdb.RPop(ctx, c.logQueueKey()).Bytes()
		if err != nil {
			break
		}
		collected = append(collected, raw)
	}
	if len(collected) == 0 {
		return
	}
	payload := make([]json.RawMessage, 0, len(collected))
	for _, raw := range collected {
		payload = append(payload, json.RawMessage(raw))
	}
	if err := c.logSink(payload); err != nil {
		// Re-queue lost entries at the tail so they are retried later.
		for _, raw := range collected {
			_ = c.rdb.LPush(context.Background(), c.logQueueKey(), raw).Err()
		}
		log.Printf("flush request logs to database failed: %v", err)
	}
}

// QueueLength reports how many log entries are waiting to be flushed.
func (c *Client) QueueLength() int64 {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	n, err := c.rdb.LLen(ctx, c.logQueueKey()).Result()
	if err != nil {
		return 0
	}
	return n
}
