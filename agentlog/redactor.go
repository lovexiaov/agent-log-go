package agentlog

import (
	"regexp"
	"strings"
)

// MaskString is the replacement used for any redacted value.
const MaskString = "***REDACTED***"

// DEFAULT_SENSITIVE_KEYS are the built-in sensitive keys. They can never be
// removed; redact() only ever APPENDS to this list.
var DEFAULT_SENSITIVE_KEYS = []string{
	"password", "passwd", "pwd", "token", "authorization", "auth", "cookie",
	"secret", "apikey", "api_key", "accesstoken", "access_token", "refreshtoken",
	"refresh_token", "credential", "privatekey", "private_key", "ssn", "bearer",
}

// Redactor masks sensitive values by key and in free text. It is append-only:
// the default sensitive keys are always active.
type Redactor struct {
	sensitiveKeys []string
	freeTextRe    *regexp.Regexp
	bearerRe      *regexp.Regexp
}

// NewRedactor builds a redactor. enabled controls whether redaction runs; extra
// keys are appended to the immutable default set.
func NewRedactor(enabled bool, extra []string) *Redactor {
	if !enabled {
		// still build keys so Append works later; enabled is checked at emit time.
		r := &Redactor{sensitiveKeys: append([]string{}, DEFAULT_SENSITIVE_KEYS...)}
		r.sensitiveKeys = append(r.sensitiveKeys, extra...)
		r.rebuildFreeText()
		return r
	}
	r := &Redactor{sensitiveKeys: append([]string{}, DEFAULT_SENSITIVE_KEYS...)}
	r.sensitiveKeys = append(r.sensitiveKeys, extra...)
	r.rebuildFreeText()
	return r
}

// Append adds keys to the sensitive set. The default keys are never cleared,
// honoring the append-only contract.
func (r *Redactor) Append(paths []string) {
	r.sensitiveKeys = append(r.sensitiveKeys, paths...)
	r.rebuildFreeText()
}

// Clone returns a copy of the redactor with its own sensitive-key slice. Used by
// Child so that appending redact paths to a child never mutates the parent.
func (r *Redactor) Clone() *Redactor {
	keys := make([]string, len(r.sensitiveKeys))
	copy(keys, r.sensitiveKeys)
	c := &Redactor{sensitiveKeys: keys}
	c.rebuildFreeText()
	return c
}

func (r *Redactor) rebuildFreeText() {
	// key=value / key: value masking for any sensitive key.
	pattern := `(?i)\b(` + strings.Join(r.sensitiveKeys, "|") + `)\b(\s*[=:]\s*)(\S+)`
	r.freeTextRe = regexp.MustCompile(pattern)
	// standalone "Bearer <token>" masking.
	r.bearerRe = regexp.MustCompile(`(?i)\bBearer\s+\S+`)
}

// normalizeKey lowercases and strips '_' and '-' for case/separator-insensitive matching.
func normalizeKey(k string) string {
	k = strings.ToLower(k)
	k = strings.ReplaceAll(k, "_", "")
	k = strings.ReplaceAll(k, "-", "")
	return k
}

// keyMatches reports whether a payload key should be fully masked. Matching is
// case-insensitive, ignores '_'/'-', and also matches a path tail (e.g.
// "user.email" matches because its tail "email" equals a sensitive key).
func (r *Redactor) keyMatches(key string) bool {
	candidates := []string{key}
	if idx := strings.LastIndex(key, "."); idx >= 0 {
		candidates = append(candidates, key[idx+1:])
	}
	for _, cand := range candidates {
		norm := normalizeKey(cand)
		for _, sk := range r.sensitiveKeys {
			skNorm := normalizeKey(sk)
			if norm == skNorm || strings.HasSuffix(norm, skNorm) {
				return true
			}
		}
	}
	return false
}

// RedactEvent masks sensitive content in a built event (message free text + payload).
func (r *Redactor) RedactEvent(e *LogEvent) {
	e.Message = r.redactFreeText(e.Message)
	if e.Payload != nil {
		e.Payload = r.redactValue(e.Payload).(map[string]interface{})
	}
}

// redactValue recursively masks sensitive keys and free text within string values.
func (r *Redactor) redactValue(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, vv := range val {
			if r.keyMatches(k) {
				out[k] = MaskString
			} else {
				out[k] = r.redactValue(vv)
			}
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, vv := range val {
			out[i] = r.redactValue(vv)
		}
		return out
	case string:
		return r.redactFreeText(val)
	default:
		return val
	}
}

// redactFreeText masks sensitive values embedded in free text using
// key=value / key: value patterns and standalone Bearer tokens.
func (r *Redactor) redactFreeText(s string) string {
	if s == "" {
		return s
	}
	// First mask standalone Bearer tokens so "Authorization: Bearer x" becomes
	// "Authorization: Bearer ***REDACTED***", then the key=value rule sees an
	// already-masked value and leaves it untouched.
	masked := r.bearerRe.ReplaceAllString(s, "Bearer "+MaskString)
	masked = r.freeTextRe.ReplaceAllString(masked, "${1}${2}"+MaskString)
	return masked
}

// IsSensitive reports whether a key is in the sensitive set (used by tests).
func (r *Redactor) IsSensitive(key string) bool {
	return r.keyMatches(key)
}
