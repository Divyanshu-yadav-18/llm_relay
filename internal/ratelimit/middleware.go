package ratelimit

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers"
)

// Middleware returns a chi-compatible middleware that enforces rate limiting.
//
// For now, rate limiting is done per-IP (from X-Forwarded-For or RemoteAddr).
// In Phase 4 when we add API key auth, this will switch to per-API-key.
//
// When rate limited, it returns HTTP 429 with an OpenAI-compatible error
// body and standard rate limit headers:
//   - X-RateLimit-Limit: max tokens per minute
//   - X-RateLimit-Remaining: tokens remaining
//   - X-RateLimit-Reset: seconds until full refill
//   - Retry-After: seconds until request can be retried (on 429)
func Middleware(limiter *TokenBucket, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract rate limit key from request
			// For now: use client IP. Phase 4 will add API key extraction.
			key := extractKey(r)

			// Each request costs 1 token for the rate check.
			result, err := limiter.Allow(r.Context(), key, 1)
			if err != nil {
				// Redis down — fail open (allow the request)
				logger.Error("rate limiter error, failing open", "error", err)
				next.ServeHTTP(w, r)
				return
			}

			// Set rate limit headers on every response
			w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(limiter.TokensPerMin(), 10))
			w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(int64(result.ResetAfter.Seconds()), 10))

			if !result.Allowed {
				w.Header().Set("Retry-After", strconv.FormatInt(int64(result.RetryAfter.Seconds())+1, 10))
				// Use OpenAI-compatible error format
				providers.WriteErrorResponse(w, http.StatusTooManyRequests,
					"Rate limit exceeded. Please retry after "+result.RetryAfter.String(),
					"rate_limit_error", "rate_limit_exceeded")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// extractKey gets the rate limit key from the request.
// Uses X-Forwarded-For if behind a reverse proxy, otherwise RemoteAddr.
func extractKey(r *http.Request) string {
	// Check X-Forwarded-For first (set by reverse proxies)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			ip := strings.TrimSpace(ips[0])
			if ip != "" {
				return "ip:" + ip
			}
		}
	}

	// Fall back to RemoteAddr (strip port)
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	return "ip:" + ip
}
