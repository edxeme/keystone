// Package ratelimit implements a per-key token-bucket limiter.
package ratelimit

import (
	"sync"
	"time"
)

// now is a variable so tests can advance the clock.
var now = time.Now

// Limiter allows bursts up to the bucket capacity and refills at
// requests-per-minute. A zero Limiter allows everything.
type Limiter struct {
	rpm int // capacity + refill rate basis

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New builds a limiter allowing rpm requests per minute per key.
// rpm <= 0 disables limiting (Allow always returns true).
func New(rpm int) *Limiter {
	return &Limiter{rpm: rpm, buckets: make(map[string]*bucket)}
}

// Allow reports whether one request from key is admitted.
func (l *Limiter) Allow(key string) bool {
	if l == nil || l.rpm <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(l.rpm), last: now()}
		l.buckets[key] = b
	}

	elapsed := now().Sub(b.last)
	b.last = now()
	b.tokens += elapsed.Seconds() * float64(l.rpm) / 60
	if b.tokens > float64(l.rpm) {
		b.tokens = float64(l.rpm)
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
