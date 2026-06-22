// Package logtail is an optional helper that streams a docker container's logs
// to stderr with generic, framework-agnostic colorization. It is opt-in: the
// proxy only tails containers named via the --logs flag.
package logtail

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vichugofsl/rieproxy/internal/ansi"
)

var reGinStatus = regexp.MustCompile(`\|\s*(\d{3})\s*\|`)

// Tail streams stdout+stderr from a docker container, colorizing each line and
// writing it to stderr. It blocks until ctx is cancelled or the stream ends.
// When expandEscapes is true, literal "\n"/"\t" sequences in a line are turned
// into real newlines/tabs before printing (handy for slog/JSON logs that embed
// escaped SQL or stack traces).
func Tail(ctx context.Context, container string, expandEscapes bool) {
	prefix := ansi.Magenta + ansi.Bold + "[" + container + "]" + ansi.Reset + " "
	tail(ctx, container, func(line string) {
		if expandEscapes {
			line = ExpandEscapes(line)
		}
		fmt.Fprintln(os.Stderr, prefix+Colorize(line))
	})
}

// ExpandEscapes converts literal "\n" and "\t" two-character sequences into
// real newlines and tabs. Structured loggers often emit embedded SQL, stack
// traces, or JSON with escaped whitespace that is unreadable on a single line.
func ExpandEscapes(s string) string {
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\t`, "\t")
	return s
}

func tail(ctx context.Context, container string, process func(string)) {
	// Only show logs from ~now onward, not the container's whole history.
	since := strconv.FormatInt(time.Now().Add(-10*time.Second).Unix(), 10)
	cmd := exec.CommandContext(ctx, "docker", "logs", "-f", "--since", since, container)

	// Merge container stdout and stderr into one pipe so we see all output.
	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s[rieproxy]%s could not create pipe for %s logs: %v\n", ansi.Red, ansi.Reset, container, err)
		return
	}
	cmd.Stdout = w
	cmd.Stderr = w

	if err := cmd.Start(); err != nil {
		_ = w.Close()
		_ = r.Close()
		fmt.Fprintf(os.Stderr, "%s[rieproxy]%s could not tail %s (is docker available?): %v\n", ansi.Red, ansi.Reset, container, err)
		return
	}
	_ = w.Close() // parent does not write; close so the reader sees EOF when the child exits

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	for scanner.Scan() {
		process(scanner.Text())
	}
	_ = r.Close()
	_ = cmd.Wait()
}

// Colorize applies best-effort, project-agnostic colorization to a single log
// line by recognizing widely-used formats: Go slog text ("level=..."), Gin
// request/debug logs, AWS Lambda lifecycle markers (START/END/REPORT), and the
// RIE's internal "(rapid)" lines. These are heuristics, not requirements — any
// line that doesn't match (logs from another language, framework, or format) is
// returned unchanged, so tailing works for any RIE-backed function.
func Colorize(line string) string {
	switch {
	case strings.Contains(line, "level=ERROR"):
		return ansi.Red + ansi.Bold + line + ansi.Reset
	case strings.Contains(line, "level=WARN"):
		return ansi.Yellow + line + ansi.Reset
	case strings.Contains(line, "level=INFO"):
		return ansi.Green + line + ansi.Reset
	case strings.Contains(line, "level=DEBUG"):
		return ansi.Cyan + line + ansi.Reset
	}

	switch {
	case strings.HasPrefix(line, "[GIN]"):
		return colorizeGin(line)
	case strings.HasPrefix(line, "[GIN-debug]"):
		return ansi.Gray + line + ansi.Reset
	case strings.HasPrefix(line, "START "), strings.HasPrefix(line, "END "), strings.HasPrefix(line, "REPORT "):
		return ansi.Magenta + line + ansi.Reset
	case strings.Contains(line, "(rapid)"):
		// RIE internal logs — dim them.
		return ansi.Gray + line + ansi.Reset
	}
	return line
}

func colorizeGin(line string) string {
	m := reGinStatus.FindStringSubmatch(line)
	if len(m) < 2 {
		return line
	}
	switch m[1][0] {
	case '2':
		return ansi.Green + line + ansi.Reset
	case '4':
		return ansi.Yellow + line + ansi.Reset
	case '5':
		return ansi.Red + ansi.Bold + line + ansi.Reset
	}
	return line
}
