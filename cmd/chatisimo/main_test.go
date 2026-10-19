package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr bool
	}{
		{name: "no arguments", args: nil, wantCode: 2, wantStderr: true},
		{name: "unknown subcommand", args: []string{"bogus"}, wantCode: 2, wantStderr: true},
		{name: "version", args: []string{"version"}, wantCode: 0, wantStdout: version + "\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run(tc.args, &stdout, &stderr)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d", code, tc.wantCode)
			}
			if stdout.String() != tc.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), tc.wantStdout)
			}
			hasUsage := strings.Contains(stderr.String(), "usage:")
			if hasUsage != tc.wantStderr {
				t.Errorf("stderr usage present = %v, want %v (stderr=%q)", hasUsage, tc.wantStderr, stderr.String())
			}
		})
	}
}
