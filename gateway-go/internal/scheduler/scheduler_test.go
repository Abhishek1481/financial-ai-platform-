package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRun_InvokesTaskOnEachTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var count atomic.Int32

	done := make(chan struct{})
	go func() {
		Run(ctx, 5*time.Millisecond, func(context.Context) { count.Add(1) })
		close(done)
	}()

	time.Sleep(35 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run() did not return after context cancellation")
	}

	if got := count.Load(); got < 2 {
		t.Errorf("task invoked %d times in 35ms at a 5ms interval, want at least 2", got)
	}
}

func TestRun_StopsInvokingAfterContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var count atomic.Int32

	done := make(chan struct{})
	go func() {
		Run(ctx, 5*time.Millisecond, func(context.Context) { count.Add(1) })
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	countAtCancel := count.Load()
	time.Sleep(30 * time.Millisecond)
	if count.Load() != countAtCancel {
		t.Errorf("task kept firing after Run() returned: %d -> %d", countAtCancel, count.Load())
	}
}
