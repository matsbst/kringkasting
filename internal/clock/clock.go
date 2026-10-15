// Package clock is the application's source of the current time and of
// randomness, so tests can pin both (the same way tools/baseline pinned
// the Deno app when the golden files were captured).
package clock

import (
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"
)

var offset atomic.Int64 // nanoseconds added to the real clock

// Now returns the current time (shifted when pinned in tests).
func Now() time.Time {
	return time.Now().Add(time.Duration(offset.Load()))
}

// NowMs returns the current time as Unix milliseconds, the unit the
// database and the durable state use (as JavaScript's Date.now()).
func NowMs() int64 {
	return Now().UnixMilli()
}

// Pin makes the clock read `start` now and advance in real time from there.
func Pin(start time.Time) {
	offset.Store(int64(time.Until(start)))
}

// Unpin restores the real clock.
func Unpin() {
	offset.Store(0)
}

var (
	randMu sync.Mutex
	rng    = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
)

// Float64 returns a random number in [0, 1).
func Float64() float64 {
	randMu.Lock()
	defer randMu.Unlock()
	return rng.Float64()
}

// IntN returns a random number in [0, n).
func IntN(n int) int {
	randMu.Lock()
	defer randMu.Unlock()
	return rng.IntN(n)
}

// Seed makes randomness repeatable (tests only).
func Seed(seed uint64) {
	randMu.Lock()
	defer randMu.Unlock()
	rng = rand.New(rand.NewPCG(seed, seed))
}
