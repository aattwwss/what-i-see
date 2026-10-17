package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

const maxBodyBytes = 1 << 20 // 1 MiB

type Result struct {
	Method     string              `json:"method,omitempty"`
	Proto      string              `json:"proto,omitempty"`
	Host       string              `json:"host,omitempty"`
	Path       string              `json:"path,omitempty"`
	Query      map[string][]string `json:"query,omitempty"`
	ClientIP   string              `json:"client_ip,omitempty"`
	Country    string              `json:"country,omitempty"`
	CfRay      string              `json:"cf_ray,omitempty"`
	RemoteAddr string              `json:"remote_addr,omitempty"`
	Headers    map[string]any      `json:"headers,omitempty"`
	Body       any                 `json:"body,omitempty"`
}

// statusRecorder captures the status code for request logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		slog.Error("marshal response failed", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(buf.Bytes()); err != nil {
		slog.Error("write response failed", "err", err)
	}
}

func collapseValues(vals []string) any {
	slices.Sort(vals)
	if len(vals) == 1 {
		return vals[0]
	}
	return vals
}

func clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("Cf-Connecting-Ip")); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		if ip := strings.TrimSpace(first); ip != "" {
			return ip
		}
	}
	return ""
}

func handler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	cip := clientIP(r)
	defer func() {
		slog.Info("request",
			"method", r.Method,
			"host", r.Host,
			"path", r.URL.Path,
			"client_ip", cip,
			"cf_ray", r.Header.Get("Cf-Ray"),
			"remote", r.RemoteAddr,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	r.Body = http.MaxBytesReader(rec, r.Body, maxBodyBytes)
	rawBody, err := io.ReadAll(r.Body)
	var maxErr *http.MaxBytesError
	switch {
	case errors.As(err, &maxErr):
		writeJSON(rec, http.StatusRequestEntityTooLarge,
			map[string]string{"error": "request body too large"})
		return
	case err != nil:
		writeJSON(rec, http.StatusBadRequest,
			map[string]string{"error": "failed to read request body"})
		return
	}

	var requestBody any
	if len(rawBody) > 0 {
		if err := json.Unmarshal(rawBody, &requestBody); err != nil {
			requestBody = string(rawBody)
		}
	}

	var query map[string][]string
	if q := r.URL.Query(); len(q) > 0 {
		query = q
	}

	headers := make(map[string]any, len(r.Header))
	for key, values := range r.Header {
		switch key {
		case "Cookie":
			var redacted []string
			for _, c := range r.Cookies() {
				redacted = append(redacted, c.Name+"=[redacted]")
			}
			if len(redacted) == 0 {
				redacted = []string{"[redacted]"}
			}
			headers[key] = collapseValues(redacted)
		case "Authorization", "Proxy-Authorization":
			headers[key] = "[redacted]"
		default:
			headers[key] = collapseValues(slices.Clone(values))
		}
	}

	res := Result{
		Method:     r.Method,
		Proto:      r.Proto,
		Host:       r.Host,
		Path:       r.URL.Path,
		Query:      query,
		ClientIP:   cip,
		Country:    r.Header.Get("Cf-Ipcountry"),
		CfRay:      r.Header.Get("Cf-Ray"),
		RemoteAddr: r.RemoteAddr,
		Headers:    headers,
		Body:       requestBody,
	}

	writeJSON(rec, http.StatusOK, res)
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	mux := http.NewServeMux()
	mux.HandleFunc("/", handler)

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	slog.Info("listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server failed", "err", err)
		os.Exit(1)
	}
}
