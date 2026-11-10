package capitime

// NamesFeature reports whether line carries the feature name in any spelling, the check that makes
// the service reject a generated line. Tests use it as their oracle so they never restate the rule.
func NamesFeature(line string) bool {
	return namesFeature(line)
}
