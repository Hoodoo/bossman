package parse

import (
	"regexp"
	"strings"
)

// Activity names are stable API values. Rules are ordered: a test command
// wrapped by a build tool is testing, for example, rather than implementation.
const (
	ActivityPlanning       = "planning"
	ActivityDocumentation  = "documentation"
	ActivityTesting        = "testing"
	ActivityImplementation = "implementation"
	ActivitySourceControl  = "source-control"
	ActivityInspection     = "inspection"
	ActivityEnvironment    = "environment"
	ActivityOther          = "other"
)

var activityRules = []struct {
	name string
	re   *regexp.Regexp
}{
	{ActivityPlanning, commandPattern(`kata`)},
	{ActivityDocumentation, commandPattern(`owcli`)},
	{ActivityTesting, regexp.MustCompile(`(?i)(^|[;&|()\n]\s*|\s)(go\s+(test|vet)\b|pytest\b|python\S*\s+-m\s+pytest\b|cargo\s+test\b|(?:npm|pnpm|yarn)\s+(?:run\s+)?test\b|make\s+(?:\S*test\S*)\b|golangci-lint\b|eslint\b|ruff\b|shellcheck\b)`)},
	{ActivityImplementation, regexp.MustCompile(`(?i)(^|[;&|()\n]\s*|\s)(gofmt\b|go\s+(fmt|build|generate|install)\b|cargo\s+(build|fmt)\b|(?:npm|pnpm|yarn)\s+run\s+build\b|tsc\b|make\s+(build|all)\b)`)},
	{ActivitySourceControl, commandPattern(`git|gh`)},
	{ActivityInspection, commandPattern(`rg|grep|sed|awk|find|fd|ls|cat|head|tail|wc|pwd|tree|stat|diff`)},
	{ActivityEnvironment, regexp.MustCompile(`(?i)(^|[;&|()\n]\s*|\s)((?:npm|pnpm|yarn)\s+(install|add|ci)\b|go\s+mod\b|cargo\s+(add|install|fetch)\b|pip\S*\s+install\b|apt(?:-get)?\b|brew\b|curl\b|wget\b|docker\b|kubectl\b)`)},
}

var shellCommandWrapper = regexp.MustCompile(`(?i)^\s*(?:/\S*/)?(?:ba|z|fi)?sh\s+-\S*c\s+["'](.*)["']\s*$`)

func commandPattern(names string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(^|[;&|()\n]\s*|\s)(` + names + `)\b`)
}

// ClassifyCommand assigns one coarse activity to a shell invocation.
func ClassifyCommand(command string) string {
	command = strings.TrimSpace(command)
	if match := shellCommandWrapper.FindStringSubmatch(command); match != nil {
		command = match[1]
	}
	for _, rule := range activityRules {
		if rule.re.MatchString(command) {
			return rule.name
		}
	}
	return ActivityOther
}
