package main

import (
	"encoding/json"
	"io"
	"net/http"
)

type Header struct {
	Key    string   `json:"key,omitempty"`
	Values []string `json:"values,omitempty"`
}

type Result struct {
	Method  string      `json:"method,omitempty"`
	Headers []Header    `json:"header,omitempty"`
	Body    interface{} `json:"body,omitempty"`
}

func handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	res := Result{
		Method:  r.Method,
		Headers: []Header{},
	}

	for key, values := range r.Header {
		header := Header{
			Key:    key,
			Values: []string{},
		}
		for _, value := range values {
			header.Values = append(header.Values, value)
		}
		res.Headers = append(res.Headers, header)
	}

	var requestBody interface{}
	err := json.NewDecoder(r.Body).Decode(&requestBody)
	if err == io.EOF {
		requestBody = nil // create empty map
	} else if err != nil {
		return
	}
	res.Body = requestBody

	j, err := json.Marshal(res)

	_, err = w.Write(j)
	if err != nil {
		return
	}
}

func main() {

	http.HandleFunc("/", handler)
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		return
	}
}
