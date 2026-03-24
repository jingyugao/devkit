package durationutil

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		{input: "", want: 0},
		{input: "0", want: 0},
		{input: "3d", want: 72 * time.Hour},
		{input: "2w", want: 14 * 24 * time.Hour},
		{input: "1h30m", want: 90 * time.Minute},
		{input: "bad", wantErr: true},
	}
	for _, tt := range tests {
		got, err := Parse(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("Parse(%q) expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Fatalf("Parse(%q) returned error: %v", tt.input, err)
		}
		if got != tt.want {
			t.Fatalf("Parse(%q)=%s want %s", tt.input, got, tt.want)
		}
	}
}
