// Command coveragegate checks the merged coverage profile against the stored floor.
//
// It reads a "go tool covdata textfmt" profile, drops the test infrastructure packages, and
// compares the total with the floor in .coverage-floor. It also fails when the floor was
// lowered against the floor on the base branch, so the floor only goes up. Without -floor it
// only measures: it prints the total and a suggested floor and exits 0.
//
// Exit codes: 0 pass, 1 total below the floor, 2 usage or unreadable input, 3 floor lowered.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

const (
	exitOK           = 0
	exitBelowFloor   = 1
	exitUsage        = 2
	exitFloorLowered = 3

	defaultModule = "github.com/HectorVeraO/chatisimo"

	// maxLineBytes bounds one profile line; real lines are under 200 bytes.
	maxLineBytes = 1 << 20
	// maxReportedErrors bounds how many input problems are printed.
	maxReportedErrors = 10
	// maxEchoBytes bounds how much of a bad input value is echoed back.
	maxEchoBytes = 40
	// suggestionMarginTenths is the surplus over the floor (1.0 point) that triggers a suggestion.
	suggestionMarginTenths = 10
	// suggestionSlackTenths is how far below the total the suggested floor sits (0.5 points).
	suggestionSlackTenths = 5
)

// excludedPackages are the packages, relative to the module path, that do not count toward
// coverage: test infrastructure and the gate itself. A package is excluded when its path
// equals an entry or lies below it.
func excludedPackages() []string {
	return []string{
		"internal/fakediscord",
		"cmd/fakediscord",
		"cmd/coveragegate",
		"e2e",
	}
}

// emit writes a formatted report line. If the stream is unwritable there is nowhere left to
// report it.
func emit(writer io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(writer, format, args...)
}

// block is one coverage block of a profile.
type block struct {
	pkg        string
	statements int
	covered    bool
}

// packageTotal accumulates the statements of one package.
type packageTotal struct {
	statements int
	covered    int
}

func main() {
	code := run(os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}

// run executes the gate and returns the process exit code. Results go to stdout and problems
// to stderr.
func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("coveragegate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profilePath := flags.String("profile", "", "textfmt coverage profile to measure (required)")
	floorPath := flags.String("floor", "", "file holding the minimum total; without it the tool only measures")
	baseFloorPath := flags.String("base-floor", "", "file holding the floor on the base branch; the floor may not be lower (needs -floor)")
	module := flags.String("module", defaultModule, "module path trimmed from profile file paths")
	flags.Usage = func() {
		// If stderr is unwritable there is nowhere left to report it.
		_, _ = fmt.Fprintln(stderr, "usage: coveragegate -profile <file> [-floor <file> [-base-floor <file>]] [-module <path>]")
		_, _ = fmt.Fprintln(stderr, "without -floor the tool only measures: it prints the total and a suggested floor, and exits 0")
		flags.PrintDefaults()
	}

	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		return exitUsage
	}

	problems := validateArgs(flags.NArg(), *profilePath, *floorPath, *baseFloorPath, *module)
	if len(problems) > 0 {
		reportProblems(stderr, problems)
		flags.Usage()

		return exitUsage
	}

	return measure(*profilePath, *floorPath, *baseFloorPath, *module, stdout, stderr)
}

func validateArgs(extraArgs int, profilePath, floorPath, baseFloorPath, module string) []string {
	var problems []string
	if extraArgs > 0 {
		problems = append(problems, "unexpected positional arguments")
	}
	if profilePath == "" {
		problems = append(problems, "-profile is required")
	}
	if baseFloorPath != "" && floorPath == "" {
		problems = append(problems, "-base-floor needs -floor")
	}
	if strings.TrimSpace(module) == "" {
		problems = append(problems, "-module must not be empty")
	}

	return problems
}

func measure(profilePath, floorPath, baseFloorPath, module string, stdout, stderr io.Writer) int {
	var problems []string

	totals, err := loadProfile(profilePath, module)
	if err != nil {
		problems = append(problems, err.Error())
	}

	var floor, baseFloor float64
	if floorPath != "" {
		floor, err = readFloor(floorPath)
		if err != nil {
			problems = append(problems, err.Error())
		}
	}
	if baseFloorPath != "" {
		baseFloor, err = readFloor(baseFloorPath)
		if err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		reportProblems(stderr, problems)

		return exitUsage
	}

	statements, covered := sumTotals(totals)
	if statements == 0 {
		reportProblems(stderr, []string{"profile has no statements outside the excluded packages"})

		return exitUsage
	}
	// Integer division truncates, so 79.99 never rounds up to 80.0.
	totalTenths := covered * 1000 / statements

	printReport(stdout, totals, totalTenths)

	if floorPath == "" {
		printSuggestion(stdout, totalTenths)

		return exitOK
	}

	return judge(totalTenths, floor, baseFloorPath != "", baseFloor, stdout, stderr)
}

