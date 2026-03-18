// Package embedder provides an interface and implementations for computing
// text embeddings used to index code chunks.
package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Embedder computes dense vector embeddings for a batch of text inputs.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// NewEmbedder constructs an Embedder based on the backend name.
// Supported values: "chroma-default", "ollama", "openai", "openai-compatible".
// A nil return means chroma-default is selected (ChromaDB handles embeddings natively).
func NewEmbedder(backend, model, url, apiKey string) (Embedder, error) {
	switch backend {
	case "", "chroma-default":
		return nil, nil // signal caller to use ChromaDB built-in EF
	case "ollama":
		if url == "" {
			url = "http://localhost:11434"
		}
		if model == "" {
			model = "nomic-embed-text"
		}
		return &OllamaEmbedder{BaseURL: url, Model: model}, nil
	case "openai":
		if url == "" {
			url = "https://api.openai.com"
		}
		if model == "" {
			model = "text-embedding-3-small"
		}
		return &OpenAIEmbedder{BaseURL: url, Model: model, APIKey: apiKey}, nil
	case "openai-compatible":
		if model == "" {
			model = "text-embedding-3-small"
		}
		return &OpenAIEmbedder{BaseURL: url, Model: model, APIKey: apiKey}, nil
	default:
		return nil, fmt.Errorf("unknown embedding backend %q", backend)
	}
}

// OllamaEmbedder calls the Ollama /api/embeddings endpoint.
type OllamaEmbedder struct {
	BaseURL string
	Model   string
	Client  *http.Client
}

func (e *OllamaEmbedder) httpClient() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return http.DefaultClient
}

type ollamaEmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbedResponse struct {
	Embedding []float32 `json:"embedding"`
}

// Embed calls Ollama once per text (the API is single-text only).
func (e *OllamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		emb, err := e.embedOne(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("ollama embed[%d]: %w", i, err)
		}
		results[i] = emb
	}
	return results, nil
}

func (e *OllamaEmbedder) embedOne(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(ollamaEmbedRequest{Model: e.Model, Prompt: text})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.BaseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, b)
	}

	var result ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.Embedding, nil
}

// OpenAIEmbedder calls an OpenAI-compatible /v1/embeddings endpoint.
type OpenAIEmbedder struct {
	BaseURL string
	Model   string
	APIKey  string
	Client  *http.Client
}

func (e *OpenAIEmbedder) httpClient() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return http.DefaultClient
}

type openAIEmbedRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type openAIEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

// Embed sends all texts in a single batch request.
func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(openAIEmbedRequest{Input: texts, Model: e.Model})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.BaseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.APIKey)
	}

	resp, err := e.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai returned %d: %s", resp.StatusCode, b)
	}

	var result openAIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	embeddings := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index < len(embeddings) {
			embeddings[d.Index] = d.Embedding
		}
	}
	return embeddings, nil
}
