package agentlog

import "testing"

func TestRedactorDefaultKeysMasked(t *testing.T) {
	r := NewRedactor(true, nil)
	if !r.IsSensitive("password") {
		t.Fatal("password should be sensitive")
	}
	if !r.IsSensitive("api_key") {
		t.Fatal("api_key should be sensitive")
	}
	if !r.IsSensitive("accessToken") {
		t.Fatal("accessToken (camelCase) should be sensitive")
	}
	if !r.IsSensitive("AUTH") {
		t.Fatal("AUTH (case-insensitive) should be sensitive")
	}
	if !r.IsSensitive("user.password") {
		t.Fatal("user.password tail 'password' should match (path tail)")
	}
	if r.IsSensitive("username") {
		t.Fatal("username is not a default sensitive key")
	}
}

func TestRedactorPayloadRecursiveMask(t *testing.T) {
	r := NewRedactor(true, nil)
	in := map[string]interface{}{
		"password": "hunter2",
		"profile": map[string]interface{}{
			"token": "abc123",
			"name":  "bob",
		},
		"tags": []interface{}{
			map[string]interface{}{"secret": "x"},
			"plain",
		},
	}
	out := r.redactValue(in).(map[string]interface{})
	if out["password"] != MaskString {
		t.Fatalf("password not masked: %v", out["password"])
	}
	profile := out["profile"].(map[string]interface{})
	if profile["token"] != MaskString {
		t.Fatalf("nested token not masked: %v", profile["token"])
	}
	if profile["name"] != "bob" {
		t.Fatalf("nested non-sensitive name changed: %v", profile["name"])
	}
	tags := out["tags"].([]interface{})
	tag0 := tags[0].(map[string]interface{})
	if tag0["secret"] != MaskString {
		t.Fatalf("array element secret not masked: %v", tag0["secret"])
	}
	if tags[1] != "plain" {
		t.Fatalf("plain array element changed: %v", tags[1])
	}
}

func TestRedactorFreeTextRegex(t *testing.T) {
	r := NewRedactor(true, nil)
	got := r.redactFreeText(`POST /login password=hunter2 token=abc`)
	if contains(got, "hunter2") {
		t.Fatalf("free-text password value not masked: %s", got)
	}
	if contains(got, "abc") {
		t.Fatalf("free-text token value not masked: %s", got)
	}

	got2 := r.redactFreeText("Authorization: Bearer eyJabc.def.ghi")
	if contains(got2, "eyJabc.def.ghi") {
		t.Fatalf("Bearer token not masked: %s", got2)
	}
	if !contains(got2, MaskString) {
		t.Fatalf("Bearer token not replaced: %s", got2)
	}
}

func TestRedactorAppendOnly(t *testing.T) {
	r := NewRedactor(true, nil)
	// defaults still present
	if !r.IsSensitive("password") {
		t.Fatal("default key removed after append")
	}
	r.Append([]string{"mysecret"})
	if !r.IsSensitive("mysecret") {
		t.Fatal("appended key not sensitive")
	}
	// defaults must remain
	if !r.IsSensitive("password") {
		t.Fatal("default key removed after append")
	}
}

func TestRedactDisable(t *testing.T) {
	// Redaction is disabled at the logger level; the redactor still masks when
	// invoked, but emit() skips it entirely when redactEnabled is false.
	mem := NewMemorySink()
	logger := NewLogger("svc", mem)
	logger.redactEnabled = false
	logger.Info("login", WithPayload(map[string]interface{}{"password": "hunter2"}))
	lines := mem.Lines()
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if contains(lines[0], MaskString) {
		t.Fatalf("redaction disabled but value was masked: %s", lines[0])
	}
}
