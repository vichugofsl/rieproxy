package logtail

import (
	"strings"
	"testing"

	"github.com/vichugofsl/rieproxy/internal/ansi"
)

func TestColorize_Slog(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{`time=2024-01-01 level=INFO msg=started`, ansi.Green},
		{`time=2024-01-01 level=ERROR msg=failed`, ansi.Red},
		{`time=2024-01-01 level=WARN msg=slow`, ansi.Yellow},
		{`time=2024-01-01 level=DEBUG msg=trace`, ansi.Cyan},
	}
	for _, tc := range cases {
		out := Colorize(tc.line)
		if !strings.Contains(out, tc.want) || !strings.Contains(out, ansi.Reset) {
			t.Errorf("Colorize(%q) = %q, want color %q", tc.line, out, tc.want)
		}
	}
}

func TestColorize_Gin(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{`[GIN] 2024/01/01 | 200 | 1ms | GET /api`, ansi.Green},
		{`[GIN] 2024/01/01 | 404 | 1ms | GET /missing`, ansi.Yellow},
		{`[GIN] 2024/01/01 | 500 | 1ms | POST /api`, ansi.Red},
	}
	for _, tc := range cases {
		if out := Colorize(tc.line); !strings.Contains(out, tc.want) {
			t.Errorf("Colorize(%q) = %q, want color %q", tc.line, out, tc.want)
		}
	}
}

func TestColorize_Lifecycle(t *testing.T) {
	for _, prefix := range []string{"START ", "END ", "REPORT "} {
		if out := Colorize(prefix + "RequestId: abc123"); !strings.Contains(out, ansi.Magenta) {
			t.Errorf("Colorize(%q...) missing magenta", prefix)
		}
	}
}

func TestColorize_GinDebugAndRapid(t *testing.T) {
	if out := Colorize("[GIN-debug] GET /api --> handler"); !strings.Contains(out, ansi.Gray) {
		t.Error("GIN-debug should be gray")
	}
	if out := Colorize("time=2024-01-01 (rapid) invoking handler"); !strings.Contains(out, ansi.Gray) {
		t.Error("(rapid) line should be gray")
	}
}

func TestColorize_Unrecognized(t *testing.T) {
	line := "just some plain text"
	if out := Colorize(line); out != line {
		t.Errorf("Colorize(%q) = %q, want unchanged", line, out)
	}
}
