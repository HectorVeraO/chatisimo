// Package draw computes a secret-gift assignment: a permutation of n participants in which nobody
// gives to themselves and no forbidden directed pair occurs. It is pure: no I/O, no clock, and
// no knowledge of people, only of indexes 0..n-1.
//
// Feasibility is decided exactly by a maximum bipartite matching, so a valid assignment exists
// if and only if the matching is perfect. Assign then samples uniformly by rejection: shuffle the
// identity permutation and accept the first one that avoids every forbidden pair. When the
// exclusions are so dense that rejection exhausts its attempts, Assign falls back to a
// randomized matching. The fallback is valid but not exactly uniform over valid assignments.
//
// A result that is not a valid assignment is a bug in this package and panics with a message
// that names the broken invariant and no index.
package draw

import (
	"errors"
	"fmt"
)

// maxAttempts bounds the rejection sampling before the matching fallback runs.
const maxAttempts = 1000

// Random is the randomness Assign needs; *math/rand/v2.Rand satisfies it.
type Random interface {
	IntN(n int) int
	Shuffle(n int, swap func(i, j int))
}

// Pair is a directed pair of participant indexes.
type Pair struct {
	Giver     int
	Recipient int
}

// ErrInvalidInput reports a call with n below 1, a pair outside 0..n-1, or no Random.
var ErrInvalidInput = errors.New("draw: invalid input")

// InfeasibleError reports that no valid assignment exists. Stranded is the number of givers a
// maximum matching leaves without a recipient.
type InfeasibleError struct {
	Stranded int
}

// Error carries the count only.
func (e *InfeasibleError) Error() string {
	text := fmt.Sprintf("draw: no valid assignment, %d givers stranded", e.Stranded)
	return text
}

// Assign returns perm where participant i gives to participant perm[i]. Self pairs are always
// forbidden, besides the pairs in forbidden. It returns *InfeasibleError when no valid
// assignment exists and ErrInvalidInput for invalid arguments.
func Assign(n int, forbidden []Pair, r Random) ([]int, error) {
	allowed, inputErr := allowedMatrix(n, forbidden, r)
	if inputErr != nil {
		return nil, inputErr
	}

	_, size := maximumMatching(allowed, identityOrder(n), nil)
	if size < n {
		return nil, &InfeasibleError{Stranded: n - size}
	}

	perm, found := sample(allowed, r)
	if !found {
		perm = randomizedMatching(allowed, r)
	}

	assertValid(perm, allowed)

	return perm, nil
}

// allowedMatrix validates the arguments and returns allowed[giver][recipient].
func allowedMatrix(n int, forbidden []Pair, r Random) ([][]bool, error) {
	if n < 1 || r == nil {
		return nil, ErrInvalidInput
	}

	allowed := make([][]bool, n)
	for giver := range allowed {
		allowed[giver] = make([]bool, n)
		for recipient := range allowed[giver] {
			allowed[giver][recipient] = giver != recipient
		}
	}

	for _, pair := range forbidden {
		isInRange := pair.Giver >= 0 && pair.Giver < n && pair.Recipient >= 0 && pair.Recipient < n
		if !isInRange {
			return nil, ErrInvalidInput
		}
		allowed[pair.Giver][pair.Recipient] = false
	}

	return allowed, nil
}

func identityOrder(n int) []int {
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	return order
}

// sample shuffles the identity permutation up to maxAttempts times and returns the first one
// that uses only allowed pairs.
func sample(allowed [][]bool, r Random) ([]int, bool) {
	n := len(allowed)
	perm := identityOrder(n)

	for range maxAttempts {
		r.Shuffle(n, func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })

		if isAllowed(perm, allowed) {
			return perm, true
		}
	}

	return nil, false
}

func isAllowed(perm []int, allowed [][]bool) bool {
	for giver, recipient := range perm {
		if !allowed[giver][recipient] {
			return false
		}
	}
	return true
}

// randomizedMatching runs the matching with the giver order and each giver's candidates in a
// random order. The caller has established that a perfect matching exists.
func randomizedMatching(allowed [][]bool, r Random) []int {
	n := len(allowed)
	order := identityOrder(n)
	r.Shuffle(n, func(i, j int) { order[i], order[j] = order[j], order[i] })

	candidates := make([][]int, n)
	for giver := range candidates {
		candidates[giver] = identityOrder(n)
		list := candidates[giver]
		r.Shuffle(n, func(i, j int) { list[i], list[j] = list[j], list[i] })
	}

	perm, size := maximumMatching(allowed, order, candidates)
	if size != n {
		panic("draw: matching is not perfect although feasibility was established")
	}
	return perm
}

// maximumMatching runs Kuhn's augmenting paths over givers in the given order. candidates, when
// non-nil, lists each giver's recipients in the order to try; otherwise they are tried in index
// order. It returns perm (giver to recipient, -1 when unmatched) and the matching size.
func maximumMatching(allowed [][]bool, order []int, candidates [][]int) ([]int, int) {
	n := len(allowed)
	giverOf := make([]int, n)
	for recipient := range giverOf {
		giverOf[recipient] = -1
	}

	matcher := matcher{allowed: allowed, candidates: candidates, giverOf: giverOf}
	size := 0
	for _, giver := range order {
		matcher.visited = make([]bool, n)
		if matcher.augment(giver) {
			size++
		}
	}

	perm := make([]int, n)
	for giver := range perm {
		perm[giver] = -1
	}
	for recipient, giver := range giverOf {
		if giver >= 0 {
			perm[giver] = recipient
		}
	}

	return perm, size
}

type matcher struct {
	allowed    [][]bool
	candidates [][]int
	giverOf    []int
	visited    []bool
}

// augment looks for an augmenting path from giver. The recursion is bounded by the number of
// recipients, because each recipient is visited at most once per search.
func (m *matcher) augment(giver int) bool {
	n := len(m.allowed)
	for step := range n {
		recipient := step
		if m.candidates != nil {
			recipient = m.candidates[giver][step]
		}

		isFree := m.allowed[giver][recipient] && !m.visited[recipient]
		if !isFree {
			continue
		}
		m.visited[recipient] = true

		holder := m.giverOf[recipient]
		if holder < 0 || m.augment(holder) {
			m.giverOf[recipient] = giver
			return true
		}
	}
	return false
}

// assertValid panics unless perm is a bijection that uses only allowed pairs.
func assertValid(perm []int, allowed [][]bool) {
	n := len(allowed)
	if len(perm) != n {
		panic("draw: result does not cover every participant")
	}

	taken := make([]bool, n)
	for giver, recipient := range perm {
		isInRange := recipient >= 0 && recipient < n
		if !isInRange || taken[recipient] {
			panic("draw: result is not a bijection")
		}
		taken[recipient] = true

		if !allowed[giver][recipient] {
			panic("draw: result uses a forbidden pair")
		}
	}
}
