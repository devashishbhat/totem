package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/openai/openai-go/v3"
)

// openAIClient resends a recorded Chat Completions request unchanged. The
// request is posted as the raw object Totem recorded, rather than rebuilt from
// typed parameters, so a replay asks exactly the question the agent asked.
type openAIClient struct {
	client openai.Client
	log    *slog.Logger
}

// chatResponse is the slice of the Chat Completions response replay needs.
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

func (c *openAIClient) Complete(ctx context.Context, payload map[string]any) (Output, error) {
	var res chatResponse
	if err := c.client.Post(ctx, "chat/completions", payload, &res); err != nil {
		return Output{}, fmt.Errorf("chat completion: %s", describe(err))
	}
	if len(res.Choices) == 0 {
		return Output{}, fmt.Errorf("the model returned no choices")
	}
	msg := res.Choices[0].Message
	if len(msg.ToolCalls) > 0 {
		call := msg.ToolCalls[0]
		args := json.RawMessage(call.Function.Arguments)
		if !json.Valid(args) {
			args, _ = json.Marshal(call.Function.Arguments)
		}
		return Output{Tool: call.Function.Name, Args: args}, nil
	}
	return Output{Content: msg.Content}, nil
}

// describe turns a provider error into something a person can act on.
//
// The SDK's own Error() is deliberately a bare status line, because request
// URLs and provider fields can carry secrets. The API's own message cannot:
// it is what says "rate limit reached ... try again in 28m" rather than just
// "429 Too Many Requests", which is the difference between a user fixing
// their account and a user filing a bug.
func describe(err error) string {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return err.Error()
	}
	switch {
	case apiErr.Message != "" && apiErr.Code != "":
		return fmt.Sprintf("%d %s: %s", apiErr.StatusCode, apiErr.Code, apiErr.Message)
	case apiErr.Message != "":
		return fmt.Sprintf("%d: %s", apiErr.StatusCode, apiErr.Message)
	default:
		return apiErr.Error()
	}
}
