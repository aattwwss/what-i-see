package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
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
	Method  string      `json:"method,omitempty"`
	Headers Headers     `json:"header,omitempty"`
	Body    interface{} `json:"body,omitempty"`
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
	j, err := json.Marshal(v)
	if err != nil {
		slog.Error("marshal response failed", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(j); err != nil {
		slog.Error("write response failed", "err", err)
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	defer func() {
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	r.Body = http.MaxBytesReader(rec, r.Body, maxBodyBytes)

	var requestBody interface{}
	err := json.NewDecoder(r.Body).Decode(&requestBody)
	var maxErr *http.MaxBytesError
	switch {
	case errors.Is(err, io.EOF):
		requestBody = nil // empty body
	case errors.As(err, &maxErr):
		writeJSON(rec, http.StatusRequestEntityTooLarge,
			map[string]string{"error": "request body too large"})
		return
	case err != nil:
		writeJSON(rec, http.StatusBadRequest,
			map[string]string{"error": "request body is not valid JSON"})
		return
	}

	res := Result{
		Method:  r.Method,
		Headers: Headers{},
	}

	for key, values := range r.Header {
		header := Header{
			Key: key,
		}
		switch key {
		case "Cookie":
			// Redact credentials: count cookies, never echo their values.
			n := 0
			for _, v := range values {
				n += strings.Count(v, ";") + 1
			}
			header.Values = []string{fmt.Sprintf("[redacted %d cookie(s)]", n)}
		case "Authorization":
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