// judge compares the total with the floor and the floor with the base floor. A lowered floor
// wins over a total below the floor: lowering the floor is the way to dodge the gate.
func judge(totalTenths int, floor float64, hasBase bool, baseFloor float64, stdout, stderr io.Writer) int {
	total := float64(totalTenths) / 10
	emit(stdout, "floor %.1f\n", floor)

	isLowered := hasBase && floor < baseFloor
	if isLowered {
		emit(stderr, "error: floor %.1f is lower than the base floor %.1f: the floor only goes up\n", floor, baseFloor)
	}
	isBelow := total < floor
	if isBelow {
		emit(stderr, "error: total %.1f is below the floor %.1f\n", total, floor)
	}

	if isLowered {
		return exitFloorLowered
	}
	if isBelow {
		return exitBelowFloor
	}

	// The epsilon absorbs float representation of one-decimal values.
	isWellAbove := total-floor > float64(suggestionMarginTenths)/10+1e-9
	if isWellAbove {
		printSuggestion(stdout, totalTenths)
	}

	return exitOK
}

func printReport(stdout io.Writer, totals map[string]*packageTotal, totalTenths int) {
	names := make([]string, 0, len(totals))
	for name := range totals {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		entry := totals[name]
		if entry.statements == 0 {
			continue
		}
		tenths := entry.covered * 1000 / entry.statements
		emit(stdout, "package %s %.1f (%d/%d)\n", name, float64(tenths)/10, entry.covered, entry.statements)
	}
	emit(stdout, "total %.1f\n", float64(totalTenths)/10)
}

func printSuggestion(stdout io.Writer, totalTenths int) {
	suggested := max(totalTenths-suggestionSlackTenths, 0)
	emit(stdout, "suggested floor %.1f\n", float64(suggested)/10)
}

func sumTotals(totals map[string]*packageTotal) (statements, covered int) {
	for _, entry := range totals {
		statements += entry.statements
		covered += entry.covered
	}

	return statements, covered
}

func reportProblems(stderr io.Writer, problems []string) {
	for i, problem := range problems {
		if i == maxReportedErrors {
			emit(stderr, "... and %d more\n", len(problems)-maxReportedErrors)

			break
		}
		emit(stderr, "error: %s\n", problem)
	}
}

// loadProfile reads the profile at filePath and returns its per-package totals.
func loadProfile(filePath, module string) (map[string]*packageTotal, error) {
	file, err := os.Open(filePath) //nolint:gosec // the path is the tool's own -profile argument
	if err != nil {
		return nil, fmt.Errorf("open profile: %w", err)
	}
	defer func() {
		// A read-only handle: a failed close loses nothing.
		_ = file.Close()
	}()

	totals, err := parseProfile(file, module)
	if err != nil {
		return nil, fmt.Errorf("profile %s: %w", filePath, err)
	}

	return totals, nil
}

// parseProfile parses a textfmt profile and returns the per-package totals of the packages
// that are not excluded. A block that appears more than once counts once and is covered when
// any occurrence has a count above zero. Every malformed line is reported together.
func parseProfile(reader io.Reader, module string) (map[string]*packageTotal, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	if !scanner.Scan() {
		err := scanner.Err()
		if err != nil {
			return nil, fmt.Errorf("read: %w", err)
		}

		return nil, errors.New("empty profile, want a mode line")
	}
	modeLine := strings.TrimSpace(scanner.Text())
	isMode := modeLine == "mode: set" || modeLine == "mode: count" || modeLine == "mode: atomic"
	if !isMode {
		return nil, fmt.Errorf("first line %q is not a mode line", echo(modeLine))
	}

	blocks := make(map[string]*block)
	var problems []string
	lineNumber := 1
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		key, parsed, err := parseBlockLine(line, module)
		if err != nil {
			problems = append(problems, fmt.Sprintf("line %d: %v", lineNumber, err))

			continue
		}
		existing, seen := blocks[key]
		if !seen {
			blocks[key] = &parsed

			continue
		}
		existing.covered = existing.covered || parsed.covered
	}
	if err := scanner.Err(); err != nil {
		problems = append(problems, fmt.Sprintf("read: %v", err))
	}
	if len(problems) > 0 {
		return nil, joinProblems(problems)
	}

	return aggregate(blocks), nil
}

