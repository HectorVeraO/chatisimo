package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// execute runs the tool with args and returns its exit code, stdout and stderr.
func execute(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)

	return code, stdout.String(), stderr.String()
}

// writeFile writes content to a new file in a temp dir and returns its path.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()

	filePath := filepath.Join(t.TempDir(), name)
	err := os.WriteFile(filePath, []byte(content), 0o600)
	if err != nil {
		t.Fatalf("write %s: %v", name, err)
	}

	return filePath
}

func fixture(name string) string {
	return filepath.Join("testdata", name)
}

// basic.txt holds 18 of 20 statements covered (90.0) plus uncovered excluded packages.
func TestGateComparesTotalWithFloor(t *testing.T) {
	tests := []struct {
		name     string
		floor    string
		wantCode int
	}{
		{name: "total above the floor", floor: "89.9", wantCode: exitOK},
		{name: "total equal to the floor", floor: "90.0", wantCode: exitOK},
		{name: "total below the floor", floor: "90.1", wantCode: exitBelowFloor},
		{name: "floor of zero", floor: "0", wantCode: exitOK},
		{name: "floor of one hundred", floor: "100", wantCode: exitBelowFloor},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			floorPath := writeFile(t, ".coverage-floor", tc.floor+"\n")

			code, stdout, stderr := execute(t, "-profile", fixture("basic.txt"), "-floor", floorPath)

			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, tc.wantCode, stdout, stderr)
			}
			if !strings.Contains(stdout, "total 90.0\n") {
				t.Errorf("stdout lacks the total:\n%s", stdout)
			}
			if !strings.Contains(stdout, "\nfloor ") {
				t.Errorf("stdout lacks the floor:\n%s", stdout)
			}
			if tc.wantCode == exitBelowFloor && !strings.Contains(stderr, "below the floor") {
				t.Errorf("stderr does not explain the failure:\n%s", stderr)
			}
		})
	}
}

func TestReportListsOnlyCountedPackages(t *testing.T) {
	code, stdout, stderr := execute(t, "-profile", fixture("basic.txt"))

	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr)
	}
	wantLines := []string{
		"package internal/alpha 80.0 (8/10)\n",
		"package internal/beta 100.0 (10/10)\n",
	}
	for _, want := range wantLines {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	for _, excluded := range []string{"fakediscord", "coveragegate", "e2e"} {
		if strings.Contains(stdout, excluded) {
			t.Errorf("stdout lists excluded package %q:\n%s", excluded, stdout)
		}
	}
}

func TestModuleFlagSelectsThePathPrefixToTrim(t *testing.T) {
	profile := writeFile(t, "custom.txt", "mode: set\n"+
		"example.org/other/internal/alpha/a.go:1.1,2.1 4 1\n"+
		"example.org/other/e2e/world.go:1.1,2.1 9 0\n"+
		"example.org/other/main.go:1.1,2.1 4 0\n"+
		"vendor.example.net/lib/x.go:1.1,2.1 2 0\n")

	code, stdout, stderr := execute(t, "-profile", profile, "-module", "example.org/other")

	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr)
	}
	wantLines := []string{
		"package internal/alpha 100.0 (4/4)\n",
		"package . 0.0 (0/4)\n",
		"package vendor.example.net/lib 0.0 (0/2)\n",
		"total 40.0\n",
	}
	for _, want := range wantLines {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "e2e") {
		t.Errorf("stdout lists the excluded package under the custom module:\n%s", stdout)
	}
}

func TestExclusionRespectsPathBoundaries(t *testing.T) {
	code, stdout, stderr := execute(t, "-profile", fixture("boundary.txt"))

	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "package cmd/coveragegatekeeper 0.0 (0/10)\n") {
		t.Errorf("a package that only shares a name prefix was excluded:\n%s", stdout)
	}
	if !strings.Contains(stdout, "total 50.0\n") {
		t.Errorf("total is not 50.0:\n%s", stdout)
	}
}

func TestDuplicateBlocksCountOnceAndAreCoveredIfAnyRunHitThem(t *testing.T) {
	code, stdout, stderr := execute(t, "-profile", fixture("duplicates.txt"))

	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "package internal/alpha 50.0 (6/12)\n") {
		t.Errorf("duplicates were not merged by block:\n%s", stdout)
	}
}

