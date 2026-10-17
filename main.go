package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"time"
)

const maxBodyBytes = 1 << 20 // 1 MiB

type Header struct {
	Key    string   `json:"key,omitempty"`
	Values []string `json:"values,omitempty"`
}

type Headers []Header

func (h Headers) Len() int           { return len(h) }
func (h Headers) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h Headers) Less(i, j int) bool { return h[i].Key < h[j].Key }

type Result struct {
	Method     string              `json:"method,omitempty"`
	Proto      string              `json:"proto,omitempty"`
	Host       string              `json:"host,omitempty"`
	Path       string              `json:"path,omitempty"`
	Query      map[string][]string `json:"query,omitempty"`
	RemoteAddr string              `json:"remote_addr,omitempty"`
	Headers    Headers             `json:"header,omitempty"`
	Body       interface{}         `json:"body,omitempty"`
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

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
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

func handler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	defer func() {
		slog.Info("request",
			"method", r.Method,
			"host", r.Host,
			"path", r.URL.Path,
			"client_ip", r.Header.Get("Cf-Connecting-Ip"),
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

	var requestBody interface{}
	if len(rawBody) > 0 {
		if err := json.Unmarshal(rawBody, &requestBody); err != nil {
			requestBody = string(rawBody)
		}
	}

	var query map[string][]string
	if q := r.URL.Query(); len(q) > 0 {
		query = q
	}

	res := Result{
		Method:     r.Method,
		Proto:      r.Proto,
		Host:       r.Host,
		Path:       r.URL.Path,
		Query:      query,
		RemoteAddr: r.RemoteAddr,
		Headers:    Headers{},
		Body:       requestBody,
	}

	for key, values := range r.Header {
		header := Header{
			Key: key,
		}
		switch key {
		case "Cookie":
			for _, c := range r.Cookies() {
				header.Values = append(header.Values, c.Name+"=[redacted]")
			}
			if len(header.Values) == 0 {
				header.Values = []string{"[redacted]"}
			}
			sort.Strings(header.Values)
		case "Authorization", "Proxy-Authorization":
			header.Values = []string{"[redacted]"}
		default:
			header.Values = append(header.Values, values...)
			sort.Strings(header.Values)
		}
		res.Headers = append(res.Headers, header)
	}

	sort.Sort(res.Headers)

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
