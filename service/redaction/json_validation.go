package redaction

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

func validateJSONFields(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := validateJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalidDocument
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 80 {
		return ErrLimit
	}
	token, err := decoder.Token()
	if err != nil {
		return ErrInvalidDocument
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	var names map[string]struct{}
	if delimiter == '{' {
		names = make(map[string]struct{})
	}
	for decoder.More() {
		if names != nil {
			field, err := decoder.Token()
			name, ok := field.(string)
			if err != nil || !ok {
				return ErrInvalidDocument
			}
			// Relay DTOs use encoding/json, which matches fields case-insensitively.
			name = strings.ToLower(name)
			if _, duplicate := names[name]; duplicate {
				return ErrInvalidDocument
			}
			names[name] = struct{}{}
		}
		if err := validateJSONValue(decoder, depth+1); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return ErrInvalidDocument
	}
	return nil
}