func TestTotalIsTruncatedToOneDecimal(t *testing.T) {
	tests := []struct {
		floor    string
		wantCode int
	}{
		{floor: "66.6", wantCode: exitOK},
		{floor: "66.7", wantCode: exitBelowFloor},
	}
	for _, tc := range tests {
		t.Run(tc.floor, func(t *testing.T) {
			floorPath := writeFile(t, ".coverage-floor", tc.floor)

			code, stdout, _ := execute(t, "-profile", fixture("truncation.txt"), "-floor", floorPath)

			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d", code, tc.wantCode)
			}
			if !strings.Contains(stdout, "total 66.6\n") {
				t.Errorf("total is not truncated to 66.6:\n%s", stdout)
			}
		})
	}
}

func TestSuggestedFloorOnlyWhenTotalExceedsFloorByMoreThanOnePoint(t *testing.T) {
	tests := []struct {
		name        string
		floor       string
		wantSuggest bool
	}{
		{name: "surplus of 1.1", floor: "88.9", wantSuggest: true},
		{name: "surplus of exactly 1.0", floor: "89.0", wantSuggest: false},
		{name: "equal", floor: "90.0", wantSuggest: false},
		{name: "much lower floor", floor: "10", wantSuggest: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			floorPath := writeFile(t, ".coverage-floor", tc.floor)

			code, stdout, stderr := execute(t, "-profile", fixture("basic.txt"), "-floor", floorPath)

			if code != exitOK {
				t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr)
			}
			hasSuggestion := strings.Contains(stdout, "suggested floor 89.5\n")
			if hasSuggestion != tc.wantSuggest {
				t.Errorf("suggestion present = %t, want %t:\n%s", hasSuggestion, tc.wantSuggest, stdout)
			}
		})
	}
}

func TestNoSuggestionWhenTotalIsBelowTheFloor(t *testing.T) {
	floorPath := writeFile(t, ".coverage-floor", "95")

	code, stdout, _ := execute(t, "-profile", fixture("basic.txt"), "-floor", floorPath)

	if code != exitBelowFloor {
		t.Fatalf("exit = %d, want 1", code)
	}
	if strings.Contains(stdout, "suggested floor") {
		t.Errorf("suggestion printed for a failing total:\n%s", stdout)
	}
}

func TestMeasureOnlyModeNeedsNoFloor(t *testing.T) {
	code, stdout, stderr := execute(t, "-profile", fixture("basic.txt"))

	if code != exitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr)
	}
	for _, want := range []string{"total 90.0\n", "suggested floor 89.5\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "\nfloor ") {
		t.Errorf("measure-only output names a floor:\n%s", stdout)
	}
}

func TestLoweredFloorFailsAgainstTheBaseFloor(t *testing.T) {
	tests := []struct {
		name     string
		floor    string
		base     string
		wantCode int
	}{
		{name: "floor lowered", floor: "80", base: "85", wantCode: exitFloorLowered},
		{name: "floor lowered by one decimal", floor: "84.9", base: "85.0", wantCode: exitFloorLowered},
		{name: "floor unchanged", floor: "85.0", base: "85", wantCode: exitOK},
		{name: "floor raised", floor: "86", base: "85", wantCode: exitOK},
		{name: "floor lowered and total below it", floor: "95", base: "96", wantCode: exitFloorLowered},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			floorPath := writeFile(t, ".coverage-floor", tc.floor)
			basePath := writeFile(t, "base-floor", tc.base)

			code, _, stderr := execute(t, "-profile", fixture("basic.txt"), "-floor", floorPath, "-base-floor", basePath)

			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, tc.wantCode, stderr)
			}
			if tc.wantCode == exitFloorLowered && !strings.Contains(stderr, "lower than the base floor") {
				t.Errorf("stderr does not explain the failure:\n%s", stderr)
			}
		})
	}
}

