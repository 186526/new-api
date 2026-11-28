package redaction

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

const maxMatches = 2048
const maxRestoreBytes = 64 << 10
const maxRestorablePlaceholder = 32

type Session struct {
	policy          *Policy
	key             []byte
	nonce           string
	tokens          map[string]string
	restore         map[string]string
	counts          map[string]int
	scanned         int
	matches         int
	restoreBytes    int
	restorePrefixes map[string]struct{}
}

func (p *Policy) NewSession(fingerprintKey []byte) (*Session, error) {
	if p.strategy == "hmac_short" && len(fingerprintKey) < 16 {
		return nil, ErrInvalidDocument
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, ErrInvalidDocument
	}
	return &Session{policy: p, key: fingerprintKey, nonce: hex.EncodeToString(nonce[:]), tokens: map[string]string{}, restore: map[string]string{}, counts: map[string]int{}}, nil
}

type match struct {
	start, end, priority int
	restorable           bool
}

func (s *Session) find(text string) ([]match, error) {
	if len(text) > MaxTextBytes-s.scanned {
		return nil, ErrLimit
	}
	s.scanned += len(text)
	var found []match
	for priority, item := range s.policy.rules {
		for _, indices := range item.pattern.FindAllStringSubmatchIndex(text, maxMatches-s.matches+1) {
			s.matches++
			if s.matches > maxMatches {
				return nil, ErrLimit
			}
			start, end := indices[item.group*2], indices[item.group*2+1]
			if start < 0 || start == end {
				continue
			}
			value := text[start:end]
			if item.valid != nil && !item.valid(value) {
				continue
			}
			if s.policy.exempt(value) {
				continue
			}
			found = append(found, match{start, end, priority, item.restorable})
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].start < found[j].start })
	merged := found[:0]
	for _, current := range found {
		if len(merged) == 0 || current.start >= merged[len(merged)-1].end {
			merged = append(merged, current)
			continue
		}
		previous := &merged[len(merged)-1]
		// Never restore a union of partially overlapping values or a value that
		// was also detected by a credential or custom rule.
		previous.restorable = previous.restorable && current.restorable && previous.start == current.start && previous.end == current.end
		previous.end = max(previous.end, current.end)
		previous.priority = min(previous.priority, current.priority)
	}
	return merged, nil
}

func (p *Policy) exempt(value string) bool {
	if p.literals[value] {
		return true
	}
	for _, pattern := range p.exceptions {
		location := pattern.FindStringIndex(value)
		if location != nil && location[0] == 0 && location[1] == len(value) {
			return true
		}
	}
	return false
}

func (s *Session) Matches(text string) (bool, error) {
	matches, err := s.find(text)
	return len(matches) > 0, err
}

func (s *Session) CheckControlIdentifiers(values ...string) error {
	matcher := Session{policy: s.policy}
	for _, value := range values {
		if value == "" {
			continue
		}
		matched, err := matcher.Matches(value)
		if err != nil {
			return err
		}
		if matched {
			return ErrInvalidDocument
		}
	}
	return nil
}

type PreviewSegment struct {
	Text string `json:"text"`
	Rule string `json:"rule,omitempty"`
}

func (s *Session) Redact(text string) (string, error) {
	return s.redact(text, nil)
}

func (s *Session) Preview(text string) (string, []PreviewSegment, error) {
	segments := []PreviewSegment{}
	result, err := s.redact(text, func(text, rule string) {
		if text != "" {
			segments = append(segments, PreviewSegment{Text: text, Rule: rule})
		}
	})
	return result, segments, err
}

func (s *Session) redact(text string, segment func(string, string)) (string, error) {
	matches, err := s.find(text)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		if segment != nil {
			segment(text, "")
		}
		return text, nil
	}
	var output strings.Builder
	offset := 0
	for _, span := range matches {
		output.WriteString(text[offset:span.start])
		item := s.policy.rules[span.priority]
		s.counts[item.id]++
		replacement := s.replacement(item, text[span.start:span.end], span.restorable)
		output.WriteString(replacement)
		if segment != nil {
			segment(text[offset:span.start], "")
			segment(replacement, item.id)
		}
		offset = span.end
	}
	output.WriteString(text[offset:])
	if segment != nil {
		segment(text[offset:], "")
	}
	return output.String(), nil
}

func (s *Session) replacement(item rule, value string, restorable bool) string {
	if item.replacement != "" {
		return item.replacement
	}
	switch s.policy.strategy {
	case "mask":
		return "[REDACTED_" + item.label + "]"
	case "hmac_short":
		mac := hmac.New(sha256.New, s.key)
		mac.Write([]byte(item.label))
		mac.Write([]byte{0})
		mac.Write([]byte(value))
		return "<" + item.label + "#" + hex.EncodeToString(mac.Sum(nil)[:6]) + ">"
	}
	canRestore := s.policy.restore && restorable
	lookup := fmt.Sprintf("%t\x00%s\x00%s", canRestore, item.label, value)
	if existing := s.tokens[lookup]; existing != "" {
		return existing
	}
	token := fmt.Sprintf("<%s_%s_%d>", item.label, s.nonce, len(s.tokens)+1)
	s.tokens[lookup] = token
	if canRestore && len(token) <= maxRestorablePlaceholder && len(value) <= maxRestoreBytes-s.restoreBytes {
		s.restore[token] = value
		s.restoreBytes += len(value)
	}
	return token
}

func (s *Session) Counts() map[string]int {
	copyOfCounts := make(map[string]int, len(s.counts))
	for key, value := range s.counts {
		copyOfCounts[key] = value
	}
	return copyOfCounts
}

// Restorer is owned by a single client text output item. It must never be used
// for tool arguments, logs, errors, or upstream-bound messages.
type Restorer struct {
	session   *Session
	pending   string
	remaining int
}

func (s *Session) NewRestorer() *Restorer { return &Restorer{session: s, remaining: maxRestoreBytes} }

func (s *Session) hasRestorePrefix(prefix string) bool {
	if s.restorePrefixes == nil {
		s.restorePrefixes = make(map[string]struct{})
		for token := range s.restore {
			for length := 1; length < len(token); length++ {
				s.restorePrefixes[token[:length]] = struct{}{}
			}
		}
	}
	_, exists := s.restorePrefixes[prefix]
	return exists
}

func (r *Restorer) Push(delta string, final bool) string {
	if len(r.session.restore) == 0 {
		return delta
	}
	text := r.pending + delta
	r.pending = ""
	var result strings.Builder
	for len(text) > 0 {
		start := strings.IndexByte(text, '<')
		if start < 0 {
			result.WriteString(text)
			break
		}
		result.WriteString(text[:start])
		text = text[start:]
		prefix := text[:min(len(text), maxRestorablePlaceholder)]
		if end := strings.IndexByte(prefix, '>'); end >= 0 {
			token := prefix[:end+1]
			if plain, ok := r.session.restore[token]; ok && len(plain) <= r.remaining {
				result.WriteString(plain)
				r.remaining -= len(plain)
				text = text[len(token):]
				continue
			}
		}
		if !final && len(text) < maxRestorablePlaceholder && r.session.hasRestorePrefix(text) {
			r.pending = strings.Clone(text)
			break
		}
		result.WriteByte('<')
		text = text[1:]
	}
	return result.String()
}
