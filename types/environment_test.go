package types

import (
	"strings"
	"testing"
)

func TestParseEnvironment(t *testing.T) {
	tests := []struct {
		name        string
		labels      map[string]string
		annotations map[string]string
		want        string
	}{
		{name: "unset"},
		{name: "annotation is trimmed", annotations: map[string]string{KeelEnvironmentAnnotation: "  prod \n"}, want: "prod"},
		{name: "label", labels: map[string]string{KeelEnvironmentAnnotation: "staging"}, want: "staging"},
		{
			name:        "annotation before label",
			labels:      map[string]string{KeelEnvironmentAnnotation: "staging"},
			annotations: map[string]string{KeelEnvironmentAnnotation: "prod"},
			want:        "prod",
		},
		{name: "whitespace is collapsed", annotations: map[string]string{KeelEnvironmentAnnotation: "eu\tprod\n 2"}, want: "eu prod 2"},
		{name: "cut to 32 characters", annotations: map[string]string{KeelEnvironmentAnnotation: strings.Repeat("é", 40)}, want: strings.Repeat("é", 32)},
		{name: "no trailing space after the cut", annotations: map[string]string{KeelEnvironmentAnnotation: strings.Repeat("a", 31) + " tail"}, want: strings.Repeat("a", 31)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseEnvironment(tt.labels, tt.annotations); got != tt.want {
				t.Errorf("ParseEnvironment() = %q, want %q", got, tt.want)
			}
		})
	}
}
