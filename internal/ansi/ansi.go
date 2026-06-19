// Package ansi holds the terminal color codes used across rieproxy.
//
// The codes are package-level variables (not constants) so that Disable can
// blank them all out for --no-color or non-TTY output, after which every
// call site naturally emits plain text with no further branching.
package ansi

// Color / style codes. Blanked by Disable.
var (
	Reset   = "\033[0m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	Gray    = "\033[90m"
	Bold    = "\033[1m"
)

// Disable blanks every code so output contains no ANSI escapes.
func Disable() {
	Reset, Red, Green, Yellow, Blue, Magenta, Cyan, Gray, Bold = "", "", "", "", "", "", "", "", ""
}
