package request

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

type exaContents struct {
	Results  []exaDocument `json:"results"`
	Statuses []exaStatus   `json:"statuses,omitempty"`
}

type exaDocument struct {
	ID    string `json:"id,omitempty"`
	URL   string `json:"url,omitempty"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

type exaStatus struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Error  json.RawMessage `json:"error"`
}

func normalizeExaContents(target string, entry forwardEntry) (forwardEntry, error) {
	if entry.StatusCode < http.StatusOK || entry.StatusCode >= http.StatusMultipleChoices {
		return entry, nil
	}
	body := entry.Body
	entry.Body = nil
	if len(bytes.TrimSpace(body)) == 0 {
		return entry, nil
	}
	var contents exaContents
	if err := common.Unmarshal(body, &contents); err != nil {
		return forwardEntry{}, sentError{errors.New("websearch: invalid exa contents response")}
	}
	document := contents.documentFor(target)
	if document == nil {
		return entry, nil
	}
	// Cache only the verified page, not the upstream's other results or statuses.
	encoded, err := common.Marshal(exaContents{Results: []exaDocument{{Title: document.Title, Text: document.Text}}})
	if err != nil {
		return forwardEntry{}, sentError{err}
	}
	entry.Body = encoded
	return entry, nil
}

func (contents exaContents) documentFor(target string) *exaDocument {
	target = exaDocumentURL(target)
	if target == "" {
		return nil
	}
	confirmed := false
	for _, status := range contents.Statuses {
		if exaDocumentURL(status.ID) != target {
			continue
		}
		if !status.succeeded() {
			return nil
		}
		confirmed = true
	}
	var matched *exaDocument
	for index := range contents.Results {
		document := &contents.Results[index]
		resultURL, resultID := exaDocumentURL(document.URL), exaDocumentURL(document.ID)
		if resultURL != target && !(confirmed && resultID == target && resultURL != "") {
			continue
		}
		if matched != nil || (resultID != "" && resultID != target) || strings.TrimSpace(document.Text) == "" {
			return nil
		}
		documentConfirmed := confirmed
		for _, status := range contents.Statuses {
			statusURL := exaDocumentURL(status.ID)
			if statusURL != resultURL && (document.ID == "" || (status.ID != document.ID && (resultID == "" || statusURL != resultID))) {
				continue
			}
			if !status.succeeded() {
				return nil
			}
			documentConfirmed = true
		}
		if len(contents.Statuses) != 0 && !documentConfirmed {
			return nil
		}
		matched = document
	}
	return matched
}

func (status exaStatus) succeeded() bool {
	return status.Status == "success" && (len(status.Error) == 0 || bytes.Equal(bytes.TrimSpace(status.Error), []byte("null")))
}

func exaDocumentURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Scheme == "https" && parsed.Port() == "443" || parsed.Scheme == "http" && parsed.Port() == "80" {
		parsed.Host = strings.TrimSuffix(parsed.Host, ":"+parsed.Port())
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	parsed.Fragment, parsed.RawFragment = "", ""
	return parsed.String()
}