func TestUsageAndInputErrorsExitTwo(t *testing.T) {
	goodFloor := writeFile(t, "good-floor", "50")
	emptyProfile := writeFile(t, "empty.txt", "")
	missing := filepath.Join(t.TempDir(), "missing")

	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{name: "no arguments", args: nil, wantStderr: "-profile is required"},
		{name: "unknown flag", args: []string{"-nope"}, wantStderr: "flag provided but not defined"},
		{name: "positional argument", args: []string{"-profile", fixture("basic.txt"), "extra"}, wantStderr: "unexpected positional arguments"},
		{name: "base floor without floor", args: []string{"-profile", fixture("basic.txt"), "-base-floor", goodFloor}, wantStderr: "-base-floor needs -floor"},
		{name: "missing profile file", args: []string{"-profile", missing}, wantStderr: "open profile"},
		{name: "empty profile file", args: []string{"-profile", emptyProfile}, wantStderr: "empty profile"},
		{name: "profile without mode line", args: []string{"-profile", fixture("nomode.txt")}, wantStderr: "is not a mode line"},
		{name: "malformed profile", args: []string{"-profile", fixture("malformed.txt")}, wantStderr: "want 3 fields"},
		{name: "only excluded packages", args: []string{"-profile", fixture("onlyexcluded.txt")}, wantStderr: "no statements outside the excluded packages"},
		{name: "missing floor file", args: []string{"-profile", fixture("basic.txt"), "-floor", missing}, wantStderr: "read floor"},
		{name: "missing base floor file", args: []string{"-profile", fixture("basic.txt"), "-floor", goodFloor, "-base-floor", missing}, wantStderr: "read floor"},
		{name: "empty module", args: []string{"-profile", fixture("basic.txt"), "-module", " "}, wantStderr: "-module must not be empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := execute(t, tc.args...)

			if code != exitUsage {
				t.Fatalf("exit = %d, want 2\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			if !strings.Contains(stderr, tc.wantStderr) {
				t.Errorf("stderr lacks %q:\n%s", tc.wantStderr, stderr)
			}
		})
	}
}

func TestMalformedProfileReportsEveryBadLine(t *testing.T) {
	code, _, stderr := execute(t, "-profile", fixture("malformed.txt"))

	if code != exitUsage {
		t.Fatalf("exit = %d, want 2", code)
	}
	for _, want := range []string{"line 2:", "line 3:", "line 4:", "line 5:"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}

func TestHostileProfileReportIsBounded(t *testing.T) {
	var profile strings.Builder
	profile.WriteString("mode: set\n")
	for range 50 {
		profile.WriteString("not a block line at all\n")
	}
	profilePath := writeFile(t, "hostile.txt", profile.String())

	code, _, stderr := execute(t, "-profile", profilePath)

	if code != exitUsage {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "and 40 more") {
		t.Errorf("stderr lacks the overflow line:\n%s", stderr)
	}
	if strings.Contains(stderr, "line 12:") {
		t.Errorf("stderr reports more than ten bad lines:\n%s", stderr)
	}
}

func TestOversizedProfileLineIsRejectedWithBoundedReport(t *testing.T) {
	huge := defaultModule + "/internal/a/" + strings.Repeat("a", 2<<20) + ".go:1.1,2.1 3 1\n"
	profilePath := writeFile(t, "huge.txt", "mode: set\n"+huge)

	code, _, stderr := execute(t, "-profile", profilePath)

	if code != exitUsage {
		t.Fatalf("exit = %d, want 2\nstderr: %.300s", code, stderr)
	}
	if len(stderr) > 2000 {
		t.Errorf("stderr is unbounded (%d bytes)", len(stderr))
	}
}

func TestFloorFileInputAbuse(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantCode int
	}{
		{name: "surrounding whitespace", content: " \t 90.0 \r\n\n", wantCode: exitOK},
		{name: "no trailing newline", content: "90.0", wantCode: exitOK},
		{name: "integer", content: "90", wantCode: exitOK},
		{name: "negative", content: "-1", wantCode: exitUsage},
		{name: "above one hundred", content: "100.1", wantCode: exitUsage},
		{name: "text", content: "ninety", wantCode: exitUsage},
		{name: "empty", content: "", wantCode: exitUsage},
		{name: "whitespace only", content: " \n", wantCode: exitUsage},
		{name: "two numbers", content: "80 90", wantCode: exitUsage},
		{name: "two lines", content: "80\n90\n", wantCode: exitUsage},
		{name: "not a number", content: "NaN", wantCode: exitUsage},
		{name: "infinity", content: "Inf", wantCode: exitUsage},
		{name: "exponent", content: "9e1", wantCode: exitUsage},
		{name: "hexadecimal", content: "0x5A", wantCode: exitUsage},
		{name: "two dots", content: "8.0.1", wantCode: exitUsage},
		{name: "lone dot", content: ".", wantCode: exitUsage},
		{name: "percent sign", content: "90%", wantCode: exitUsage},
		{name: "long text", content: strings.Repeat("9", 5000), wantCode: exitUsage},
		{name: "byte order mark", content: "\ufeff90.0\n", wantCode: exitOK},
		{name: "byte order mark only", content: "\ufeff", wantCode: exitUsage},
		{name: "fullwidth digits", content: "\uff19\uff10", wantCode: exitUsage},
		{name: "zero-width space inside", content: "9\u200b0", wantCode: exitUsage},
		{name: "arabic-indic digits", content: "\u0669\u0660", wantCode: exitUsage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			floorPath := writeFile(t, ".coverage-floor", tc.content)

			code, _, stderr := execute(t, "-profile", fixture("basic.txt"), "-floor", floorPath)

			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, tc.wantCode, stderr)
			}
			if len(stderr) > 2000 {
				t.Errorf("stderr echoes unbounded input (%d bytes)", len(stderr))
			}
		})
	}
}

