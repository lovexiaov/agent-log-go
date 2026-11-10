package agentlog

import "testing"

func TestAIRequest(t *testing.T) {
	mem := NewMemorySink()
	logger := NewLogger("svc", mem)
	logger.RecordAIRequest("gpt-4o", "summarize this", nil)
	line := mem.Lines()[0]
	if !contains(line, `"event_type":"ai_request_initiated"`) {
		t.Fatalf("expected ai_request_initiated: %s", line)
	}
	if !contains(line, `"level":"info"`) {
		t.Fatalf("expected info level: %s", line)
	}
	if !contains(line, `"agent_auto":false`) {
		t.Fatalf("AI events must be agent_auto=false: %s", line)
	}
	if !contains(line, `"prompt_text":"summarize this"`) {
		t.Fatalf("expected prompt_text payload: %s", line)
	}
	if contains(line, "promptText") {
		t.Fatalf("camelCase alias leaked: %s", line)
	}
}

func TestAIResponseTokens(t *testing.T) {
	mem := NewMemorySink()
	logger := NewLogger("svc", mem)
	logger.RecordAIResponse("gpt-4o", 530, map[string]interface{}{
		"prompt_tokens": 320, "completion_tokens": 210, "request_id": "r1",
	})
	line := mem.Lines()[0]
	if !contains(line, `"event_type":"ai_response_received"`) {
		t.Fatalf("expected ai_response_received: %s", line)
	}
	if !contains(line, `"total_tokens":530`) {
		t.Fatalf("expected total_tokens=530: %s", line)
	}
	if !contains(line, `"request_id":"r1"`) {
		t.Fatalf("expected request_id: %s", line)
	}
}

func TestAIToolCallAndResult(t *testing.T) {
	mem := NewMemorySink()
	logger := NewLogger("svc", mem)
	logger.RecordAIToolCall("search", map[string]interface{}{"q": "x"}, nil)
	if !contains(mem.Lines()[0], `"event_type":"ai_tool_call_initiated"`) {
		t.Fatal("expected tool call initiated")
	}
	logger.RecordAIToolResult("search", true, map[string]interface{}{"tool_call_latency_ms": 12})
	line := mem.Lines()[1]
	if !contains(line, `"event_type":"ai_tool_call_completed"`) {
		t.Fatal("expected tool call completed")
	}
	if !contains(line, `"ok":true`) {
		t.Fatalf("expected ok=true: %s", line)
	}
}

func TestAIGuardrailWarn(t *testing.T) {
	mem := NewMemorySink()
	logger := NewLogger("svc", mem)
	logger.RecordAIGuardrail("toxicity", "blocked", nil)
	line := mem.Lines()[0]
	if !contains(line, `"event_type":"ai_guardrail_triggered"`) {
		t.Fatal("expected guardrail triggered")
	}
	if !contains(line, `"level":"warn"`) {
		t.Fatalf("guardrail must be warn: %s", line)
	}
	if !contains(line, `"guardrail_type":"toxicity"`) {
		t.Fatalf("expected guardrail_type: %s", line)
	}
}

func TestAIErrorVocab(t *testing.T) {
	mem := NewMemorySink()
	logger := NewLogger("svc", mem)
	logger.RecordAIError("RateLimitError", "429 too many requests", map[string]interface{}{"request_id": "r9"})
	line := mem.Lines()[0]
	if !contains(line, `"event_type":"ai_error"`) {
		t.Fatal("expected ai_error")
	}
	if !contains(line, `"level":"error"`) {
		t.Fatalf("ai_error must be error level: %s", line)
	}
	if !contains(line, `"agent_auto":false`) {
		t.Fatalf("ai_error must be agent_auto=false: %s", line)
	}
	if !contains(line, `"exception":"RateLimitError"`) {
		t.Fatalf("expected exception: %s", line)
	}
	if !contains(line, `"mechanism":"handled"`) {
		t.Fatalf("expected mechanism=handled: %s", line)
	}
}

func TestAIPayloadSnakeCase(t *testing.T) {
	mem := NewMemorySink()
	logger := NewLogger("svc", mem)
	logger.RecordAIResponse("gpt-4o", 10, nil)
	line := mem.Lines()[0]
	if contains(line, "promptText") || contains(line, "tokenUsage") {
		t.Fatalf("camelCase alias leaked: %s", line)
	}
}

// The facade must expose the AI helpers just like the other five ends.
func TestAgentLogFacadeAIEvents(t *testing.T) {
	previous := shared
	defer func() { shared = previous }()
	mem := NewMemorySink()
	shared = NewLogger("svc", mem)
	AgentLog.RecordAIResponse("gpt-4o", 42, map[string]interface{}{"request_id": "r1"})
	line := mem.Lines()[0]
	if !contains(line, `"event_type":"ai_response_received"`) ||
		!contains(line, `"total_tokens":42`) ||
		!contains(line, `"request_id":"r1"`) {
		t.Fatalf("facade delegate did not emit a standard AI event: %s", line)
	}
}
