package redaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"
)

const MaxBodyBytes = 8 << 20

var ErrSensitiveURL = errors.New("fetch target contains protected data")

type Format string

const (
	Chat      Format = "chat"
	Responses Format = "responses"
	Claude    Format = "claude"
	Gemini    Format = "gemini"
	Search    Format = "search"
	Fetch     Format = "fetch"
)

type scope uint8

const (
	noScope scope = iota
	userScope
	toolScope
	systemScope
	toolPayloadScope
)

func (s *Session) RewriteJSON(raw []byte, format Format) ([]byte, error) {
	switch format {
	case Chat, Responses, Claude, Gemini, Search, Fetch:
	default:
		return nil, ErrInvalidDocument
	}
	if len(raw) > MaxBodyBytes {
		return nil, ErrLimit
	}
	if !utf8.Valid(raw) {
		return nil, ErrInvalidDocument
	}
	if err := validateJSONFields(raw); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil || document == nil || decoder.Decode(new(any)) != io.EOF {
		return nil, ErrInvalidDocument
	}
	if format == Fetch {
		target, ok := document["url"].(string)
		if !ok {
			return nil, ErrInvalidDocument
		}
		if err := s.checkFetchURL(target); err != nil {
			return nil, err
		}
		for field, value := range document {
			if field == "url" {
				continue
			}
			if err := s.checkUnchangedToolField(field, value); err != nil {
				return nil, err
			}
		}
		return raw, nil
	}
	rewritten, err := s.walk(document, format, noScope, "", 0)
	if err != nil {
		return nil, err
	}
	if len(s.counts) == 0 {
		return raw, nil
	}
	out, err := json.Marshal(rewritten)
	if err != nil {
		return nil, ErrInvalidDocument
	}
	if len(out) > MaxBodyBytes {
		return nil, ErrLimit
	}
	return out, nil
}

func (s *Session) walk(node any, format Format, active scope, key string, depth int) (any, error) {
	if depth > 80 {
		return nil, ErrLimit
	}
	switch value := node.(type) {
	case string:
		if s.enabled(active) {
			return s.Redact(value)
		}
		if format == Search && active == noScope {
			return value, s.checkUnchangedToolValue(value)
		}
	case json.Number:
		if format == Search {
			return value, s.checkUnchangedToolValue(value)
		}
	case []any:
		for i, item := range value {
			result, err := s.walk(item, format, active, key, depth+1)
			if err != nil {
				return nil, err
			}
			value[i] = result
		}
	case map[string]any:
		if format != Gemini || (key != "" && key != "requests" && key != "request") || active == toolScope {
			active = objectScope(value, active)
		}
		for field, item := range value {
			if format == Search {
				if err := s.checkUnchangedToolValue(field); err != nil {
					return nil, err
				}
			}
			folded := strings.ToLower(field)
			next := fieldScope(format, active, key, folded)
			if next == toolScope && (folded == "arguments" || folded == "output" || folded == "input") {
				if text, ok := item.(string); ok && json.Valid([]byte(text)) && (strings.HasPrefix(strings.TrimSpace(text), "{") || strings.HasPrefix(strings.TrimSpace(text), "[")) {
					result, err := s.rewriteEmbedded(text, format, depth+1)
					if err != nil {
						return nil, err
					}
					value[field] = result
					continue
				}
			}
			result, err := s.walk(item, format, next, folded, depth+1)
			if err != nil {
				return nil, err
			}
			value[field] = result
		}
	}
	return node, nil
}

func (s *Session) rewriteEmbedded(text string, format Format, depth int) (string, error) {
	if !s.enabled(toolScope) {
		return text, nil
	}
	if len(text) > MaxTextBytes-s.scanned {
		return "", ErrLimit
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var object any
	if err := decoder.Decode(&object); err != nil {
		return "", ErrInvalidDocument
	}
	result, err := s.walk(object, format, toolScope, "arguments", depth)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", ErrInvalidDocument
	}
	return string(encoded), nil
}

