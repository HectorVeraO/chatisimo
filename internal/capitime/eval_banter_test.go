//go:build eval

package capitime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
	"github.com/HectorVeraO/chatisimo/internal/platform/llm"
)

// This file is the local evaluation of the banter line. It is built only with the "eval" tag, so
// "go test ./...", lint and CI never compile or run it. It calls the real model with the key stored
// in the local config store and costs money:
//
//	go test -tags eval -run TestEvalBanter -count=1 -v ./internal/capitime/

const (
	evalBanterSetPath       = "testdata/eval_banter.json"
	evalBanterMaxModelCalls = 40
	evalClockLayout         = "15:04"
	evalImmediateClaim      = "ahora"
)

// evalBanterRow is one invented arrival of the banter set: what the member wrote and the clock
// fields the service would compute for it. Activity lists lowercase word stems; a line that holds
// any of them picked up what the member said they would do. It is empty for a row that names no
// activity.
type evalBanterRow struct {
	ID           string   `json:"id"`
	Said         string   `json:"said"`
	Claim        string   `json:"claim"`
	ClaimedClock string   `json:"claimed_clock"`
	CapiClock    string   `json:"capi_clock"`
	DelayMinutes int      `json:"delay_minutes"`
	EasterEgg    bool     `json:"easter_egg"`
	Activity     []string `json:"activity"`
}

// input returns the generator input the service would build for the row, with the default prompts.
func (r evalBanterRow) input() capitime.BanterInput {
	return capitime.BanterInput{
		Locale:       capitime.LocaleESCL,
		Said:         capitime.SaidForEval(r.Said),
		ClaimSummary: r.Claim,
		ClaimedClock: r.ClaimedClock,
		CapiClock:    r.CapiClock,
		DelayMinutes: r.DelayMinutes,
		Immediate:    r.Claim == evalImmediateClaim,
		EasterEgg:    r.EasterEgg,
	}
}

// isConsistent reports whether the clock fields of the row agree: the promised time plus the delay
// is the real time, and only an on-time row has no delay.
func (r evalBanterRow) isConsistent() bool {
	claimed, err := time.Parse(evalClockLayout, r.ClaimedClock)
	if err != nil || r.DelayMinutes < 0 {
		return false
	}

	wantCapiClock := claimed.Add(time.Duration(r.DelayMinutes) * time.Minute).Format(evalClockLayout)
	isOnTime := r.DelayMinutes == 0

	return r.CapiClock == wantCapiClock && r.EasterEgg == isOnTime
}

func loadEvalBanterRows(t *testing.T) []evalBanterRow {
	t.Helper()

	raw, err := os.ReadFile(evalBanterSetPath)
	if err != nil {
		t.Fatalf("read %s: %v", evalBanterSetPath, err)
	}

	var rows []evalBanterRow
	err = json.Unmarshal(raw, &rows)
	if err != nil {
		t.Fatalf("parse %s: %v", evalBanterSetPath, err)
	}

	seen := map[string]bool{}
	for _, row := range rows {
		hasBadStem := false
		for _, stem := range row.Activity {
			if strings.TrimSpace(stem) == "" || stem != strings.ToLower(stem) {
				hasBadStem = true
			}
		}
		if row.ID == "" || seen[row.ID] || row.Claim == "" || hasBadStem || !row.isConsistent() {
			t.Fatalf("row %q is incomplete, duplicated or inconsistent", row.ID)
		}
		seen[row.ID] = true
	}
	if len(rows) > evalBanterMaxModelCalls {
		t.Fatalf("the set has %d rows, more than the %d model calls one run may make", len(rows), evalBanterMaxModelCalls)
	}

	return rows
}

// meteredGenerator forwards banter requests to the real client, one at a time and without retries,
// and refuses to go past limit calls. It adds up the token usage the model reports and keeps the
// line of the newest request, which the banter generator drops when it rejects it.
type meteredGenerator struct {
	client       *llm.Client
	limit        int
	calls        int
	promptTokens int
	outputTokens int
	failures     []string
	lastLine     string
	hasLine      bool
}

