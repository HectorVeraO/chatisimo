package giftexchange_test

import (
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

func TestCryptoSourceSatisfiesRandom(t *testing.T) {
	//nolint:gosec // the test exercises the production composition rand.New(CryptoSource{}).
	var r giftexchange.Random = rand.New(giftexchange.CryptoSource{})
	for range 1000 {
		n := r.IntN(7)
		if n < 0 || n >= 7 {
			t.Fatalf("IntN(7) = %d", n)
		}
	}
	items := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	r.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
	seen := make(map[int]bool)
	for _, v := range items {
		seen[v] = true
	}
	if len(seen) != len(items) {
		t.Fatalf("shuffle lost items: %v", items)
	}
}

func TestCryptoSourceUsesAllBits(t *testing.T) {
	var s giftexchange.CryptoSource
	var seenSet, seenClear uint64
	seenClear = ^uint64(0)
	for range 256 {
		v := s.Uint64()
		seenSet |= v
		seenClear &= v
	}
	// A constant or counter-like source leaves high bits never set or never cleared.
	if seenSet != ^uint64(0) {
		t.Fatalf("bits never set across 256 draws: %064b", ^seenSet)
	}
	if seenClear != 0 {
		t.Fatalf("bits never cleared across 256 draws: %064b", seenClear)
	}
}

func TestCryptoSourceIsSafeForConcurrentUse(t *testing.T) {
	//nolint:gosec // the test exercises the production composition rand.New(CryptoSource{}).
	r := rand.New(giftexchange.CryptoSource{})
	var wg sync.WaitGroup
	var outOfRange atomic.Int64
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				if v := r.IntN(100); v < 0 || v >= 100 {
					outOfRange.Add(1)
				}
				r.Shuffle(5, func(_, _ int) {})
			}
		}()
	}
	wg.Wait()
	if n := outOfRange.Load(); n != 0 {
		t.Fatalf("%d draws outside [0, 100)", n)
	}
}
