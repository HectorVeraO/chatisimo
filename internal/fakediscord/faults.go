package fakediscord

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// Route labels the fake knows, in the form FailNext and the control API accept. Parameter
// names inside braces do not matter when a fault is matched.
const (
	routeGetGateway        = "GET /gateway"
	routePutCommands       = "PUT /applications/{app}/commands"
	routePutGuildCommands  = "PUT /applications/{app}/guilds/{guild}/commands"
	routeCreateDM          = "POST /users/@me/channels"
	routeCallback          = "POST /interactions/{id}/{token}/callback"
	routeEditOriginal      = "PATCH /webhooks/{app}/{token}/messages/@original"
	routeGetOriginal       = "GET /webhooks/{app}/{token}/messages/@original"
	routeDeleteOriginal    = "DELETE /webhooks/{app}/{token}/messages/@original"
	routeFollowup          = "POST /webhooks/{app}/{token}"
	routeEditMessage       = "PATCH /webhooks/{app}/{token}/messages/{id}"
	routeGetMessage        = "GET /webhooks/{app}/{token}/messages/{id}"
	routeDeleteMessage     = "DELETE /webhooks/{app}/{token}/messages/{id}"
	routeGatewayIdentify   = "GATEWAY identify"
	rateLimitRetryAfter    = "0.2"
	rateLimitBucket        = "fake"
	rateLimitedMessage     = "You are being rate limited."
	injectedFaultMessage   = "Injected fault"
	maxQueuedFaultsByRoute = 1000

	minFaultStatus = 400
	maxFaultStatus = 599
	minCloseCode   = 4000
	maxCloseCode   = 4999
)

var knownRoutes = []string{
	routeGetGateway, routePutCommands, routePutGuildCommands, routeCreateDM, routeCreateMessage,
	routeCallback, routeEditOriginal, routeGetOriginal, routeDeleteOriginal, routeFollowup,
	routeEditMessage, routeGetMessage, routeDeleteMessage, routeGatewayIdentify,
}

var routeParam = regexp.MustCompile(`\{[^}]*\}`)

// ErrInvalidFault is returned for a fault the fake cannot apply.
var ErrInvalidFault = errors.New("fakediscord: invalid fault")

// fault is one failure waiting for the next request on its route.
type fault struct {
	status int
	code   int
}

// normalizeRoute turns a route label into the key faults are matched on: no API prefix and
// anonymous parameters.
func normalizeRoute(route string) string {
	trimmed := strings.TrimSpace(route)
	withoutPrefix := strings.Replace(trimmed, " "+restPrefix, " ", 1)

	return routeParam.ReplaceAllString(withoutPrefix, "{}")
}

// canonicalRoute returns the known route label that route names, or false.
func canonicalRoute(route string) (string, bool) {
	key := normalizeRoute(route)
	for _, known := range knownRoutes {
		if normalizeRoute(known) == key {
			return known, true
		}
	}

	return "", false
}

// validateFault reports every reason a fault cannot be applied.
func validateFault(route string, status, code int) (string, error) {
	canonical, isKnown := canonicalRoute(route)
	var problems []error
	if !isKnown {
		problems = append(problems, fmt.Errorf("route %q is not one the fake serves", route))
	}

	isGateway := canonical == routeGatewayIdentify
	switch {
	case isGateway:
		isCloseCode := code >= minCloseCode && code <= maxCloseCode
		if !isCloseCode {
			problems = append(problems, fmt.Errorf("close code %d is outside %d-%d", code, minCloseCode, maxCloseCode))
		}
	case isKnown:
		isErrorStatus := status >= minFaultStatus && status <= maxFaultStatus
		if !isErrorStatus {
			problems = append(problems, fmt.Errorf("status %d is outside %d-%d", status, minFaultStatus, maxFaultStatus))
		}
		if code < 0 {
			problems = append(problems, fmt.Errorf("code %d is negative", code))
		}
	}

	joined := errors.Join(problems...)
	if joined != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidFault, joined)
	}

	return canonical, nil
}

// FailNext makes the next request on route fail instead of being served. route is a label
// such as "POST /channels/{id}/messages"; status and discordCode shape the error answer (a
// status of 429 answers a rate limit with Retry-After 0.2 and ignores discordCode). The
// route "GATEWAY identify" closes the next Identify with the Gateway close code
// discordCode, status being ignored, even when the token is right. Several calls queue up
// in order. FailNext returns ErrInvalidFault when the fault cannot be applied.
func (s *Server) FailNext(route string, status, discordCode int) error {
	return s.addFault(route, status, discordCode)
}

func (s *Server) addFault(route string, status, code int) error {
	canonical, err := validateFault(route, status, code)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	queue := s.faults[canonical]
	if len(queue) >= maxQueuedFaultsByRoute {
		return fmt.Errorf("%w: %d faults already wait on %q", ErrInvalidFault, len(queue), canonical)
	}
	s.faults[canonical] = append(queue, fault{status: status, code: code})

	return nil
}

// takeFault removes and returns the oldest fault waiting on the route.
func (s *Server) takeFault(route string) (fault, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	queue := s.faults[route]
	if len(queue) == 0 {
		return fault{}, false
	}
	next := queue[0]
	s.faults[route] = queue[1:]

	return next, true
}

// faulted wraps a handler so the next request on route can be failed by FailNext.
func (s *Server) faulted(route string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		injected, isInjected := s.takeFault(route)
		if !isInjected {
			next(w, r)
			return
		}
		s.logger.Info("fault injected", "route", route, "status", injected.status)
		writeFault(w, injected)
	}
}

func writeFault(w http.ResponseWriter, f fault) {
	if f.status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", rateLimitRetryAfter)
		w.Header().Set("X-RateLimit-Bucket", rateLimitBucket)
		writeJSON(w, f.status, map[string]any{
			"message":     rateLimitedMessage,
			"retry_after": 0.2,
			"global":      false,
		})

		return
	}

	writeError(w, f.status, f.code, injectedFaultMessage)
}

// PendingFaults returns how many faults queued by FailNext still wait on route. A fault
// that was served leaves the count; route is validated the way FailNext validates it.
func (s *Server) PendingFaults(route string) (int, error) {
	canonical, err := validateFault(route, http.StatusInternalServerError, 0)
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.faults[canonical]), nil
}
