// Package agentlog provides a language-neutral, standard-library-only structured
// logging framework conforming to Schema v1 (JSONL wire format).
//
// This file implements a small, dependency-free JSON serializer and parser.
// No third-party JSON library is used anywhere in the package.
package agentlog

import (
	"fmt"
	"strconv"
	"strings"
)

// MarshalJSON serializes a Go value to a compact JSON string.
// Supported value types: nil, bool, string, int, int64, float64,
// map[string]interface{} and []interface{}.
func MarshalJSON(v interface{}) string {
	var b strings.Builder
	writeJSONValue(&b, v)
	return b.String()
}

func writeJSONValue(b *strings.Builder, v interface{}) {
	switch val := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if val {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		writeJSONString(b, val)
	case int:
		b.WriteString(strconv.FormatInt(int64(val), 10))
	case int64:
		b.WriteString(strconv.FormatInt(val, 10))
	case float64:
		b.WriteString(strconv.FormatFloat(val, 'f', -1, 64))
	case map[string]interface{}:
		b.WriteByte('{')
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		// deterministic output: write keys in sorted order.
		sortStrings(keys)
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, k)
			b.WriteByte(':')
			writeJSONValue(b, val[k])
		}
		b.WriteByte('}')
	case []interface{}:
		b.WriteByte('[')
		for i, e := range val {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONValue(b, e)
		}
		b.WriteByte(']')
	default:
		b.WriteString("null")
	}
}