func TestBaseFloorFileInputAbuse(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantCode int
	}{
		{name: "surrounding whitespace", content: " \t 90.0 \r\n\n", wantCode: exitOK},
		{name: "negative", content: "-1", wantCode: exitUsage},
		{name: "above one hundred", content: "100.1", wantCode: exitUsage},
		{name: "text", content: "ninety", wantCode: exitUsage},
		{name: "empty", content: "", wantCode: exitUsage},
		{name: "two lines", content: "80\n90\n", wantCode: exitUsage},
		{name: "not a number", content: "NaN", wantCode: exitUsage},
		{name: "exponent", content: "9e1", wantCode: exitUsage},
		{name: "long text", content: strings.Repeat("9", 5000), wantCode: exitUsage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			floorPath := writeFile(t, ".coverage-floor", "90.0")
			baseFloorPath := writeFile(t, ".coverage-floor-base", tc.content)

			code, _, stderr := execute(t, "-profile", fixture("basic.txt"), "-floor", floorPath, "-base-floor", baseFloorPath)

			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, tc.wantCode, stderr)
			}
			if len(stderr) > 2000 {
				t.Errorf("stderr echoes unbounded input (%d bytes)", len(stderr))
			}
		})
	}
}

func FuzzParseFloor(f *testing.F) {
	for _, seed := range []string{"90.0", " 1 ", "-1", "101", "NaN", "Inf", "1e9", "0x1p3", ".", "", "9.9.9", "\x00", "\ufeff90"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, text string) {
		value, err := parseFloor(text)
		if err != nil {
			return
		}
		if value < 0 || value > 100 {
			t.Fatalf("parseFloor(%q) = %v, outside 0 to 100", text, value)
		}
	})
}

func FuzzParseProfile(f *testing.F) {
	seeds := []string{
		"mode: set\n" + defaultModule + "/internal/a/a.go:1.1,2.1 3 1\n",
		"mode: count\n" + defaultModule + "/e2e/w.go:1.1,2.1 3 0\n",
		"mode: set\nx:1.1,2.1 -3 1\n",
		"mode: set\n:\n",
		"mode: bogus\n",
		"",
		"mode: set\n\x00\xff 1 1\n",
		"mode: set\n" + strings.Repeat("a", 5000) + ":1.1,2.1 1 18446744073709551616\n",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, content string) {
		totals, err := parseProfile(strings.NewReader(content), defaultModule)
		if err != nil {
			return
		}
		for name, entry := range totals {
			isConsistent := entry.statements >= 0 && entry.covered >= 0 && entry.covered <= entry.statements
			if !isConsistent {
				t.Fatalf("package %q has %d covered of %d statements", name, entry.covered, entry.statements)
			}
			for _, excluded := range []string{"internal/fakediscord", "cmd/fakediscord", "cmd/coveragegate", "e2e"} {
				isExcluded := name == excluded || strings.HasPrefix(name, excluded+"/")
				if isExcluded {
					t.Fatalf("excluded package %q was counted", name)
				}
			}
		}
	})
}
