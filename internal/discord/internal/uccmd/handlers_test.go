package uccmd_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/uccmd"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

type stubUCService struct{}

func (stubUCService) Check(_ context.Context, _ uccalendar.Query) (uccalendar.Report, error) {
	return uccalendar.Report{}, nil
}

func TestNewAggregatesAllMissingDependencies(t *testing.T) {
	t.Parallel()

	_, err := uccmd.New(nil, nil)
	if err == nil {
		t.Fatal("New(nil, nil) succeeded, want error")
	}
	for _, want := range []string{"service", "location"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}

	handlers, err := uccmd.New(stubUCService{}, time.UTC)
	if err != nil || handlers == nil {
		t.Fatalf("New(valid) = %v, %v, want non-nil handlers", handlers, err)
	}
}
