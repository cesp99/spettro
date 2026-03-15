package provider

import (
	"context"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	anthropicOption "github.com/anthropics/anthropic-sdk-go/option"
	openai "github.com/openai/openai-go/v3"
	openaiOption "github.com/openai/openai-go/v3/option"
)

type OpenAICompatibleAdapter struct {
	APIKey  string
	BaseURL string
}

func (a OpenAICompatibleAdapter) Send(ctx context.Context, model string, req Request) (Response, error) {
	opts := []openaiOption.RequestOption{openaiOption.WithAPIKey(a.APIKey)}
	if a.BaseURL != "" {
		opts = append(opts, openaiOption.WithBaseURL(a.BaseURL))
	}
	client := openai.NewClient(opts...)

	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage(req.Prompt),
		},
	})
	if err != nil {
		if strings.Contains(err.Error(), "not a chat model") || strings.Contains(err.Error(), "v1/completions") {
			return a.sendLegacyCompletion(ctx, client, model, req)
		}
		return Response{}, err
	}

	content := ""
	if len(completion.Choices) > 0 {
		content = completion.Choices[0].Message.Content
	}
	return Response{
		Content:         content,
		EstimatedTokens: int(completion.Usage.TotalTokens),
	}, nil
}

func (a OpenAICompatibleAdapter) sendLegacyCompletion(ctx context.Context, client openai.Client, model string, req Request) (Response, error) {
	completion, err := client.Completions.New(ctx, openai.CompletionNewParams{
		Model:  openai.CompletionNewParamsModel(model),
		Prompt: openai.CompletionNewParamsPromptUnion{OfString: openai.String(req.Prompt)},
	})
	if err != nil {
		return Response{}, err
	}
	content := ""
	if len(completion.Choices) > 0 {
		content = completion.Choices[0].Text
	}
	return Response{
		Content:         content,
		EstimatedTokens: int(completion.Usage.TotalTokens),
	}, nil
}

type AnthropicAdapter struct {
	APIKey string
}

func (a AnthropicAdapter) Send(ctx context.Context, model string, req Request) (Response, error) {
	client := anthropic.NewClient(anthropicOption.WithAPIKey(a.APIKey))

	msg, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: 8096,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(req.Prompt)),
		},
	})
	if err != nil {
		return Response{}, err
	}

	var sb strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			sb.WriteString(block.AsText().Text)
		}
	}
	return Response{
		Content:         sb.String(),
		EstimatedTokens: int(msg.Usage.InputTokens + msg.Usage.OutputTokens),
	}, nil
}
