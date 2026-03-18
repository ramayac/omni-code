package embedder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaEmbedder(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embeddings" {
			t.Errorf("expected path /api/embeddings, got %s", r.URL.Path)
		}

		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		prompt := req["prompt"].(string)

		var emb []float32
		if prompt == "hello" {
			emb = []float32{0.1, 0.2, 0.3}
		} else {
			emb = []float32{0.4, 0.5, 0.6}
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"embedding": emb,
		})
	}))
	defer ts.Close()

	e := &OllamaEmbedder{
		BaseURL: ts.URL,
		Model:   "test-model",
	}

	embeddings, err := e.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(embeddings) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(embeddings))
	}
	if embeddings[0][0] != 0.1 || embeddings[1][0] != 0.4 {
		t.Errorf("unexpected embeddings: %v", embeddings)
	}
}

func TestOpenAIEmbedder(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("expected path /v1/embeddings, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing or incorrect Authorization header: %s", r.Header.Get("Authorization"))
		}

		resp := openAIEmbedResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{Embedding: []float32{1, 2, 3}, Index: 0},
				{Embedding: []float32{4, 5, 6}, Index: 1},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	e := &OpenAIEmbedder{
		BaseURL: ts.URL,
		Model:   "test-model",
		APIKey:  "test-key",
	}

	embeddings, err := e.Embed(context.Background(), []string{"one", "two"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(embeddings) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(embeddings))
	}
	if embeddings[0][0] != 1 || embeddings[1][0] != 4 {
		t.Errorf("unexpected embeddings: %v", embeddings)
	}
}

func TestNewEmbedder(t *testing.T) {
	e, err := NewEmbedder("chroma-default", "", "", "")
	if err != nil || e != nil {
		t.Errorf("expected nil/nil for chroma-default")
	}

	e, err = NewEmbedder("ollama", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.(*OllamaEmbedder); !ok {
		t.Errorf("expected OllamaEmbedder")
	}

	e, err = NewEmbedder("openai", "", "", "key")
	if err != nil {
		t.Fatal(err)
	}
	if oe, ok := e.(*OpenAIEmbedder); !ok || oe.APIKey != "key" {
		t.Errorf("expected OpenAIEmbedder with key")
	}
}
