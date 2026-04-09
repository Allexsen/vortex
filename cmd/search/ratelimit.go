package main

import (
	"log/slog"
	"net"
	"net/http"
	"sync"

	"golang.org/x/time/rate"
)

type IPRateLimiter struct {
	limiters sync.Map
	rate     rate.Limit
	burst    int
}

func NewIPRateLimiter(r float64, b int) *IPRateLimiter {
	return &IPRateLimiter{
		rate:  rate.Limit(r),
		burst: b,
	}
}

func (rl *IPRateLimiter) GetLimiter(ip string) *rate.Limiter {
	limiter := rate.NewLimiter(rl.rate, rl.burst)
	if v, loaded := rl.limiters.LoadOrStore(ip, limiter); loaded {
		return v.(*rate.Limiter)
	}

	return limiter
}

func (rl *IPRateLimiter) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}

		if !rl.GetLimiter(ip).Allow() {
			SearchRequestsTotal.WithLabelValues("rate_limited").Inc()
			slog.Info("rate limit exceeded for IP", "request_id", getRequestID(r.Context()), "ip", ip)
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next(w, r)
	}
}
