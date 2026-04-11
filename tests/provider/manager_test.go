package provider_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"spettro/internal/provider"
)

func TestManagerSend_UsesFantasyForOpenAICompatibleTextRequests(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-test",
			"object": "chat.completion",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "hello from fantasy",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 20,
				"total_tokens":      30,
			},
		})
	}))
	t.Cleanup(server.Close)

	pm := provider.NewManager()
	pm.AddLocalModels([]provider.Model{{Provider: server.URL, Name: "test-model", Local: true}})

	resp, err := pm.Send(context.Background(), server.URL, "test-model", provider.Request{
		Prompt: "say hello",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if resp.Content != "hello from fantasy" {
		t.Fatalf("expected fantasy response, got %q", resp.Content)
	}
	if resp.EstimatedTokens != 30 {
		t.Fatalf("expected total tokens 30, got %d", resp.EstimatedTokens)
	}
}

func TestManagerSend_FallsBackToLegacyCompletionModels(t *testing.T) {
	t.Parallel()

	var (
		mu              sync.Mutex
		chatCalls       int
		completionCalls int
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch r.URL.Path {
		case "/v1/chat/completions":
			chatCalls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"message": "This is not a chat model and not supported in the v1/chat/completions endpoint. Did you mean to use v1/completions?",
				},
			})
		case "/v1/completions":
			completionCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "cmpl-test",
				"object": "text_completion",
				"choices": []map[string]any{
					{
						"index": 0,
						"text":  "hello from legacy completions",
					},
				},
				"usage": map[string]any{
					"prompt_tokens":     8,
					"completion_tokens": 6,
					"total_tokens":      14,
				},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	pm := provider.NewManager()
	pm.AddLocalModels([]provider.Model{{Provider: server.URL, Name: "legacy-model", Local: true}})

	resp, err := pm.Send(context.Background(), server.URL, "legacy-model", provider.Request{
		Prompt: "say hello",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if resp.Content != "hello from legacy completions" {
		t.Fatalf("expected legacy completion fallback, got %q", resp.Content)
	}

	mu.Lock()
	defer mu.Unlock()
	if chatCalls < 2 {
		t.Fatalf("expected at least two chat attempts before fallback, got %d", chatCalls)
	}
	if completionCalls != 1 {
		t.Fatalf("expected one completions fallback request, got %d", completionCalls)
	}
}
