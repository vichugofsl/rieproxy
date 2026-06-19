package translate

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func TestNew(t *testing.T) {
	for _, v := range []string{"1.0", "1", "v1", "2.0", "2", "v2", "V2"} {
		if _, err := New(v); err != nil {
			t.Errorf("New(%q) unexpected error: %v", v, err)
		}
	}
	if _, err := New("3.0"); err == nil {
		t.Error("New(\"3.0\") expected error, got nil")
	}
}

func TestIsBinaryContentType(t *testing.T) {
	binary := []string{
		"image/png", "image/jpeg", "IMAGE/PNG", "audio/mpeg", "video/mp4",
		"application/octet-stream", "application/zip", "application/pdf",
	}
	for _, ct := range binary {
		if !isBinaryContentType(ct) {
			t.Errorf("expected binary: %q", ct)
		}
	}
	text := []string{"application/json", "text/plain", "text/html", "application/x-www-form-urlencoded", ""}
	for _, ct := range text {
		if isBinaryContentType(ct) {
			t.Errorf("expected non-binary: %q", ct)
		}
	}
}

func TestSourceIP(t *testing.T) {
	cases := map[string]string{
		"10.0.0.1:8080":       "10.0.0.1",
		"[::1]:8080":          "[::1]",
		"192.168.1.100:54000": "192.168.1.100",
		"noport":              "noport",
	}
	for in, want := range cases {
		if got := sourceIP(in); got != want {
			t.Errorf("sourceIP(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- v1 request ---

func TestV1_BuildRequest_GET(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/bibles?lang=eng&page=1&page=2", nil)
	r.RemoteAddr = "192.168.1.1:54321"

	ev, err := buildV1Request(r)
	if err != nil {
		t.Fatal(err)
	}
	if ev.HTTPMethod != http.MethodGet || ev.Path != "/api/bibles" {
		t.Errorf("method/path = %q %q", ev.HTTPMethod, ev.Path)
	}
	if ev.Body != "" || ev.IsBase64Encoded {
		t.Errorf("expected empty unencoded body, got %q b64=%v", ev.Body, ev.IsBase64Encoded)
	}
	if ev.QueryStringParameters["lang"] != "eng" || ev.QueryStringParameters["page"] != "1" {
		t.Errorf("single query params wrong: %v", ev.QueryStringParameters)
	}
	if got := ev.MultiValueQueryStringParameters["page"]; len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Errorf("multi query params wrong: %v", got)
	}
	if ev.RequestContext.Identity.SourceIP != "192.168.1.1" {
		t.Errorf("sourceIP = %q", ev.RequestContext.Identity.SourceIP)
	}
	if ev.RequestContext.Stage != "local" || ev.RequestContext.RequestID == "" {
		t.Errorf("context = %+v", ev.RequestContext)
	}
}

func TestV1_BuildRequest_PostJSON(t *testing.T) {
	body := `{"name":"test"}`
	r := httptest.NewRequest(http.MethodPost, "/api/bibles", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	ev, err := buildV1Request(r)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Body != body || ev.IsBase64Encoded {
		t.Errorf("body = %q b64=%v", ev.Body, ev.IsBase64Encoded)
	}
}

func TestV1_BuildRequest_PostBinary(t *testing.T) {
	raw := []byte{0x89, 0x50, 0x4E, 0x47} // PNG magic
	r := httptest.NewRequest(http.MethodPost, "/api/upload", strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "image/png")

	ev, err := buildV1Request(r)
	if err != nil {
		t.Fatal(err)
	}
	if !ev.IsBase64Encoded {
		t.Fatal("expected base64-encoded body")
	}
	decoded, err := base64.StdEncoding.DecodeString(ev.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(raw) {
		t.Errorf("decoded = %v, want %v", decoded, raw)
	}
}

func TestV1_BuildRequest_Headers(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer token123")
	r.Header.Add("X-Custom", "first")
	r.Header.Add("X-Custom", "second")

	ev, err := buildV1Request(r)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Headers["Authorization"] != "Bearer token123" {
		t.Errorf("auth header = %q", ev.Headers["Authorization"])
	}
	if ev.Headers["X-Custom"] != "first" {
		t.Errorf("single X-Custom = %q", ev.Headers["X-Custom"])
	}
	if got := ev.MultiValueHeaders["X-Custom"]; len(got) != 2 || got[1] != "second" {
		t.Errorf("multi X-Custom = %v", got)
	}
}

// --- v2 request ---

func TestV2_BuildRequest(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/items?q=a&q=b", strings.NewReader(`{"x":1}`))
	r.RemoteAddr = "10.1.2.3:9999"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Add("Cookie", "a=1")
	r.Header.Add("Cookie", "b=2")
	r.Header.Add("X-Multi", "one")
	r.Header.Add("X-Multi", "two")

	ev, err := buildV2Request(r)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Version != "2.0" || ev.RouteKey != "$default" {
		t.Errorf("version/routekey = %q %q", ev.Version, ev.RouteKey)
	}
	if ev.RawPath != "/api/items" || ev.RawQueryString != "q=a&q=b" {
		t.Errorf("rawpath/rawquery = %q %q", ev.RawPath, ev.RawQueryString)
	}
	if ev.RequestContext.HTTP.Method != http.MethodPost || ev.RequestContext.HTTP.SourceIP != "10.1.2.3" {
		t.Errorf("http ctx = %+v", ev.RequestContext.HTTP)
	}
	// v2 folds multi-value headers with commas...
	if ev.Headers["X-Multi"] != "one,two" {
		t.Errorf("X-Multi = %q, want \"one,two\"", ev.Headers["X-Multi"])
	}
	// ...and pulls cookies into a dedicated field, not Headers.
	if len(ev.Cookies) != 2 || ev.Cookies[0] != "a=1" {
		t.Errorf("cookies = %v", ev.Cookies)
	}
	if _, ok := ev.Headers["Cookie"]; ok {
		t.Error("Cookie should not be in Headers for v2")
	}
	if ev.QueryStringParameters["q"] != "a,b" {
		t.Errorf("v2 query q = %q, want \"a,b\"", ev.QueryStringParameters["q"])
	}
}

// --- response writing (shared shape, per-version structs) ---

func TestV1_WriteResponse(t *testing.T) {
	resp := events.APIGatewayProxyResponse{
		StatusCode:        201,
		Headers:           map[string]string{"Content-Type": "application/json"},
		MultiValueHeaders: map[string][]string{"Set-Cookie": {"a=1", "b=2"}},
		Body:              `{"ok":true}`,
	}
	raw, _ := json.Marshal(resp)
	rec := httptest.NewRecorder()

	status, err := V1{}.WriteResponse(rec, raw)
	if err != nil {
		t.Fatal(err)
	}
	if status != 201 || rec.Code != 201 {
		t.Errorf("status = %d / %d", status, rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("content-type = %q", rec.Header().Get("Content-Type"))
	}
	if got := rec.Header().Values("Set-Cookie"); len(got) != 2 {
		t.Errorf("set-cookie = %v", got)
	}
	if rec.Body.String() != `{"ok":true}` {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestV1_WriteResponse_Base64(t *testing.T) {
	raw := []byte{0x01, 0x02, 0x03}
	resp := events.APIGatewayProxyResponse{
		StatusCode:      200,
		Body:            base64.StdEncoding.EncodeToString(raw),
		IsBase64Encoded: true,
	}
	b, _ := json.Marshal(resp)
	rec := httptest.NewRecorder()
	if _, err := (V1{}).WriteResponse(rec, b); err != nil {
		t.Fatal(err)
	}
	if rec.Body.String() != string(raw) {
		t.Errorf("decoded body = %v, want %v", rec.Body.Bytes(), raw)
	}
}

func TestV1_WriteResponse_DefaultStatus(t *testing.T) {
	b, _ := json.Marshal(events.APIGatewayProxyResponse{Body: "hi"}) // StatusCode 0
	rec := httptest.NewRecorder()
	status, err := V1{}.WriteResponse(rec, b)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
}

func TestV2_WriteResponse_Cookies(t *testing.T) {
	resp := events.APIGatewayV2HTTPResponse{
		StatusCode: 200,
		Headers:    map[string]string{"Content-Type": "text/plain"},
		Cookies:    []string{"session=abc", "theme=dark"},
		Body:       "hello",
	}
	b, _ := json.Marshal(resp)
	rec := httptest.NewRecorder()

	status, err := V2{}.WriteResponse(rec, b)
	if err != nil {
		t.Fatal(err)
	}
	if status != 200 {
		t.Errorf("status = %d", status)
	}
	if got := rec.Header().Values("Set-Cookie"); len(got) != 2 || got[0] != "session=abc" {
		t.Errorf("set-cookie = %v", got)
	}
	if rec.Body.String() != "hello" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// BuildPayload should produce valid JSON that round-trips to the event struct.
func TestBuildPayload_RoundTrips(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)

	p1, err := V1{}.BuildPayload(r)
	if err != nil {
		t.Fatal(err)
	}
	var ev1 events.APIGatewayProxyRequest
	if err := json.Unmarshal(p1, &ev1); err != nil || ev1.Path != "/x" {
		t.Errorf("v1 payload bad: %v path=%q", err, ev1.Path)
	}

	r2 := httptest.NewRequest(http.MethodGet, "/y", nil)
	p2, err := V2{}.BuildPayload(r2)
	if err != nil {
		t.Fatal(err)
	}
	var ev2 events.APIGatewayV2HTTPRequest
	if err := json.Unmarshal(p2, &ev2); err != nil || ev2.RawPath != "/y" {
		t.Errorf("v2 payload bad: %v rawpath=%q", err, ev2.RawPath)
	}
}
