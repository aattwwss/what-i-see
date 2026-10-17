package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func findHeader(headers Headers, key string) ([]string, bool) {
	for _, h := range headers {
		if h.Key == key {
			return h.Values, true
		}
	}
	return nil, false
}

func TestHandler_GETWithQueryAndSortedHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://debug.example.com/webhook/test?tag=b&tag=a&env=prod", nil)
	req.Header.Add("X-Zebra", "z")
	req.Header.Add("X-Alpha", "2")
	req.Header.Add("X-Alpha", "1")

	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var res Result
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if res.Method != http.MethodGet {
		t.Errorf("Method = %q, want GET", res.Method)
	}
	if res.Host != "debug.example.com" {
		t.Errorf("Host = %q, want debug.example.com", res.Host)
	}
	if res.Path != "/webhook/test" {
		t.Errorf("Path = %q, want /webhook/test", res.Path)
	}
	wantQuery := map[string][]string{
		"env": {"prod"},
		"tag": {"b", "a"},
	}
	if !reflect.DeepEqual(res.Query, wantQuery) {
		t.Errorf("Query = %v, want %v", res.Query, wantQuery)
	}
	if res.Body != nil {
		t.Errorf("Body = %v, want nil", res.Body)
	}

	if len(res.Headers) != 2 || res.Headers[0].Key != "X-Alpha" || res.Headers[1].Key != "X-Zebra" {
		t.Errorf("Headers not sorted by key: %+v", res.Headers)
	}
	if want := []string{"1", "2"}; !reflect.DeepEqual(res.Headers[0].Values, want) {
		t.Errorf("X-Alpha values = %v, want %v", res.Headers[0].Values, want)
	}
}

func TestHandler_JSONBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://debug.example.com/api", strings.NewReader(`{"hello":"world","n":42}`))
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var res Result
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	wantBody := map[string]interface{}{"hello": "world", "n": float64(42)}
	if !reflect.DeepEqual(res.Body, wantBody) {
		t.Errorf("Body = %#v, want %#v", res.Body, wantBody)
	}
}

func TestHandler_RawBodyFallback(t *testing.T) {
	raw := "foo=bar&baz=123"
	req := httptest.NewRequest(http.MethodPost, "http://debug.example.com/form", strings.NewReader(raw))
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(`\u0026`)) {
		t.Errorf("response escaped '&' as \\u0026: %s", rec.Body.String())
	}

	var res Result
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if res.Body != raw {
		t.Errorf("Body = %#v, want %q", res.Body, raw)
	}
}

func TestHandler_RedactsCredentials(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://debug.example.com/", nil)
	req.Header.Set("Cookie", "cf_clearance=secret1; authelia_session=secret2")
	req.Header.Set("Authorization", "Bearer top-secret-token")
	req.Header.Set("Proxy-Authorization", "Basic dXNlcjpwYXNz")

	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	raw := rec.Body.String()
	for _, secret := range []string{"secret1", "secret2", "top-secret-token", "dXNlcjpwYXNz"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("response leaked secret %q: %s", secret, raw)
		}
	}

	var res Result
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	wantCookies := []string{"authelia_session=[redacted]", "cf_clearance=[redacted]"}
	if got, ok := findHeader(res.Headers, "Cookie"); !ok || !reflect.DeepEqual(got, wantCookies) {
		t.Errorf("Cookie header = %v, want %v", got, wantCookies)
	}
	if got, ok := findHeader(res.Headers, "Authorization"); !ok || !reflect.DeepEqual(got, []string{"[redacted]"}) {
		t.Errorf("Authorization header = %v, want [redacted]", got)
	}
	if got, ok := findHeader(res.Headers, "Proxy-Authorization"); !ok || !reflect.DeepEqual(got, []string{"[redacted]"}) {
		t.Errorf("Proxy-Authorization header = %v, want [redacted]", got)
	}
}

func TestHandler_BodyTooLarge(t *testing.T) {
	oversized := strings.Repeat("a", maxBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "http://debug.example.com/", strings.NewReader(oversized))
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}
