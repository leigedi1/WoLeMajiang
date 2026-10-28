// Package ratelimit 令牌桶限流: 用于单条 WS 连接的消息频率限制。
package ratelimit

import (
	"math"
	"sync"
	"time"
)

// Bucket 令牌桶。burst 为桶容量, rate 为每秒补充令牌数。
type Bucket struct {
	mu     sync.Mutex
	tokens float64
	burst  float64
	rate   float64
	last   time.Time
}

// NewBucket 创建满桶。
func NewBucket(burst int, rate float64) *Bucket {
	return &Bucket{
		tokens: float64(burst),
		burst:  float64(burst),
		rate:   rate,
	}
}

// Allow 在时刻 now 尝试消耗一枚令牌。
func (b *Bucket) Allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.last.IsZero() {
		elapsed := now.Sub(b.last).Seconds()
		if elapsed > 0 {
			b.tokens = math.Min(b.burst, b.tokens+elapsed*b.rate)
		}
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}
