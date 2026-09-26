package server

import (
	"crypto/sha256"
	"sync"
	"time"
)

type bucket struct {
	start time.Time
	n     int
}
type limiter struct {
	mu    sync.Mutex
	items map[[32]byte]bucket
	now   func() time.Time
}

func (l *limiter) allow(key string, max int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.items == nil {
		l.items = map[[32]byte]bucket{}
	}
	if len(l.items) >= 20000 {
		for k, v := range l.items {
			if now.Sub(v.start) >= 24*time.Hour {
				delete(l.items, k)
			}
		}
	}
	h := sha256.Sum256([]byte(key))
	b, exists := l.items[h]
	if !exists && len(l.items) >= 20000 {
		return false
	}
	if now.Sub(b.start) >= window {
		b = bucket{start: now}
	}
	if b.n >= max {
		return false
	}
	b.n++
	l.items[h] = b
	return true
}
