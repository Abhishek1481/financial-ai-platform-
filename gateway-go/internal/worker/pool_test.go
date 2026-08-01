package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPool_ProcessesSubmittedItems(t *testing.T) {
	var processed int32
	var wg sync.WaitGroup
	wg.Add(3)

	pool := NewPool(2, 10, func(ctx context.Context, item int) {
		atomic.AddInt32(&processed, 1)
		wg.Done()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	for i := range 3 {
		if err := pool.Submit(i); err != nil {
			t.Fatalf("Submit(%d) error: %v", i, err)
		}
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for all items to be processed")
	}

	if got := atomic.LoadInt32(&processed); got != 3 {
		t.Errorf("processed = %d, want 3", got)
	}
}

func TestPool_SubmitReturnsErrQueueFullWhenSaturated(t *testing.T) {
	block := make(chan struct{})
	pool := NewPool(1, 1, func(ctx context.Context, item int) {
		<-block // the single worker stays busy until the test releases it
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer close(block)

	// First item occupies the one worker.
	if err := pool.Submit(1); err != nil {
		t.Fatalf("Submit(1) error: %v", err)
	}
	// Give the worker a moment to actually pick it up, so the queue
	// buffer is genuinely free before item 2 is submitted into it.
	time.Sleep(50 * time.Millisecond)

	// Second item fills the queue buffer (size 1).
	if err := pool.Submit(2); err != nil {
		t.Fatalf("Submit(2) error: %v", err)
	}

	// Third has nowhere to go — the worker is still busy and the buffer
	// is full.
	err := pool.Submit(3)
	if !errors.Is(err, ErrQueueFull) {
		t.Errorf("Submit(3) on a saturated pool: error = %v, want %v", err, ErrQueueFull)
	}
}

func TestPool_StopDrainsBufferedItemsBeforeReturning(t *testing.T) {
	var processed int32
	pool := NewPool(1, 10, func(ctx context.Context, item int) {
		time.Sleep(10 * time.Millisecond)
		atomic.AddInt32(&processed, 1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	for i := range 5 {
		if err := pool.Submit(i); err != nil {
			t.Fatalf("Submit(%d) error: %v", i, err)
		}
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	if err := pool.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}

	if got := atomic.LoadInt32(&processed); got != 5 {
		t.Errorf("processed = %d after Stop(), want 5 (all buffered items drained)", got)
	}
}

func TestPool_StopReturnsContextErrorOnTimeout(t *testing.T) {
	block := make(chan struct{})
	defer close(block) // avoid leaking the worker goroutine past the test

	pool := NewPool(1, 1, func(ctx context.Context, item int) {
		<-block // never finishes within the test's timeout below
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	if err := pool.Submit(1); err != nil {
		t.Fatalf("Submit(1) error: %v", err)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stopCancel()
	if err := pool.Stop(stopCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Stop() error = %v, want %v", err, context.DeadlineExceeded)
	}
}
