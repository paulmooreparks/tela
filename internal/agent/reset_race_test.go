package agent

import (
	"sync"
	"testing"
)

// TestResetForTestingConcurrentAccess drives the agent's package-level
// globals from many reader goroutines while ResetForTesting runs on the
// main goroutine. It gives the teststack teardown race fixed in card
// tela-79 a deterministic trigger under the -race detector: before the
// fix the bare stopCh, verbose, and reregisterNeeded globals raced with
// the reset; after it every access goes through a synchronized accessor
// or an atomic. The test asserts nothing beyond the absence of a panic
// or a -race failure, so it also passes cleanly without -race.
func TestResetForTestingConcurrentAccess(t *testing.T) {
	const readers = 8
	const iterations = 500

	// Arm a stop channel so the readers observe a live value to start.
	newStopCh()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// Touch every global the reset mutates.
				select {
				case <-stopChan():
				default:
				}
				_ = verbose.Load()
				_ = reregisterNeeded.Load()
			}
		}()
	}

	for i := 0; i < iterations; i++ {
		ResetForTesting()
		newStopCh()
		ensureStopCh()
		verbose.Store(i%2 == 0)
		reregisterNeeded.Store(i%2 == 1)
	}

	close(stop)
	wg.Wait()

	// Leave the package globals in the clean post-reset state.
	ResetForTesting()
}
