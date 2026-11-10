package fakediscord

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

const (
	maxGenerateRules = 1000
	maxGenerateCalls = 1000

	// noArrivalAnswer is what an unscripted request that carries media and no text gets: there is
	// no text to echo, and the only caller that sends such a request reads it as "nothing to do".
	noArrivalAnswer = `{"arrival":false}`
)

// ErrInvalidGenerated is returned for a scripted generateContent answer the fake cannot store.
var ErrInvalidGenerated = errors.New("fakediscord: invalid generated answer")

// GenerateCall records one generateContent request the fake answered.
type GenerateCall struct {
	// System is the text of the system instruction.
	System string `json:"system"`
	// User is the text of the user message, trimmed.
	User string `json:"user"`
	// MIMETypes lists the type of each inline media part of the user message, in order.
	MIMETypes []string `json:"mime_types"`
}

// generateRule answers every generateContent request whose system instruction and user text
// contain its two substrings.
type generateRule struct {
	systemContains string
	userContains   string
	response       string
}

type generateContentBody struct {
	SystemInstruction struct {
		Parts []generateContentPart `json:"parts"`
	} `json:"systemInstruction"`
	Contents []struct {
		Parts []generateContentPart `json:"parts"`
	} `json:"contents"`
}

type generateContentPart struct {
	Text       string `json:"text"`
	InlineData *struct {
		MIMEType string `json:"mime_type"`
	} `json:"inline_data"`
}

// SetGenerated scripts the answer to future generateContent requests whose system instruction
// contains systemContains and whose user text contains userContains; an empty substring matches
// every request. The first rule that matches wins. A request no rule matches gets the text set
// with SetRephrased for its user text, else its user text echoed back.
func (s *Server) SetGenerated(systemContains, userContains, response string) error {
	if strings.TrimSpace(response) == "" {
		return fmt.Errorf("%w: response is empty", ErrInvalidGenerated)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.generateRules) >= maxGenerateRules {
		return fmt.Errorf("%w: at most %d answers can be scripted", ErrInvalidGenerated, maxGenerateRules)
	}

	rule := generateRule{systemContains: systemContains, userContains: userContains, response: response}
	s.generateRules = append(s.generateRules, rule)

	return nil
}

// GenerateCalls returns the generateContent requests the fake answered, oldest first. It keeps the
// newest 1000.
func (s *Server) GenerateCalls() []GenerateCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	calls := make([]GenerateCall, 0, len(s.generateCalls))
	for _, call := range s.generateCalls {
		call.MIMETypes = slices.Clone(call.MIMETypes)
		calls = append(calls, call)
	}

	return calls
}

// parseGenerateCall flattens a generateContent body into the texts and media types it carries.
func parseGenerateCall(body generateContentBody) GenerateCall {
	var system strings.Builder
	for _, part := range body.SystemInstruction.Parts {
		system.WriteString(part.Text)
	}

	var user strings.Builder
	mimeTypes := []string{}
	for _, content := range body.Contents {
		for _, part := range content.Parts {
			user.WriteString(part.Text)
			if part.InlineData != nil {
				mimeTypes = append(mimeTypes, part.InlineData.MIMEType)
			}
		}
	}

	return GenerateCall{
		System:    system.String(),
		User:      strings.TrimSpace(user.String()),
		MIMETypes: mimeTypes,
	}
}

// answerGenerateLocked records call and picks its answer: the first matching rule, then the
// rephrase map, then the user text itself. It must run with s.mu held.
func (s *Server) answerGenerateLocked(call GenerateCall) string {
	s.generateCalls = append(s.generateCalls, call)
	if len(s.generateCalls) > maxGenerateCalls {
		s.generateCalls = s.generateCalls[len(s.generateCalls)-maxGenerateCalls:]
	}

	for _, rule := range s.generateRules {
		isMatch := strings.Contains(call.System, rule.systemContains) && strings.Contains(call.User, rule.userContains)
		if isMatch {
			return rule.response
		}
	}

	if call.User == "" {
		return noArrivalAnswer
	}

	rephrased, found := s.rephrases[call.User]
	if found {
		return rephrased
	}

	return call.User
}

func (s *Server) handleGenerateContent(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.PathValue("model"), ":generateContent") {
		handleNotFound(w, r)
		return
	}

	var body generateContentBody
	if !decodeBody(w, r, &body) {
		return
	}

	call := parseGenerateCall(body)
	hasNothingToRead := call.User == "" && len(call.MIMETypes) == 0
	if hasNothingToRead {
		writeError(w, http.StatusBadRequest, codeInvalidFormBody, "Invalid Form Body")
		return
	}

	s.mu.Lock()
	answer := s.answerGenerateLocked(call)
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"candidates": []map[string]any{
			{
				"content": map[string]any{
					"parts": []map[string]string{
						{"text": answer},
					},
				},
			},
		},
	})
}

func (s *Server) handleControlGenerated(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SystemContains string `json:"system_contains"`
		UserContains   string `json:"user_contains"`
		Response       string `json:"response"`
	}
	if !decodeBody(w, r, &body) {
		return
	}

	err := s.SetGenerated(body.SystemContains, body.UserContains, body.Response)
	if err != nil {
		invalidBody(err.Error()).write(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleControlGenerateCalls(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.GenerateCalls())
}