func writeJSONString(b *strings.Builder, s string) {
	const hex = "0123456789abcdef"
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		case '\b':
			b.WriteString("\\b")
		case '\f':
			b.WriteString("\\f")
		default:
			if c < 0x20 {
				b.WriteString("\\u")
				b.WriteByte(hex[(int(c)>>4)&0xf])
				b.WriteByte(hex[int(c)&0xf])
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
}

func sortStrings(s []string) {
	// simple insertion sort to avoid importing sort for a tiny helper.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// ParseJSON parses a JSON string into Go values using a hand-rolled parser.
// Returns map[string]interface{}, []interface{}, string, int64, float64, bool or nil.
func ParseJSON(s string) (interface{}, error) {
	p := &jsonParser{data: []byte(s), pos: 0}
	p.skipWS()
	if p.pos >= len(p.data) {
		return nil, fmt.Errorf("agentlog: empty json input")
	}
	v, err := p.parseValue()
	if err != nil {
		return nil, err
	}
	p.skipWS()
	if p.pos != len(p.data) {
		return nil, fmt.Errorf("agentlog: trailing data at position %d", p.pos)
	}
	return v, nil
}

type jsonParser struct {
	data []byte
	pos  int
}

func (p *jsonParser) skipWS() {
	for p.pos < len(p.data) {
		c := p.data[p.pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			p.pos++
		} else {
			return
		}
	}
}

func (p *jsonParser) parseValue() (interface{}, error) {
	if p.pos >= len(p.data) {
		return nil, fmt.Errorf("agentlog: unexpected end of input")
	}
	c := p.data[p.pos]
	switch c {
	case '{':
		return p.parseObject()
	case '[':
		return p.parseArray()
	case '"':
		return p.parseString()
	case 't', 'f':
		return p.parseBool()
	case 'n':
		return p.parseNull()
	default:
		if c == '-' || (c >= '0' && c <= '9') {
			return p.parseNumber()
		}
		return nil, fmt.Errorf("agentlog: unexpected character %q at position %d", c, p.pos)
	}
}

func (p *jsonParser) parseObject() (interface{}, error) {
	obj := map[string]interface{}{}
	p.pos++ // consume '{'
	p.skipWS()
	if p.pos < len(p.data) && p.data[p.pos] == '}' {
		p.pos++
		return obj, nil
	}
	for {
		p.skipWS()
		if p.pos >= len(p.data) || p.data[p.pos] != '"' {
			return nil, fmt.Errorf("agentlog: expected object key at position %d", p.pos)
		}
		key, err := p.parseString()
		if err != nil {
			return nil, err
		}
		p.skipWS()
		if p.pos >= len(p.data) || p.data[p.pos] != ':' {
			return nil, fmt.Errorf("agentlog: expected ':' at position %d", p.pos)
		}
		p.pos++ // consume ':'
		p.skipWS()
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		obj[key.(string)] = val
		p.skipWS()
		if p.pos >= len(p.data) {
			return nil, fmt.Errorf("agentlog: unexpected end in object")
		}
		if p.data[p.pos] == ',' {
			p.pos++
			continue
		}
		if p.data[p.pos] == '}' {
			p.pos++
			return obj, nil
		}
		return nil, fmt.Errorf("agentlog: expected ',' or '}' at position %d", p.pos)
	}
}

func (p *jsonParser) parseArray() (interface{}, error) {
	arr := []interface{}{}
	p.pos++ // consume '['
	p.skipWS()
	if p.pos < len(p.data) && p.data[p.pos] == ']' {
		p.pos++
		return arr, nil
	}
	for {
		p.skipWS()
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		arr = append(arr, val)
		p.skipWS()
		if p.pos >= len(p.data) {
			return nil, fmt.Errorf("agentlog: unexpected end in array")
		}
		if p.data[p.pos] == ',' {
			p.pos++
			continue
		}
		if p.data[p.pos] == ']' {
			p.pos++
			return arr, nil
		}
		return nil, fmt.Errorf("agentlog: expected ',' or ']' at position %d", p.pos)
	}
}

func (p *jsonParser) parseString() (interface{}, error) {
	// assumes current char is '"'
	var b strings.Builder
	p.pos++ // consume opening quote
	for p.pos < len(p.data) {
		c := p.data[p.pos]
		if c == '"' {
			p.pos++
			return b.String(), nil
		}
		if c == '\\' {
			p.pos++
			if p.pos >= len(p.data) {
				return nil, fmt.Errorf("agentlog: unfinished escape")
			}
			esc := p.data[p.pos]
			switch esc {
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			case '/':
				b.WriteByte('/')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'u':
				p.pos++
				if p.pos+4 > len(p.data) {
					return nil, fmt.Errorf("agentlog: unfinished unicode escape")
				}
				var code int
				for i := 0; i < 4; i++ {
					code = code*16 + hexVal(p.data[p.pos+i])
				}
				p.pos += 4
				b.WriteRune(rune(code))
			default:
				return nil, fmt.Errorf("agentlog: invalid escape '\\%c'", esc)
			}
			p.pos++
			continue
		}
		b.WriteByte(c)
		p.pos++
	}
	return nil, fmt.Errorf("agentlog: unterminated string")
}

func (p *jsonParser) parseNumber() (interface{}, error) {
	start := p.pos
	for p.pos < len(p.data) {
		c := p.data[p.pos]
		if (c >= '0' && c <= '9') || c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E' {
			p.pos++
		} else {
			break
		}
	}
	raw := string(p.data[start:p.pos])
	if strings.ContainsAny(raw, ".eE") {
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("agentlog: invalid number %q: %w", raw, err)
		}
		return f, nil
	}
	i, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		// fall back to float on overflow
		f, ferr := strconv.ParseFloat(raw, 64)
		if ferr != nil {
			return nil, fmt.Errorf("agentlog: invalid number %q: %w", raw, err)
		}
		return f, nil
	}
	return i, nil
}

func (p *jsonParser) parseBool() (interface{}, error) {
	if p.pos+4 <= len(p.data) && string(p.data[p.pos:p.pos+4]) == "true" {
		p.pos += 4
		return true, nil
	}
	if p.pos+5 <= len(p.data) && string(p.data[p.pos:p.pos+5]) == "false" {
		p.pos += 5
		return false, nil
	}
	return nil, fmt.Errorf("agentlog: invalid literal at position %d", p.pos)
}

func (p *jsonParser) parseNull() (interface{}, error) {
	if p.pos+4 <= len(p.data) && string(p.data[p.pos:p.pos+4]) == "null" {
		p.pos += 4
		return nil, nil
	}
	return nil, fmt.Errorf("agentlog: invalid literal at position %d", p.pos)
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return 0
}
