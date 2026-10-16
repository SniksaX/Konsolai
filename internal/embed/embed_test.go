package embed

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestEmbedBatchesAndCachesDim(t *testing.T) {
	var calls atomic.Int32
	var lastN int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" {
			t.Errorf("path %s", r.URL.Path)
		}
		var body struct {
			Inputs    []string `json:"inputs"`
			Normalize bool     `json:"normalize"`
			Truncate  bool     `json:"truncate"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if !body.Normalize || !body.Truncate {
			t.Errorf("flags %+v", body)
		}
		if len(body.Inputs) > 32 {
			t.Errorf("batch %d", len(body.Inputs))
		}
		lastN = len(body.Inputs)
		calls.Add(1)
		vecs := make([][]float32, len(body.Inputs))
		for i := range vecs {
			vecs[i] = []float32{1, 0, 0}
		}
		_ = json.NewEncoder(w).Encode(vecs)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "")
	texts := make([]string, 33)
	for i := range texts {
		texts[i] = "t"
	}
	got, err := c.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 33 || c.Dim() != 3 {
		t.Fatalf("n=%d dim=%d calls=%d", len(got), c.Dim(), calls.Load())
	}
	if calls.Load() != 2 || lastN != 1 {
		t.Fatalf("calls=%d last=%d", calls.Load(), lastN)
	}
}

func TestEmbedDimensionMismatch(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			_ = json.NewEncoder(w).Encode([][]float32{{1, 0}})
			return
		}
		_ = json.NewEncoder(w).Encode([][]float32{{1, 0, 0}})
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "")
	if _, err := c.Embed(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	_, err := c.Embed(context.Background(), []string{"b"})
	if err == nil || !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("got %v", err)
	}
}

func TestEmbedRetries5xx(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(503)
			_, _ = io.WriteString(w, "busy")
			return
		}
		_ = json.NewEncoder(w).Encode([][]float32{{0.2, 0.8}})
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "")
	got, err := c.Embed(context.Background(), []string{"x"})
	if err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 || len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("n=%d got %+v", n.Load(), got)
	}
}

func TestRerankParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rerank" {
			t.Errorf("path %s", r.URL.Path)
		}
		var body struct {
			Query string   `json:"query"`
			Texts []string `json:"texts"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Query != "q" || len(body.Texts) != 2 {
			t.Errorf("%+v", body)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"index": 1, "score": 0.9},
			{"index": 0, "score": 0.2},
		})
	}))
	t.Cleanup(srv.Close)
	c := New("", srv.URL)
	got, err := c.Rerank(context.Background(), "q", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Index != 1 || got[0].Score != 0.9 || got[0].Text != "b" {
		t.Fatalf("%+v", got)
	}
}

func TestRerankUnreachable(t *testing.T) {
	c := New("", "http://127.0.0.1:1")
	_, err := c.Rerank(context.Background(), "q", []string{"a"})
	if err == nil {
		t.Fatal("want error")
	}
}
