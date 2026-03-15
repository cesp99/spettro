package provider

import "context"

type Model struct {
	Provider     string
	ProviderName string
	Name         string
	DisplayName  string
	Vision       bool
	Reasoning    bool
	ToolCall     bool
	Context      int
	Status       string
	EnvKey       string
	Local        bool
}

type ProviderInfo struct {
	ID   string
	Name string
	Env  string
}

type Request struct {
	Prompt      string
	Images      []string
	RequireFast bool
	MaxTokens   int
}

type Response struct {
	Content         string
	EstimatedTokens int
	Provider        string
	Model           string
}

type Adapter interface {
	Send(context.Context, string, Request) (Response, error)
}