func (s *Session) checkUnchangedToolField(key string, value any) error {
	if err := s.checkUnchangedToolValue(key); err != nil {
		return err
	}
	return s.checkUnchangedToolValue(value)
}

func (s *Session) checkUnchangedToolValue(value any) error {
	switch value := value.(type) {
	case string:
		matched, err := s.Matches(value)
		if err != nil {
			return err
		}
		if matched {
			return ErrInvalidDocument
		}
	case json.Number:
		return s.checkUnchangedToolValue(value.String())
	case []any:
		for _, child := range value {
			if err := s.checkUnchangedToolValue(child); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, child := range value {
			if err := s.checkUnchangedToolField(key, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Session) checkFetchURL(target string) error {
	parsed, err := url.Parse(target)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" {
		return ErrInvalidDocument
	}
	if parsed.RawQuery != "" {
		// Query decoders interpret '+' as a space; path decoders do not.
		query := strings.ReplaceAll(parsed.RawQuery, "+", " ")
		if query != parsed.RawQuery {
			if err := s.CheckURLComponent(query); err != nil {
				return err
			}
		}
	}
	return s.CheckURLComponent(target)
}

func (s *Session) CheckURLComponent(target string) error {
	const maxEscapes = 4
	for depth := 0; ; depth++ {
		matched, err := s.Matches(target)
		if err != nil {
			return err
		}
		if matched {
			return ErrSensitiveURL
		}
		if !strings.Contains(target, "%") {
			return nil
		}
		if depth == maxEscapes {
			return ErrLimit
		}
		decoded, err := url.PathUnescape(target)
		if err != nil {
			return ErrInvalidDocument
		}
		target = decoded
	}
}

func objectScope(value map[string]any, active scope) scope {
	if active == toolScope || active == systemScope {
		return active
	}
	var role, tag string
	for field, item := range value {
		switch strings.ToLower(field) {
		case "role":
			role, _ = item.(string)
		case "type":
			tag, _ = item.(string)
		}
	}
	if tag == "tool_use" || tag == "tool_result" || tag == "function_call" || tag == "function_call_output" {
		return toolScope
	}
	switch role {
	case "user":
		return userScope
	case "tool", "function":
		return toolScope
	case "system", "developer":
		return systemScope
	case "assistant", "model":
		return noScope
	}
	return active
}

func fieldScope(format Format, active scope, parent, field string) scope {
	if format == Search {
		if parent == "" {
			switch field {
			case "query", "q", "text_query", "search_query":
				return userScope
			}
		}
		if parent == "search_query" && field == "q" {
			return userScope
		}
		return noScope
	}
	if active == toolPayloadScope {
		return toolPayloadScope
	}
	if active == toolScope {
		switch parent {
		case "arguments", "response", "input":
			return toolScope
		case "functioncall", "function_call", "functionresponse", "function_response":
			if format == Gemini {
				switch field {
				case "args", "response":
					return toolPayloadScope
				}
				switch field {
				case "id", "name", "willcontinue", "will_continue", "scheduling":
					return noScope
				}
			}
		}
	}
	if active == toolScope {
		switch field {
		case "id", "type", "role", "name", "call_id", "tool_use_id", "index":
			return noScope
		default:
			return toolScope
		}
	}
	switch field {
	case "system", "instructions", "systeminstruction", "system_instruction":
		return systemScope
	case "tool_calls", "function_call", "functioncall", "functionresponse", "function_call_output", "function_response", "arguments", "tool_result":
		return toolScope
	case "input":
		if active == systemScope {
			return active
		}
		if format == Responses {
			return userScope
		}
	case "contents":
		if format == Gemini && (parent == "" || parent == "requests" || parent == "request") {
			return userScope
		}
	case "text", "content", "parts", "output_text":
		return active
	}
	return noScope
}

func (s *Session) enabled(active scope) bool {
	switch active {
	case userScope:
		return s.policy.scopes.UserMessages
	case toolScope, toolPayloadScope:
		return s.policy.scopes.ToolIO
	case systemScope:
		return s.policy.scopes.SystemPrompt
	}
	return false
}
