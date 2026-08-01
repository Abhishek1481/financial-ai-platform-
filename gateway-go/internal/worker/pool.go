// Package worker implements a generic bounded worker pool.
//
// This is the actual "concurrent document ingestion" engineering this
// platform's Go side is responsible for. Accepting many simultaneous HTTP
// uploads is just what net/http already does per request — what needs
// deliberate design is making sure a burst of uploads can't unboundedly
// fan out into overwhelming ml-service with concurrent extraction RPCs.
// Bounding both worker count and queue depth is that backpressure
// mechanism.
package worker

import (
	"context"
	"errors"
	"sync"
)

var ErrQueueFull = errors.New("worker: queue is full")

// ProcessFunc handles one item of work. Errors are the processor's own
// responsibility to record (e.g. updating a job's status in a repository)
// — the pool guarantees bounded concurrency and nothing else; retry
// policy, failure tracking, and dead-lettering all belong to whatever
// ProcessFunc closes over.
type ProcessFunc[T any] func(ctx context.Context, item T)

// Pool is a fixed number of goroutines pulling work off a fixed-size
// buffered channel.
type Pool[T any] struct {
	queue   chan T
	workers int
	process ProcessFunc[T]
	wg      sync.WaitGroup
}

func NewPool[T any](workers, queueSize int, process ProcessFunc[T]) *Pool[T] {
	return &Pool[T]{
		queue:   make(chan T, queueSize),
		workers: workers,
		process: process,
	}
}

// Start spins up the worker goroutines. They run until ctx is cancelled or
// Stop closes the queue, whichever happens first.
func (p *Pool[T]) Start(ctx context.Context) {
	for range p.workers {
		p.wg.Add(1)
		go p.runWorker(ctx)
	}
}

func (p *Pool[T]) runWorker(ctx context.Context) {
	defer p.wg.Done()
	for {
		select {
		case item, ok := <-p.queue:
			if !ok {
				return
			}
			p.process(ctx, item)
		case <-ctx.Done():
			return
		}
	}
}

// Submit enqueues item without blocking, returning ErrQueueFull if every
// worker is busy and the buffer is already at capacity. Callers (an HTTP
// handler) turn that into a 503 rather than a request that hangs waiting
// for queue space.
func (p *Pool[T]) Submit(item T) error {
	select {
	case p.queue <- item:
		return nil
	default:
		return ErrQueueFull
	}
}

// Stop closes the queue — draining whatever was already buffered, not
// discarding it — and blocks until every worker exits or ctx's deadline
// passes, mirroring http.Server.Shutdown(ctx)'s contract so main.go can
// race it against the same shutdown timeout.
func (p *Pool[T]) Stop(ctx context.Context) error {
	close(p.queue)

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
