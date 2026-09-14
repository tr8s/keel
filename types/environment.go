package types

import (
	"strings"
	"unicode/utf8"
)

// KeelEnvironmentAnnotation - optional display label of the environment a workload runs in, ie: prod or staging.
// Slack approval and deploy notice messages show it next to the workload; it does not change grouping.
const KeelEnvironmentAnnotation = "keel.sh/environment"

// MaxEnvironmentLength - the longest environment label kept, longer labels are cut
const MaxEnvironmentLength = 32

// ParseEnvironment - the environment label of a resource (keel.sh/environment, annotation first, then label), with
// its whitespace collapsed and cut to MaxEnvironmentLength characters. Empty when unset.
func ParseEnvironment(labels, annotations map[string]string) string {
	value, ok := annotations[KeelEnvironmentAnnotation]
	if !ok {
		value = labels[KeelEnvironmentAnnotation]
	}
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) > MaxEnvironmentLength {
		value = strings.TrimSpace(string([]rune(value)[:MaxEnvironmentLength]))
	}
	return value
}
