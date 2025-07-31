// Package firsterr provides an enhanced errgroup implementation with:
// - First-error-out semantics
// - Context cancellation propagation
// - Buffered error channel to prevent deadlocks
// - Explicit cancellation control
//
// Compared to standard library's errgroup:
// - Uses buffered channel (size 1) for first error
// - Provides Cancel() method for explicit termination
// - Documents panic behavior (not recovered)
// - Optimized for high-contention scenarios
package firsterr

import (
	"context"
	"sync"
)

// Group coordinates a set of goroutines working on related tasks.
// Zero value is not usable - use WithContext to create instances.
//
// Key behaviors:
// - First error cancels the group context
// - Wait() returns first error or nil if all succeed
// - Panics propagate to caller (not recovered)
// - Safe for concurrent use by multiple goroutines
type Group struct {
	ctx    context.Context //nolint:containedctx
	cancel context.CancelFunc

	wg      sync.WaitGroup
	errOnce sync.Once

	errCh chan error // signals first error
}

// WithContext creates a new Group and derived context.
// The returned context should be used by all goroutines in the group.
// Canceling this context terminates all group operations.
func WithContext(parent context.Context) (context.Context, *Group) {
	ctx, cancel := context.WithCancel(parent)
	g := &Group{
		ctx:    ctx,
		cancel: cancel,
		errCh:  make(chan error, 1), // buffer of 1 to avoid blocking on first error
	}

	return ctx, g
}

// Go executes the function in a new goroutine as part of the group.
// The function should:
// - Monitor ctx.Done() for cancellation
// - Return promptly on context cancellation
// - Return nil on success or error on failure
//
// The first non-nil error cancels the group context.
func (g *Group) Go(f func(context.Context) error) {
	g.wg.Add(1)

	go (func() {
		defer g.wg.Done()
		if err := f(g.ctx); err != nil {
			g.errOnce.Do(func() {
				// publishes first error and cancels context
				select {
				case g.errCh <- err:
				default:
				}
				g.cancel()
			})
		}
	})()
}

// Wait blocks until:
// - The first goroutine returns an error, or
// - All goroutines complete successfully
//
// After first error, remaining goroutines should respect context cancellation
// but are not explicitly waited for.
//
// Returns:
// - First error encountered, or
// - nil if all goroutines completed successfully
func (g *Group) Wait() error {
	// closes when ALL goroutines finish
	doneAll := make(chan struct{})

	go (func() {
		g.wg.Wait()
		close(doneAll)
	})()

	select {
	case err := <-g.errCh:
		return err // first error
	case <-doneAll:
		return nil // all completed without error
	}
}

// Cancel terminates all operations in the group by canceling the context.
// Safe to call:
// - Concurrently with other methods
// - Multiple times
// - Even if Wait() has already returned
func (g *Group) Cancel() {
	g.cancel()
}
