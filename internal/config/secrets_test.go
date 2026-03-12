package config

import "testing"

func TestEncryptedAPIKeyRoundTrip(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("SPETTRO_MASTER_KEY", "test-master-key")

	if err := SaveAPIKey("anthropic", "sk-ant-secret"); err != nil {
		t.Fatalf("save encrypted key: %v", err)
	}
	if err := SaveAPIKey("openai-compatible", "sk-openai-secret"); err != nil {
		t.Fatalf("save encrypted key: %v", err)
	}

	keys, err := LoadAPIKeys()
	if err != nil {
		t.Fatalf("load encrypted keys: %v", err)
	}
	if got := keys["anthropic"]; got != "sk-ant-secret" {
		t.Fatalf("expected anthropic key, got %q", got)
	}
	if got := keys["openai-compatible"]; got != "sk-openai-secret" {
		t.Fatalf("expected openai key, got %q", got)
	}
}
