// Package jobs wraps River: a registry modules add workers to, an insert-only Scheduler modules
// receive at construction, and the working Runner built from the registry afterwards.
package jobs

import (
	"errors"
	"fmt"

	"github.com/riverqueue/river"
)

// Schema is the Postgres schema of River's tables.
const Schema = "river"

// Registry collects workers before the Runner exists (River's bundle is fixed at NewClient and
// has no lock). Not safe for concurrent use; used only during wiring.
type Registry struct {
	workers *river.Workers
	kinds   int
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{workers: river.NewWorkers()}
}

// Register adds w for kind T.Kind(). A duplicate kind returns an error (river.AddWorkerSafely).
func Register[T river.JobArgs](r *Registry, w river.Worker[T]) error {
	var problems []error

	if r == nil {
		problems = append(problems, errors.New("nil registry"))
	}

	if w == nil {
		problems = append(problems, errors.New("nil worker"))
	}

	if len(problems) > 0 {
		return fmt.Errorf("register worker: %w", errors.Join(problems...))
	}

	err := river.AddWorkerSafely(r.workers, w)
	if err != nil {
		return fmt.Errorf("register worker: %w", err)
	}

	r.kinds++

	return nil
}

// joinProblems returns nil when there are no problems, and otherwise one error carrying all of them.
func joinProblems(problems []error) error {
	if len(problems) == 0 {
		return nil
	}

	return fmt.Errorf("%w", errors.Join(problems...))
}
