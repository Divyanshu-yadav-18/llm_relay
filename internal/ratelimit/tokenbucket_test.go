package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setupTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	return mr, client
}

func TestTokenBucket_Allow(t *testing.T) {
	mr, client := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	// 60 tokens per min = 1 token per second
	tb := NewTokenBucket(client, 60, 5)

	ctx := context.Background()
	key := "test_ip"

	// 1. First request is allowed (bucket starts full)
	res, err := tb.Allow(ctx, key, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Error("expected first request to be allowed")
	}
	if res.Remaining != 4 { // burst of 5 - 1
		t.Errorf("expected 4 remaining, got %d", res.Remaining)
	}
	if res.RetryAfter != 0 {
		t.Errorf("expected 0 RetryAfter, got %v", res.RetryAfter)
	}

	// 2. Consume more tokens
	res, err = tb.Allow(ctx, key, 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Error("expected second request to be allowed")
	}
	if res.Remaining != 0 {
		t.Errorf("expected 0 remaining, got %d", res.Remaining)
	}

	// 3. Request denied when bucket is empty
	res, err = tb.Allow(ctx, key, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Allowed {
		t.Error("expected third request to be denied")
	}
	if res.Remaining != 0 {
		t.Errorf("expected 0 remaining, got %d", res.Remaining)
	}
	if res.RetryAfter <= 0 {
		t.Errorf("expected RetryAfter > 0, got %v", res.RetryAfter)
	}

	// 4. Different keys are independent
	key2 := "test_ip_2"
	res, err = tb.Allow(ctx, key2, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Error("expected request with different key to be allowed")
	}
}

func TestTokenBucket_Concurrent(t *testing.T) {
	mr, client := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	tb := NewTokenBucket(client, 60000, 100) // high rate, high burst
	ctx := context.Background()
	key := "concurrent_test"

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowedCount := 0

	// Fire 200 concurrent requests, bucket size is 100
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := tb.Allow(ctx, key, 1)
			if err == nil && res.Allowed {
				mu.Lock()
				allowedCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Given burst size is 100, we should expect at least 100 allowed (possibly slightly more due to refill)
	if allowedCount < 100 {
		t.Errorf("expected at least 100 allowed requests, got %d", allowedCount)
	}
	if allowedCount > 110 {
		t.Errorf("expected close to 100 allowed requests, got %d", allowedCount)
	}
}

func TestTokenBucket_Refill(t *testing.T) {
	mr, client := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	// 120 tokens per min = 2 tokens per sec
	tb := NewTokenBucket(client, 120, 2)
	ctx := context.Background()
	key := "refill_test"

	// Consume all
	tb.Allow(ctx, key, 2)

	// Denied
	res, _ := tb.Allow(ctx, key, 1)
	if res.Allowed {
		t.Error("expected request to be denied before refill")
	}

	// Sleep 600ms, should refill at least 1 token
	time.Sleep(600 * time.Millisecond)

	// Now should be allowed
	res, _ = tb.Allow(ctx, key, 1)
	if !res.Allowed {
		t.Error("expected request to be allowed after refill")
	}
}
