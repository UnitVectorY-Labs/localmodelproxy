package usage

import "testing"

func TestParseJSONUsage(t *testing.T) {
	got, model, ok := ParseJSON([]byte(`{"model":"google/gemini","usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`))
	if !ok {
		t.Fatal("expected usage")
	}
	if model != "google/gemini" || got.InputTokens != 3 || got.OutputTokens != 4 || got.TotalTokens != 7 {
		t.Fatalf("unexpected parse: %#v model=%s", got, model)
	}
}

func TestParseOpenAIDetailUsage(t *testing.T) {
	got, _, ok := ParseJSON([]byte(`{"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":7}}}`))
	if !ok {
		t.Fatal("expected usage")
	}
	if got.InputTokens != 6 || got.OutputTokens != 20 || got.ThinkingTokens != 7 || got.CachedTokens != 4 || got.TotalTokens != 30 {
		t.Fatalf("unexpected parse: %#v", got)
	}
}

func TestParseGoogleStyleUsage(t *testing.T) {
	got, _, ok := ParseJSON([]byte(`{"usage":{"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":22,"thoughtsTokenCount":5,"cachedContentTokenCount":3,"totalTokenCount":38}}}`))
	if !ok {
		t.Fatal("expected usage")
	}
	if got.InputTokens != 8 || got.OutputTokens != 22 || got.ThinkingTokens != 5 || got.CachedTokens != 3 || got.TotalTokens != 38 {
		t.Fatalf("unexpected parse: %#v", got)
	}
}

func TestParseSSEUsage(t *testing.T) {
	body := []byte("data: {\"model\":\"google/gemini\",\"choices\":[]}\n\ndata: {\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":6,\"total_tokens\":11}}\n\ndata: [DONE]\n\n")
	got, model, ok := ParseSSE(body)
	if !ok {
		t.Fatal("expected usage")
	}
	if model != "google/gemini" || got.TotalTokens != 11 {
		t.Fatalf("unexpected parse: %#v model=%s", got, model)
	}
}
