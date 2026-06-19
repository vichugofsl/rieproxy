// Package translate converts between Go's net/http types and the AWS API
// Gateway event/response JSON that the Lambda Runtime Interface Emulator
// expects. It supports both payload formats:
//
//   - "1.0": REST API / API Gateway proxy events (APIGatewayProxyRequest)
//   - "2.0": HTTP API events (APIGatewayV2HTTPRequest), also used by Lambda
//     Function URLs.
package translate

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

// Translator converts an HTTP request into a Lambda invoke payload and writes
// a Lambda response back to an HTTP client.
type Translator interface {
	// BuildPayload marshals an incoming HTTP request into the JSON event
	// payload to POST to the Lambda RIE invoke endpoint.
	BuildPayload(r *http.Request) ([]byte, error)
	// WriteResponse parses the raw Lambda response JSON and writes it to w,
	// returning the HTTP status code that was written.
	WriteResponse(w http.ResponseWriter, raw []byte) (int, error)
}

// New returns the Translator for the given API Gateway payload version.
// Accepted values: "1.0"/"1"/"v1" and "2.0"/"2"/"v2".
func New(payloadVersion string) (Translator, error) {
	switch strings.ToLower(payloadVersion) {
	case "1.0", "1", "v1":
		return V1{}, nil
	case "2.0", "2", "v2":
		return V2{}, nil
	default:
		return nil, fmt.Errorf("unsupported payload version %q (want \"1.0\" or \"2.0\")", payloadVersion)
	}
}

// --- v1: REST / API Gateway proxy events ---

// V1 translates using the API Gateway proxy (payload format 1.0) event shape.
type V1 struct{}

func (V1) BuildPayload(r *http.Request) ([]byte, error) {
	ev, err := buildV1Request(r)
	if err != nil {
		return nil, err
	}
	return json.Marshal(ev)
}

func buildV1Request(r *http.Request) (events.APIGatewayProxyRequest, error) {
	body, isB64, err := readBody(r)
	if err != nil {
		return events.APIGatewayProxyRequest{}, err
	}

	headers := make(map[string]string, len(r.Header))
	multiHeaders := make(map[string][]string, len(r.Header))
	for k, vals := range r.Header {
		headers[k] = vals[0]
		multiHeaders[k] = vals
	}

	query := make(map[string]string)
	multiQuery := make(map[string][]string)
	for k, vals := range r.URL.Query() {
		query[k] = vals[0]
		multiQuery[k] = vals
	}

	now := time.Now()
	return events.APIGatewayProxyRequest{
		HTTPMethod:                      r.Method,
		Path:                            r.URL.Path,
		Headers:                         headers,
		MultiValueHeaders:               multiHeaders,
		QueryStringParameters:           query,
		MultiValueQueryStringParameters: multiQuery,
		Body:                            body,
		IsBase64Encoded:                 isB64,
		RequestContext: events.APIGatewayProxyRequestContext{
			RequestID:        generateRequestID(),
			Stage:            "local",
			Identity:         events.APIGatewayRequestIdentity{SourceIP: sourceIP(r.RemoteAddr)},
			HTTPMethod:       r.Method,
			Path:             r.URL.Path,
			RequestTimeEpoch: now.UnixMilli(),
		},
	}, nil
}

func (V1) WriteResponse(w http.ResponseWriter, raw []byte) (int, error) {
	var resp events.APIGatewayProxyResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return 0, fmt.Errorf("unmarshaling v1 response: %w", err)
	}
	writeHeaders(w, resp.Headers, resp.MultiValueHeaders)
	status := statusOrDefault(resp.StatusCode)
	w.WriteHeader(status)
	return status, writeBody(w, resp.Body, resp.IsBase64Encoded)
}

// --- v2: HTTP API events ---

// V2 translates using the HTTP API (payload format 2.0) event shape.
type V2 struct{}

func (V2) BuildPayload(r *http.Request) ([]byte, error) {
	ev, err := buildV2Request(r)
	if err != nil {
		return nil, err
	}
	return json.Marshal(ev)
}

