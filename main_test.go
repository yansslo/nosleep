package main

import (
	"testing"
	"time"
)

func TestParseHumanDuration(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{input: "30m", want: 30 * time.Minute},
		{input: "1h30m", want: 90 * time.Minute},
		{input: "1d", want: 24 * time.Hour},
		{input: "1.5 days", want: 36 * time.Hour},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := parseHumanDuration(test.input)
			if err != nil {
				t.Fatalf("parseHumanDuration(%q) returned %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("parseHumanDuration(%q) = %v, want %v", test.input, got, test.want)
			}
		})
	}
}

func TestParseHumanDurationRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{"", "tomorrow", "1w"} {
		if _, err := parseHumanDuration(input); err == nil {
			t.Fatalf("parseHumanDuration(%q) unexpectedly succeeded", input)
		}
	}
}