func (m *meteredGenerator) GenerateWithPrompt(ctx context.Context, systemInstruction, userPrompt string) (string, error) {
	m.lastLine, m.hasLine = "", false
	if m.calls >= m.limit {
		return "", errEvalCallLimit
	}
	m.calls++

	// A request with text only takes the same path as the production GenerateWithPrompt, and this
	// method also returns the token usage.
	text, usage, err := m.client.GenerateWithMediaUsage(ctx, llm.MediaRequest{Prompt: systemInstruction, Text: userPrompt})
	m.promptTokens += usage.PromptTokens
	m.outputTokens += usage.OutputTokens
	if err != nil {
		m.failures = append(m.failures, err.Error())

		return "", fmt.Errorf("eval banter request: %w", err)
	}

	m.lastLine, m.hasLine = text, true

	return text, nil
}

// evalBanterScore collects the outcome of the banter lines over the set.
type evalBanterScore struct {
	produced       int
	rejected       []string
	namesFeature   []string
	withActivity   int
	missedActivity []string
	noLine         []string
}

// record scores one row. hasLine is false when the request failed; raw is the line the model
// wrote, and isAccepted tells whether the production sanitizer kept it.
func (s *evalBanterScore) record(t *testing.T, row evalBanterRow, raw string, hasLine, isAccepted bool) {
	t.Helper()

	if !hasLine {
		s.noLine = append(s.noLine, row.ID)
		t.Logf("%s: no line (request failed)", row.ID)

		return
	}

	s.produced++
	if !isAccepted {
		s.rejected = append(s.rejected, row.ID)
	}

	hasName := capitime.NamesFeatureForEval(raw)
	if hasName {
		s.namesFeature = append(s.namesFeature, row.ID)
	}

	activity := "none in the row"
	if len(row.Activity) > 0 {
		s.withActivity++
		activity = "picked up"
		if !evalPicksUp(raw, row.Activity) {
			activity = "missed"
			s.missedActivity = append(s.missedActivity, row.ID)
		}
	}

	t.Logf("%s: line=%q rejected=%v names_feature=%v activity=%s", row.ID, raw, !isAccepted, hasName, activity)
}

func (s *evalBanterScore) report(t *testing.T, rows int) {
	t.Helper()

	t.Logf("banter: lines produced %d/%d, no line %v", s.produced, rows, s.noLine)
	t.Logf("banter: rejected by the sanitizer %d/%d %v", len(s.rejected), s.produced, s.rejected)
	t.Logf("banter: lines with the feature name %d/%d %v", len(s.namesFeature), s.produced, s.namesFeature)
	t.Logf("banter: activity missed %d/%d %v", len(s.missedActivity), s.withActivity, s.missedActivity)
}

// evalPicksUp reports whether line holds any of the lowercase stems. The stems carry no accents,
// so the accents of line are dropped first.
func evalPicksUp(line string, stems []string) bool {
	lower := strings.NewReplacer("\u00e1", "a", "\u00e9", "e", "\u00ed", "i", "\u00f3", "o", "\u00fa", "u").Replace(strings.ToLower(line))
	for _, stem := range stems {
		if strings.Contains(lower, stem) {
			return true
		}
	}

	return false
}

// TestEvalBanter writes one banter line per row of the invented set with the production generator
// and the default prompts, and logs for each whether the sanitizer rejected it, whether it carries
// the feature name and whether it picked up the activity. It reports and never fails on quality:
// the pass rule is applied by whoever runs it.
func TestEvalBanter(t *testing.T) {
	rows := loadEvalBanterRows(t)
	meter := &meteredGenerator{client: newEvalClient(t), limit: evalBanterMaxModelCalls}
	generator := capitime.NewLLMBanterGenerator(capitime.LLMBanterConfig{Generator: meter})
	score := &evalBanterScore{}

	for _, row := range rows {
		_, err := generator.GenerateBanter(t.Context(), row.input())
		score.record(t, row, meter.lastLine, meter.hasLine, err == nil)
	}

	score.report(t, len(rows))
	t.Logf("banter: %d calls, %d prompt tokens, %d output tokens", meter.calls, meter.promptTokens, meter.outputTokens)
	t.Logf("banter: request errors %q", meter.failures)
}
