//go:build eval

package capitime_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
	"github.com/HectorVeraO/chatisimo/internal/platform/config"
	"github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/llm"
)

// This file is the local evaluation of the arrival read. It is built only with the "eval" tag,
// so "go test ./...", lint and CI never compile or run it. The model half calls the real model with
// the key stored in the local config store and costs money:
//
//	go test -tags eval -run TestEvalArrivalRead -count=1 -v ./internal/capitime/

const (
	evalSetPath       = "testdata/eval_arrival.json"
	evalMaxModelCalls = 100
	evalInstantLayout = "2006-01-02 15:04"
	evalImmediate     = "immediate"

	evalSettingLLMKey     = "llm.api_key"
	evalSettingLLMModel   = "llm.model"
	evalSettingLLMBaseURL = "llm.base_url"
)

var errEvalCallLimit = errors.New("eval: model call limit reached")

// evalRow is one labeled message of the frozen set. Now and Want are wall-clock instants in
// America/Santiago; Want is "immediate" for an arrival without a time and empty for a negative.
// Earlier holds the messages the same person sent just before Text, oldest first; the labels
// describe Text read after them.
type evalRow struct {
	ID      string   `json:"id"`
	Now     string   `json:"now"`
	Earlier []string `json:"earlier"`
	Text    string   `json:"text"`
	Arrival bool     `json:"arrival"`
	Want    string   `json:"want"`
}

// evalScore collects the outcome of the arrival read over the set.
type evalScore struct {
	name         string
	negatives    int
	positives    int
	detected     int
	falseReplies []string
	missed       []string
	wrongTime    []string
	unparseable  []string
	failed       []string
}

// record scores one row. decision is the decision the service would log for it.
func (s *evalScore) record(t *testing.T, row evalRow, now time.Time, claim capitime.ArrivalClaim, decision string) {
	t.Helper()

	switch decision {
	case "model_unparseable":
		s.unparseable = append(s.unparseable, row.ID)
	case "model_failed":
		s.failed = append(s.failed, row.ID)
	}

	if !row.Arrival {
		s.negatives++
		if claim.Detected {
			s.falseReplies = append(s.falseReplies, row.ID)
		}

		return
	}

	s.positives++
	if !claim.Detected {
		s.missed = append(s.missed, row.ID)

		return
	}

	s.detected++
	if !evalClaimMatches(t, row, now, claim) {
		s.wrongTime = append(s.wrongTime, fmt.Sprintf("%s(got %s)", row.ID, evalDescribe(claim)))
	}
}

func (s *evalScore) report(t *testing.T) {
	t.Helper()

	t.Logf("%s: false replies on negatives %d/%d %v", s.name, len(s.falseReplies), s.negatives, s.falseReplies)
	t.Logf("%s: missed positives %d/%d %v", s.name, len(s.missed), s.positives, s.missed)
	t.Logf("%s: wrong day or time among detected positives %d/%d %v", s.name, len(s.wrongTime), s.detected, s.wrongTime)
	t.Logf("%s: unparseable answers %d %v", s.name, len(s.unparseable), s.unparseable)
	t.Logf("%s: failed requests %d %v", s.name, len(s.failed), s.failed)
}

func evalDescribe(claim capitime.ArrivalClaim) string {
	if claim.Immediate {
		return evalImmediate
	}

	return claim.ClaimedAt.In(capitime.Santiago()).Format(evalInstantLayout)
}

func evalClaimMatches(t *testing.T, row evalRow, now time.Time, claim capitime.ArrivalClaim) bool {
	t.Helper()

	if row.Want == evalImmediate {
		return claim.Immediate && claim.ClaimedAt.Equal(now)
	}

	want := evalInstant(t, row.ID, row.Want)
	isSameInstant := !claim.Immediate && claim.ClaimedAt.Equal(want)

	return isSameInstant
}

func evalInstant(t *testing.T, rowID, raw string) time.Time {
	t.Helper()

	parsed, err := time.ParseInLocation(evalInstantLayout, raw, capitime.Santiago())
	if err != nil {
		t.Fatalf("row %s: %q is not a %q instant: %v", rowID, raw, evalInstantLayout, err)
	}

	return parsed
}

