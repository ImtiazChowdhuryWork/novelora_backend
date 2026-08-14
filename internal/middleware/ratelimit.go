package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type visitorLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// IPRateLimiter tracks a token-bucket limiter per client IP. Safe for
// concurrent use. A background sweep evicts visitors that haven't been
// seen recently so a long-running process doesn't accumulate unbounded
// memory from distinct IPs over time.
type IPRateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitorLimiter
	rps      rate.Limit
	burst    int
}

// NewIPRateLimiter creates a limiter allowing requestsPerMinute requests
// per IP on average, with burst as the short-term allowance above that
// average.
func NewIPRateLimiter(requestsPerMinute, burst int) *IPRateLimiter {
	limiter := &IPRateLimiter{
		visitors: make(map[string]*visitorLimiter),
		rps:      rate.Limit(float64(requestsPerMinute) / 60),
		burst:    burst,
	}
	go limiter.sweepStaleVisitors()
	return limiter
}

func (limiter *IPRateLimiter) sweepStaleVisitors() {
	for {
		time.Sleep(time.Minute)
		limiter.mu.Lock()
		for ip, visitor := range limiter.visitors {
			if time.Since(visitor.lastSeen) > 3*time.Minute {
				delete(limiter.visitors, ip)
			}
		}
		limiter.mu.Unlock()
	}
}

func (limiter *IPRateLimiter) allow(ip string) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	visitor, exists := limiter.visitors[ip]
	if !exists {
		visitor = &visitorLimiter{limiter: rate.NewLimiter(limiter.rps, limiter.burst)}
		limiter.visitors[ip] = visitor
	}
	visitor.lastSeen = time.Now()
	return visitor.limiter.Allow()
}

// Middleware rejects requests over the per-IP limit with 429.
func (limiter *IPRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if !limiter.allow(clientIP(request)) {
			writeAuthError(responseWriter, http.StatusTooManyRequests, "too many requests, please slow down")
			return
		}
		next.ServeHTTP(responseWriter, request)
	})
}

// clientIP prefers X-Forwarded-For's left-most address (the original
// client) since production sits behind a reverse proxy (Nginx/Caddy on
// the target VPS), falling back to the raw connection address for
// direct/local connections.
func clientIP(request *http.Request) string {
	if forwarded := request.Header.Get("X-Forwarded-For"); forwarded != "" {
		if first, _, _ := strings.Cut(forwarded, ","); strings.TrimSpace(first) != "" {
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return request.RemoteAddr
	}
	return host
}
