package metadata

import (
	"context"
	"sync"
	"time"
)

// TokenBucket limits background AniList requests: capacity tokens, refilled evenly
// over per. AniList's anonymous rate limit is per IP and shared by all profiles.
type TokenBucket struct {
	mu       sync.Mutex
	capacity float64
	tokens   float64
	rate     float64 // tokens per second
	last     time.Time
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
}

func NewTokenBucket(capacity int, per time.Duration, now func() time.Time) *TokenBucket {
	if now == nil {
		now = time.Now
	}
	return &TokenBucket{
		capacity: float64(capacity),
		tokens:   float64(capacity),
		rate:     float64(capacity) / per.Seconds(),
		last:     now(),
		now:      now,
		sleep: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		},
	}
}

func (b *TokenBucket) refill() {
	now := b.now()
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens = min(b.capacity, b.tokens+elapsed*b.rate)
		b.last = now
	}
}

// TryTake takes a token if one is available.
func (b *TokenBucket) TryTake() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill()
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// Wait blocks until a token is available or ctx is done.
func (b *TokenBucket) Wait(ctx context.Context) error {
	for {
		b.mu.Lock()
		b.refill()
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return nil
		}
		wait := time.Duration((1 - b.tokens) / b.rate * float64(time.Second))
		b.mu.Unlock()
		if err := b.sleep(ctx, wait); err != nil {
			return err
		}
	}
}
