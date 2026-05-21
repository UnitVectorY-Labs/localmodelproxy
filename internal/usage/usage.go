package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
)

type TokenUsage struct {
	InputTokens    int64
	OutputTokens   int64
	ThinkingTokens int64
	CachedTokens   int64
	TotalTokens    int64
}

type payload struct {
	Model string      `json:"model"`
	Usage *usageShape `json:"usage"`
}

type usageShape struct {
	PromptTokens            int64        `json:"prompt_tokens"`
	CompletionTokens        int64        `json:"completion_tokens"`
	TotalTokens             int64        `json:"total_tokens"`
	InputTokens             int64        `json:"input_tokens"`
	OutputTokens            int64        `json:"output_tokens"`
	PromptTokensDetails     tokenDetails `json:"prompt_tokens_details"`
	CompletionTokensDetails tokenDetails `json:"completion_tokens_details"`
	ExtraProperties         extraProps   `json:"extra_properties"`
	UsageMetadata           googleUsage  `json:"usageMetadata"`
	UsageMetadataSnake      googleUsage  `json:"usage_metadata"`
}

type tokenDetails struct {
	CachedTokens    int64 `json:"cached_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

type extraProps struct {
	Google googleUsage `json:"google"`
}

type googleUsage struct {
	PromptTokenCount     int64 `json:"promptTokenCount"`
	CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	TotalTokenCount      int64 `json:"totalTokenCount"`
	ThoughtsTokenCount   int64 `json:"thoughtsTokenCount"`
	CachedContentTokens  int64 `json:"cachedContentTokenCount"`

	PromptTokens     int64 `json:"prompt_tokens"`
	CandidatesTokens int64 `json:"candidates_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	ThinkingTokens   int64 `json:"thinking_tokens"`
	ThoughtsTokens   int64 `json:"thoughts_tokens"`
	CachedTokens     int64 `json:"cached_tokens"`
}

func ParseJSON(body []byte) (TokenUsage, string, bool) {
	var p payload
	if err := json.Unmarshal(body, &p); err != nil || p.Usage == nil {
		return TokenUsage{}, p.Model, false
	}
	return normalize(*p.Usage), p.Model, true
}

func ParseModel(body []byte) string {
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return ""
	}
	return p.Model
}

func ParseSSE(data []byte) (TokenUsage, string, bool) {
	var total TokenUsage
	var model string
	var found bool

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if value == "" || value == "[DONE]" {
			continue
		}

		usage, eventModel, ok := ParseJSON([]byte(value))
		if eventModel != "" {
			model = eventModel
		}
		if ok {
			total = usage
			found = true
		}
	}

	return total, model, found
}

func normalize(u usageShape) TokenUsage {
	input := firstNonZero(
		u.PromptTokens,
		u.InputTokens,
		u.UsageMetadata.PromptTokenCount,
		u.UsageMetadataSnake.PromptTokenCount,
		u.ExtraProperties.Google.PromptTokenCount,
		u.ExtraProperties.Google.PromptTokens,
	)
	output := firstNonZero(
		u.CompletionTokens,
		u.OutputTokens,
		u.UsageMetadata.CandidatesTokenCount,
		u.UsageMetadataSnake.CandidatesTokenCount,
		u.ExtraProperties.Google.CandidatesTokenCount,
		u.ExtraProperties.Google.CandidatesTokens,
	)
	thinking := firstNonZero(
		u.CompletionTokensDetails.ReasoningTokens,
		u.UsageMetadata.ThoughtsTokenCount,
		u.UsageMetadataSnake.ThoughtsTokenCount,
		u.ExtraProperties.Google.ThoughtsTokenCount,
		u.ExtraProperties.Google.ThinkingTokens,
		u.ExtraProperties.Google.ThoughtsTokens,
	)
	cached := firstNonZero(
		u.PromptTokensDetails.CachedTokens,
		u.UsageMetadata.CachedContentTokens,
		u.UsageMetadataSnake.CachedContentTokens,
		u.ExtraProperties.Google.CachedContentTokens,
		u.ExtraProperties.Google.CachedTokens,
	)
	total := firstNonZero(
		u.TotalTokens,
		u.UsageMetadata.TotalTokenCount,
		u.UsageMetadataSnake.TotalTokenCount,
		u.ExtraProperties.Google.TotalTokenCount,
		u.ExtraProperties.Google.TotalTokens,
	)
	if total == 0 {
		total = input + output
	}
	if cached > input {
		input = 0
	} else {
		input -= cached
	}
	return TokenUsage{
		InputTokens:    input,
		OutputTokens:   output,
		ThinkingTokens: thinking,
		CachedTokens:   cached,
		TotalTokens:    total,
	}
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
