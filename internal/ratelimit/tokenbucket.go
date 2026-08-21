package ratelimit

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Result contains the outcome of a rate limit check.
type Result struct {
	Allowed    bool          // Whether the request is allowed
	Remaining  int64         // Tokens remaining in the bucket
	ResetAfter time.Duration // Time until the bucket fully refills
	RetryAfter time.Duration // Time until enough tokens are available (0 if allowed)
}

// TokenBucket implements a Redis-backed token bucket rate limiter.
type TokenBucket struct {
	client       *redis.Client
	script       *redis.Script
	tokensPerMin int64
	burstSize    int64
	keyPrefix    string
}

const tokenBucketScript = `
-- Token Bucket Rate Limiter
-- KEYS[1] = bucket key
-- ARGV[1] = max tokens (capacity)
-- ARGV[2] = refill rate (tokens per second)
-- ARGV[3] = now (unix timestamp in milliseconds)
-- ARGV[4] = requested tokens

local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local refill_rate = tonumber(ARGV[2])  -- tokens per second
local now = tonumber(ARGV[3])          -- current time in ms
local requested = tonumber(ARGV[4])    -- tokens to consume

-- Get current state or initialize
local tokens = tonumber(redis.call('HGET', key, 'tokens'))
local last_refill = tonumber(redis.call('HGET', key, 'last_refill'))

if tokens == nil then
    -- First request: initialize bucket to full
    tokens = capacity
    last_refill = now
end

-- Calculate tokens to add based on elapsed time
local elapsed_ms = now - last_refill
local refill = math.floor(elapsed_ms * refill_rate / 1000)
if refill > 0 then
    tokens = math.min(capacity, tokens + refill)
    last_refill = now
end

-- Check if we have enough tokens
local allowed = 0
local remaining = tokens
local retry_after_ms = 0

if tokens >= requested then
    allowed = 1
    remaining = tokens - requested
    -- Update state
    redis.call('HSET', key, 'tokens', remaining, 'last_refill', last_refill)
else
    -- Not enough tokens. Calculate retry-after.
    local deficit = requested - tokens
    retry_after_ms = math.ceil(deficit / refill_rate * 1000)
    -- Update last_refill even on rejection (so refill accounting stays correct)
    redis.call('HSET', key, 'tokens', tokens, 'last_refill', last_refill)
end

-- Set TTL so stale buckets get cleaned up (2x the refill period)
local ttl_seconds = math.ceil(capacity / refill_rate * 2)
redis.call('EXPIRE', key, ttl_seconds)

-- Return: allowed, remaining, retry_after_ms
return {allowed, remaining, retry_after_ms}
`

// NewTokenBucket creates a new Redis-backed token bucket.
func NewTokenBucket(client *redis.Client, tokensPerMin, burstSize int64) *TokenBucket {
	return &TokenBucket{
		client:       client,
		script:       redis.NewScript(tokenBucketScript),
		tokensPerMin: tokensPerMin,
		burstSize:    burstSize,
		keyPrefix:    "ratelimit:",
	}
}

// TokensPerMin returns the configured tokens per minute.
func (tb *TokenBucket) TokensPerMin() int64 {
	return tb.tokensPerMin
}

// Allow checks if a request consuming `tokens` should be allowed.
func (tb *TokenBucket) Allow(ctx context.Context, key string, tokens int64) (*Result, error) {
	return tb.AllowN(ctx, key, tokens)
}

// AllowN is an alias for Allow with n tokens.
func (tb *TokenBucket) AllowN(ctx context.Context, key string, n int64) (*Result, error) {
	refillRate := float64(tb.tokensPerMin) / 60.0
	now := time.Now().UnixMilli()

	fullKey := tb.keyPrefix + key

	res, err := tb.script.Run(ctx, tb.client, []string{fullKey}, tb.burstSize, refillRate, now, n).Result()
	if err != nil {
		return nil, err
	}

	vals := res.([]interface{})
	allowedInt := vals[0].(int64)
	remaining := vals[1].(int64)
	retryAfterMs := vals[2].(int64)

	allowed := allowedInt == 1

	// Calculate reset after (time until full capacity)
	resetAfterSecs := float64(tb.burstSize-remaining) / refillRate
	resetAfter := time.Duration(resetAfterSecs * float64(time.Second))
	retryAfter := time.Duration(retryAfterMs) * time.Millisecond

	return &Result{
		Allowed:    allowed,
		Remaining:  remaining,
		ResetAfter: resetAfter,
		RetryAfter: retryAfter,
	}, nil
}
