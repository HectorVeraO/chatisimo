package capitime_test

import (
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
)

// TestHandleMessageKeepsTheTextsOfMessagesHandledAtOnce handles two messages of the target member
// at the same time, the first one held inside the clock with the earlier instant already taken.
// Both texts must reach the next read: neither message may make the other look like it comes from
// the future and drop it.
func TestHandleMessageKeepsTheTextsOfMessagesHandledAtOnce(t *testing.T) {
	base := time.Date(2026, 11, 1, 21, 0, 0, 0, capitime.Santiago())

	var (
		ticks    atomic.Int64
		holdNext atomic.Bool
	)
	held := make(chan struct{})
	release := make(chan struct{})

	// Every reading is one second later than the one before. The reading taken while holdNext is
	// set is handed back only after release closes.
	now := func() time.Time {
		at := base.Add(time.Duration(ticks.Add(1)) * time.Second)
		if holdNext.CompareAndSwap(true, false) {
			close(held)
			<-release
		}

		return at
	}

	h := newReadHarness(t, readHarnessOptions{answer: noArrivalJSON, now: now})

	handleAsync := func(text string) <-chan error {
		done := make(chan error, 1)
		go func() {
			_, err := h.svc.HandleMessage(t.Context(), targetMessage(text))
			done <- err
		}()

		return done
	}

	holdNext.Store(true)
	firstDone := handleAsync("primero")
	<-held
	secondDone := handleAsync("segundo")

	// The second message either finishes while the first is held or waits for it; both orders
	// must keep both texts. The wait gives it the time to finish when nothing stops it.
	var secondErr error
	hasSecondFinished := false
	select {
	case secondErr = <-secondDone:
		hasSecondFinished = true
	case <-time.After(300 * time.Millisecond):
	}
	close(release)

	if err := <-firstDone; err != nil {
		t.Fatalf("HandleMessage(primero): %v", err)
	}
	if !hasSecondFinished {
		secondErr = <-secondDone
	}
	if secondErr != nil {
		t.Fatalf("HandleMessage(segundo): %v", secondErr)
	}

	h.handle(t, targetMessage("tercero"))

	got := earlierLines(h.lastCall(t))
	slices.Sort(got)
	if want := []string{"primero", "segundo"}; !slices.Equal(got, want) {
		t.Errorf("earlier messages = %q, want %q", got, want)
	}
}