func joinProblems(problems []string) error {
	shown := problems
	if len(shown) > maxReportedErrors {
		shown = shown[:maxReportedErrors]
	}
	collected := make([]error, 0, len(shown)+1)
	for _, problem := range shown {
		collected = append(collected, errors.New(problem))
	}
	if len(problems) > len(shown) {
		collected = append(collected, fmt.Errorf("and %d more", len(problems)-len(shown)))
	}

	return errors.Join(collected...) //nolint:wrapcheck // joins this function's own errors
}

func aggregate(blocks map[string]*block) map[string]*packageTotal {
	totals := make(map[string]*packageTotal)
	for _, entry := range blocks {
		if isExcluded(entry.pkg) {
			continue
		}
		total, ok := totals[entry.pkg]
		if !ok {
			total = &packageTotal{}
			totals[entry.pkg] = total
		}
		total.statements += entry.statements
		if entry.covered {
			total.covered += entry.statements
		}
	}

	return totals
}

// parseBlockLine parses "<file>:<start>.<col>,<end>.<col> <numStmts> <count>". The returned
// key identifies the block across duplicates.
func parseBlockLine(line, module string) (string, block, error) {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return "", block{}, fmt.Errorf("want 3 fields, got %d", len(fields))
	}
	location := fields[0]
	colon := strings.LastIndex(location, ":")
	if colon <= 0 || colon == len(location)-1 {
		return "", block{}, errors.New("missing file:range location")
	}
	file := location[:colon]

	statements, err := strconv.Atoi(fields[1])
	if err != nil || statements < 0 {
		return "", block{}, fmt.Errorf("bad statement count %q", echo(fields[1]))
	}
	count, err := strconv.ParseUint(fields[2], 10, 64)
	if err != nil {
		return "", block{}, fmt.Errorf("bad execution count %q", echo(fields[2]))
	}

	parsed := block{
		pkg:        packageOf(file, module),
		statements: statements,
		covered:    count > 0,
	}

	return location, parsed, nil
}

// packageOf returns the import path of the package that holds file, relative to module when
// the file lies inside it.
func packageOf(file, module string) string {
	dir := path.Dir(file)
	relative, inside := strings.CutPrefix(dir, module+"/")
	if inside {
		return relative
	}
	if dir == module {
		return "."
	}

	return dir
}

func isExcluded(pkg string) bool {
	for _, excluded := range excludedPackages() {
		isSame := pkg == excluded
		isBelow := strings.HasPrefix(pkg, excluded+"/")
		if isSame || isBelow {
			return true
		}
	}

	return false
}

// readFloor reads a floor file: one number from 0 to 100, surrounded by optional whitespace.
func readFloor(filePath string) (float64, error) {
	content, err := os.ReadFile(filePath) //nolint:gosec // the path is the tool's own argument
	if err != nil {
		return 0, fmt.Errorf("read floor: %w", err)
	}

	floor, err := parseFloor(string(content))
	if err != nil {
		return 0, fmt.Errorf("floor file %s: %w", filePath, err)
	}

	return floor, nil
}

// parseFloor accepts plain decimal notation only (digits with at most one dot), so forms
// strconv would take, such as "NaN", "Inf", "1e1" or "0x10", are rejected.
// A leading UTF-8 byte order mark, which some Windows editors write, is ignored.
func parseFloor(text string) (float64, error) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
	isPlainDecimal := trimmed != "" && strings.Count(trimmed, ".") <= 1 &&
		strings.Trim(trimmed, "0123456789.") == "" && trimmed != "."
	if !isPlainDecimal {
		return 0, fmt.Errorf("%q is not a number from 0 to 100", echo(trimmed))
	}
	value, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number from 0 to 100", echo(trimmed))
	}
	if value > 100 {
		return 0, fmt.Errorf("%q is above 100", echo(trimmed))
	}

	return value, nil
}

// echo keeps echoed input short so a hostile file cannot flood the output.
func echo(text string) string {
	if len(text) <= maxEchoBytes {
		return text
	}

	return text[:maxEchoBytes] + "..."
}
