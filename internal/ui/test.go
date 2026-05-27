package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// executeTest sends a test chat completion request to the local proxy and returns the result.
func executeTest(addr, model, systemMsg, userMsg string) testResultMsg {
	reqBody := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": systemMsg},
			{"role": "user", "content": userMsg},
		},
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return testResultMsg{model: model, err: fmt.Errorf("marshal request: %w", err)}
	}

	client := &http.Client{Timeout: 60 * time.Second}
	url := fmt.Sprintf("http://%s/v1/chat/completions", addr)
	resp, err := client.Post(url, "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		return testResultMsg{model: model, err: fmt.Errorf("request failed: %w", err)}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return testResultMsg{model: model, err: fmt.Errorf("read response: %w", err)}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return testResultMsg{model: model, err: fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncateString(string(respBody), 200))}
	}

	// Parse the response to extract the content
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return testResultMsg{model: model, err: fmt.Errorf("parse response: %w", err)}
	}
	if result.Error != nil {
		return testResultMsg{model: model, err: fmt.Errorf("API error: %s", result.Error.Message)}
	}
	if len(result.Choices) == 0 {
		return testResultMsg{model: model, err: fmt.Errorf("no choices in response")}
	}
	content := result.Choices[0].Message.Content
	if content == "" {
		content = "(empty response)"
	}
	return testResultMsg{model: model, content: content}
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
