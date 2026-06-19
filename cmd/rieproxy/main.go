// Command rieproxy is a tiny, zero-dependency local HTTP front end for the AWS
// Lambda Runtime Interface Emulator (RIE). It turns each HTTP request into an
// API Gateway event (payload format 1.0 or 2.0), POSTs it to the RIE, and
// writes the Lambda response back — letting you curl/browse a Lambda running
// locally without Node, Python, or the Serverless Framework.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/vichugofsl/rieproxy/internal/ansi"
	"github.com/vichugofsl/rieproxy/internal/proxy"
)

// version is overridden at build time via -ldflags "-X main.version=..." (how
// GoReleaser stamps releases). When unset, resolveVersion falls back to the
// module version recorded by `go install <pkg>@<version>`.
var version = "dev"

// resolveVersion reports the build version, preferring the ldflags value, then
// the module version from the build info (set by `go install ...@vX.Y.Z`), then
// "dev" for plain `go build`/`go run`.
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return version
}

// stringList collects a repeatable flag into a slice.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func main() {
	var tail stringList
	flag.Var(&tail, "logs", "docker container to tail logs from (repeatable)")

	port := flag.String("port", env("RIEPROXY_PORT", "3000"), "local HTTP server port")
	target := flag.String("target", env("RIEPROXY_TARGET", "http://127.0.0.1:8080"), "Lambda RIE endpoint (host:port or URL)")
	function := flag.String("function", env("RIEPROXY_FUNCTION", "function"), "Lambda function name in the RIE invoke path")
	payload := flag.String("payload", env("RIEPROXY_PAYLOAD", "1.0"), `API Gateway payload version: "1.0" or "2.0"`)
	timeout := flag.Duration("timeout", envDuration("RIEPROXY_TIMEOUT", 300*time.Second), "per-invocation timeout")
	cors := flag.Bool("cors", true, "send permissive CORS headers (local-dev convenience)")
	noColor := flag.Bool("no-color", false, "disable ANSI colors in output")
	expandEscapes := flag.Bool("expand-escapes", false, `expand literal \n and \t in tailed --logs into real newlines/tabs`)
	restart := flag.String("restart-container", env("RIEPROXY_RESTART_CONTAINER", ""), "docker container to restart if an invocation fails (optional)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("rieproxy", resolveVersion())
		return
	}
	if *noColor {
		ansi.Disable()
	}

	cfg := proxy.Config{
		Port:             *port,
		Target:           normalizeTarget(*target),
		Function:         *function,
		PayloadVersion:   *payload,
		Timeout:          *timeout,
		CORS:             *cors,
		RestartContainer: *restart,
		TailContainers:   tail,
		ExpandEscapes:    *expandEscapes,
	}
	if err := proxy.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "rieproxy:", err)
		os.Exit(1)
	}
}

// normalizeTarget accepts either a bare host:port (like bref's TARGET) or a
// full URL and returns a URL.
func normalizeTarget(t string) string {
	if !strings.HasPrefix(t, "http://") && !strings.HasPrefix(t, "https://") {
		return "http://" + t
	}
	return t
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
