package fakediscord

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

const (
	tokenSecondSegmentBytes = 6
	// 20 random bytes encode to exactly 27 base64url characters.
	tokenThirdSegmentBytes = 20
)

// newToken builds a synthetic bot token whose first segment is the base64 of applicationID,
// the only part a Discord client library reads. The other segments are random, so the
// token is unique per process and appears in no tracked file.
func newToken(applicationID string) (string, error) {
	if !isSnowflake(applicationID) {
		return "", fmt.Errorf("token: application id %q is not a snowflake", applicationID)
	}

	second, err := randomSegment(tokenSecondSegmentBytes)
	if err != nil {
		return "", err
	}
	third, err := randomSegment(tokenThirdSegmentBytes)
	if err != nil {
		return "", err
	}

	first := base64.RawStdEncoding.EncodeToString([]byte(applicationID))
	token := first + "." + second + "." + third

	return token, nil
}

func randomSegment(size int) (string, error) {
	buf := make([]byte, size)
	_, err := rand.Read(buf)
	if err != nil {
		return "", fmt.Errorf("token: read random bytes: %w", err)
	}

	segment := base64.RawURLEncoding.EncodeToString(buf)

	return segment, nil
}

// tokensEqual compares two tokens without leaking where they differ.
func tokensEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
