package domain

import "testing"

func TestDefaultMaxOutputTokens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		context  int64
		provided int64
		want     int64
	}{
		{name: "equal to context", context: 1_048_576, provided: 1_048_576, want: 128_000},
		{name: "missing output", context: 1_048_576, provided: 0, want: 128_000},
		{name: "plausible output kept", context: 1_048_576, provided: 32_000, want: 32_000},
		{name: "small window", context: 64_000, provided: 0, want: 64_000},
		{name: "unknown context keeps provided", context: 0, provided: 16_000, want: 16_000},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := DefaultMaxOutputTokens(testCase.context, testCase.provided); got != testCase.want {
				t.Fatalf("DefaultMaxOutputTokens(%d, %d) = %d, want %d", testCase.context, testCase.provided, got, testCase.want)
			}
		})
	}
}

func TestDefaultAutoCompactWindow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		context   int64
		maxOutput int64
		want      int64
	}{
		{name: "one million", context: 1_000_000, maxOutput: 32_000, want: 800_000},
		{name: "small window reserves output", context: 200_000, maxOutput: 32_000, want: 152_000},
		{name: "no output budget", context: 200_000, want: 160_000},
		{name: "unknown window", context: 0, maxOutput: 32_000, want: 0},
		{name: "output larger than reserve", context: 100_000, maxOutput: 90_000, want: 80_000},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := DefaultAutoCompactWindow(testCase.context, testCase.maxOutput); got != testCase.want {
				t.Fatalf("DefaultAutoCompactWindow(%d, %d) = %d, want %d", testCase.context, testCase.maxOutput, got, testCase.want)
			}
		})
	}
}
