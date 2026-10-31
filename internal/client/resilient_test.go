package client

import (
	"context"
	"github.com/adilbek/catalog-sis2/internal/domain"
	"sync/atomic"
	"testing"
	"time"
)

// Parent deadline caps every attempt together; deterministic large backoff cannot escape it.
func TestWriteTotalBoundsRetryAndBackoff(t *testing.T) {
	r := NewResilient(nil)
	r.MaxAttempts = 100
	r.WriteAttemptTimeout = time.Second
	r.WriteTotal = 30 * time.Millisecond
	r.BaseDelay = time.Second
	r.MaxDelay = time.Second
	var calls atomic.Int32
	start := time.Now()
	_, err := write(context.Background(), r, true, func(ctx context.Context) (int, error) { calls.Add(1); <-ctx.Done(); return 0, ctx.Err() })
	if domain.CodeOf(err) != domain.CodeDeadline || calls.Load() != 1 || time.Since(start) > 300*time.Millisecond {
		t.Fatalf("total deadline: calls=%d err=%v elapsed=%s", calls.Load(), err, time.Since(start))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls.Store(0)
	_, err = write(ctx, r, true, func(context.Context) (int, error) { calls.Add(1); return 0, nil })
	if err == nil || calls.Load() != 0 {
		t.Fatal("cancelled call started work")
	}
}