func buildV2Request(r *http.Request) (events.APIGatewayV2HTTPRequest, error) {
	body, isB64, err := readBody(r)
	if err != nil {
		return events.APIGatewayV2HTTPRequest{}, err
	}

	// v2 folds multi-value headers into a single comma-joined value and lifts
	// cookies out into a dedicated field.
	headers := make(map[string]string, len(r.Header))
	var cookies []string
	for k, vals := range r.Header {
		if strings.EqualFold(k, "Cookie") {
			cookies = append(cookies, vals...)
			continue
		}
		headers[k] = strings.Join(vals, ",")
	}

	query := make(map[string]string)
	for k, vals := range r.URL.Query() {
		query[k] = strings.Join(vals, ",")
	}

	now := time.Now()
	return events.APIGatewayV2HTTPRequest{
		Version:               "2.0",
		RouteKey:              "$default",
		RawPath:               r.URL.Path,
		RawQueryString:        r.URL.RawQuery,
		Cookies:               cookies,
		Headers:               headers,
		QueryStringParameters: query,
		Body:                  body,
		IsBase64Encoded:       isB64,
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			RouteKey:  "$default",
			Stage:     "$default",
			RequestID: generateRequestID(),
			TimeEpoch: now.UnixMilli(),
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{
				Method:    r.Method,
				Path:      r.URL.Path,
				Protocol:  r.Proto,
				SourceIP:  sourceIP(r.RemoteAddr),
				UserAgent: r.UserAgent(),
			},
		},
	}, nil
}

func (V2) WriteResponse(w http.ResponseWriter, raw []byte) (int, error) {
	var resp events.APIGatewayV2HTTPResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return 0, fmt.Errorf("unmarshaling v2 response: %w", err)
	}
	writeHeaders(w, resp.Headers, resp.MultiValueHeaders)
	for _, c := range resp.Cookies {
		w.Header().Add("Set-Cookie", c)
	}
	status := statusOrDefault(resp.StatusCode)
	w.WriteHeader(status)
	return status, writeBody(w, resp.Body, resp.IsBase64Encoded)
}

// --- shared helpers ---

func readBody(r *http.Request) (body string, isBase64 bool, err error) {
	if r.Body == nil {
		return "", false, nil
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return "", false, fmt.Errorf("reading body: %w", err)
	}
	if len(raw) == 0 {
		return "", false, nil
	}
	if isBinaryContentType(r.Header.Get("Content-Type")) {
		return base64.StdEncoding.EncodeToString(raw), true, nil
	}
	return string(raw), false, nil
}

func writeHeaders(w http.ResponseWriter, headers map[string]string, multi map[string][]string) {
	for k, vals := range multi {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	for k, v := range headers {
		if w.Header().Get(k) == "" {
			w.Header().Set(k, v)
		}
	}
}

func writeBody(w http.ResponseWriter, body string, isBase64 bool) error {
	if isBase64 {
		decoded, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			return fmt.Errorf("decoding base64 response body: %w", err)
		}
		_, err = w.Write(decoded)
		return err
	}
	_, err := io.WriteString(w, body)
	return err
}

func statusOrDefault(code int) int {
	if code == 0 {
		return http.StatusOK
	}
	return code
}

// sourceIP strips the port from a net/http RemoteAddr, leaving the host. For
// IPv6 (e.g. "[::1]:8080") the bracketed host is preserved.
func sourceIP(remoteAddr string) string {
	if idx := strings.LastIndex(remoteAddr, ":"); idx != -1 {
		return remoteAddr[:idx]
	}
	return remoteAddr
}

func generateRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func isBinaryContentType(ct string) bool {
	ct = strings.ToLower(ct)
	for _, prefix := range []string{
		"image/", "audio/", "video/",
		"application/octet-stream",
		"application/zip",
		"application/pdf",
	} {
		if strings.HasPrefix(ct, prefix) {
			return true
		}
	}
	return false
}