func loadEvalRows(t *testing.T) []evalRow {
	t.Helper()

	raw, err := os.ReadFile(evalSetPath)
	if err != nil {
		t.Fatalf("read %s: %v", evalSetPath, err)
	}

	var rows []evalRow
	err = json.Unmarshal(raw, &rows)
	if err != nil {
		t.Fatalf("parse %s: %v", evalSetPath, err)
	}

	seen := map[string]bool{}
	for _, row := range rows {
		isLabeled := row.Arrival == (row.Want != "")
		hasBlankEarlier := slices.ContainsFunc(row.Earlier, func(text string) bool {
			return strings.TrimSpace(text) == ""
		})
		if row.ID == "" || seen[row.ID] || strings.TrimSpace(row.Text) == "" || hasBlankEarlier || !isLabeled {
			t.Fatalf("row %q is incomplete, duplicated or mislabeled", row.ID)
		}
		seen[row.ID] = true
	}
	if len(rows) > evalMaxModelCalls {
		t.Fatalf("the set has %d rows, more than the %d model calls one run may make", len(rows), evalMaxModelCalls)
	}

	return rows
}

// meteredReader forwards reads to the real client, one at a time and without retries, and refuses
// to go past limit calls. It adds up the token usage the model reports.
type meteredReader struct {
	client       *llm.Client
	limit        int
	calls        int
	promptTokens int
	outputTokens int
	failures     []string
}

func (m *meteredReader) GenerateWithMedia(ctx context.Context, req llm.MediaRequest) (string, error) {
	if m.calls >= m.limit {
		return "", errEvalCallLimit
	}
	m.calls++

	text, usage, err := m.client.GenerateWithMediaUsage(ctx, req)
	m.promptTokens += usage.PromptTokens
	m.outputTokens += usage.OutputTokens
	if err != nil {
		m.failures = append(m.failures, err.Error())

		return "", fmt.Errorf("eval model read: %w", err)
	}

	return text, nil
}

// newEvalClient builds the model client from llm.api_key, llm.base_url and llm.model of the local
// config store, opened with DATABASE_URL and MASTER_KEY as the binary does. It never migrates or
// writes. The test is skipped when the environment or the key is missing.
func newEvalClient(t *testing.T) *llm.Client {
	t.Helper()

	env, err := config.LoadEnv(os.LookupEnv)
	if err != nil {
		t.Skipf("model evaluation skipped: %v", err)
	}

	pool, err := db.Open(t.Context(), env.DatabaseURL.Reveal(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open the local database: %v", err)
	}
	t.Cleanup(pool.Close)

	store, err := config.NewStore(t.Context(), pool, env.MasterKey, env.Environment, []config.Key{
		{Name: evalSettingLLMKey, Secret: true},
		{Name: evalSettingLLMModel, Default: llm.DefaultModel},
		{Name: evalSettingLLMBaseURL, Default: llm.DefaultBaseURL},
	})
	if err != nil {
		t.Fatalf("open the local config store: %v", err)
	}

	apiKey, err := store.Secret(t.Context(), evalSettingLLMKey)
	if errors.Is(err, config.ErrNotSet) {
		t.Skipf("model evaluation skipped: %s is not set in the local config store", evalSettingLLMKey)
	}
	if err != nil {
		t.Fatalf("read %s: %v", evalSettingLLMKey, err)
	}

	model, err := store.Get(t.Context(), evalSettingLLMModel)
	if err != nil {
		t.Fatalf("read %s: %v", evalSettingLLMModel, err)
	}

	baseURL, err := store.Get(t.Context(), evalSettingLLMBaseURL)
	if err != nil {
		t.Fatalf("read %s: %v", evalSettingLLMBaseURL, err)
	}

	// A private transport lets the test close its idle connections before the leak check runs.
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)

	client, err := llm.New(llm.Config{
		APIKey:     apiKey.Reveal(),
		Model:      model,
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: llm.DefaultTimeout, Transport: transport},
	})
	if err != nil {
		t.Fatalf("create the model client: %v", err)
	}

	t.Logf("model under evaluation: %s", model)

	return client
}

// TestEvalArrivalRead scores the model read on the frozen labeled set, with the production prompt
// and parser. It reports and never fails on quality: the pass rule is applied by whoever runs it.
func TestEvalArrivalRead(t *testing.T) {
	rows := loadEvalRows(t)
	reader := &meteredReader{client: newEvalClient(t), limit: evalMaxModelCalls}
	score := &evalScore{name: "model read"}

	for _, row := range rows {
		now := evalInstant(t, row.ID, row.Now)
		claim, decision := capitime.ReadTextForEval(t.Context(), reader, row.Earlier, row.Text, now)
		score.record(t, row, now, claim, decision)
	}

	score.report(t)
	t.Logf("model read: %d calls, %d prompt tokens, %d output tokens",
		reader.calls, reader.promptTokens, reader.outputTokens)
	t.Logf("model read: request errors %q", reader.failures)
}
