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

func TestHandler_GETWithCloudflareAndHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://debug.example.com/webhook/test?tag=b&tag=a&env=prod", nil)
	req.RemoteAddr = "172.30.0.15:60370"
	req.Header.Set("Cf-Connecting-Ip", "2401:7400:c805:5628::1c7a")
	req.Header.Set("Cf-Ipcountry", "SG")
	req.Header.Set("Cf-Ray", "a4bf61b14cd5237c-HKG")
	req.Header.Set("X-Forwarded-For", "172.71.210.192")
	req.Header.Set("Via", "2.0 Caddy")
	req.Header.Add("X-Single", "only-one")
	req.Header.Add("X-Multi", "2")
	req.Header.Add("X-Multi", "1")

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
	if res.ClientIP != "2401:7400:c805:5628::1c7a" {
		t.Errorf("ClientIP = %q, want 2401:7400:c805:5628::1c7a", res.ClientIP)
	}
	if res.Country != "SG" {
		t.Errorf("Country = %q, want SG", res.Country)
	}
	if res.CfRay != "a4bf61b14cd5237c-HKG" {
		t.Errorf("CfRay = %q, want a4bf61b14cd5237c-HKG", res.CfRay)
	}
	wantRoute := "2401:7400:c805:5628::1c7a (SG, IPv6) -> Cloudflare HKG (172.71.210.192) -> 2.0 Caddy (172.30.0.15:60370)"
	if res.Route != wantRoute {
		t.Errorf("Route = %q, want %q", res.Route, wantRoute)
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

	if got := res.Headers["X-Single"]; got != "only-one" {
		t.Errorf("X-Single = %#v, want %q", got, "only-one")
	}
	wantMulti := []any{"1", "2"}
	if got := res.Headers["X-Multi"]; !reflect.DeepEqual(got, wantMulti) {
		t.Errorf("X-Multi = %#v, want %#v", got, wantMulti)
	}
}

func TestHandler_ClientIPFallbackToXFF(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://debug.example.com/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")

	rec := httptest.NewRecorder()
	handler(rec, req)

	var res Result
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if res.ClientIP != "203.0.113.9" {
		t.Errorf("ClientIP = %q, want 203.0.113.9", res.ClientIP)
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

	wantBody := map[string]any{"hello": "world", "n": float64(42)}
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

	wantCookies := []any{"authelia_session=[redacted]", "cf_clearance=[redacted]"}
	if got := res.Headers["Cookie"]; !reflect.DeepEqual(got, wantCookies) {
		t.Errorf("Cookie header = %#v, want %#v", got, wantCookies)
	}
	if got := res.Headers["Authorization"]; got != "[redacted]" {
		t.Errorf("Authorization header = %#v, want [redacted]", got)
	}
	if got := res.Headers["Proxy-Authorization"]; got != "[redacted]" {
		t.Errorf("Proxy-Authorization header = %#v, want [redacted]", got)
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
