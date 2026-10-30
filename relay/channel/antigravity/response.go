package antigravity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// sseEnvelope 是 Antigravity 每个 SSE 帧的外层结构。
type sseEnvelope struct {
	Response json.RawMessage `json:"response"`
	TraceID  string          `json:"traceId"`
	Error    json.RawMessage `json:"error"`
}

type upstreamErrorEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func upstreamError(raw []byte) error {
	var payload upstreamErrorEnvelope
	if err := common.Unmarshal(raw, &payload); err != nil || payload.Message == "" {
		return fmt.Errorf("antigravity upstream error: %s", strings.TrimSpace(string(raw)))
	}
	status := payload.Status
	if status == "" {
		status = fmt.Sprintf("code=%d", payload.Code)
	}
	return fmt.Errorf("antigravity upstream error: %s: %s", status, payload.Message)
}

// unwrapResponseBody 把非流式上游响应（SSE 聚合成一个 JSON，或单个信封）还原成 Gemini 响应。
func unwrapResponseBody(raw []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("antigravity channel: empty upstream response")
	}
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		var envelope sseEnvelope
		if err := common.Unmarshal(trimmed, &envelope); err != nil {
			return nil, err
		}
		if len(envelope.Error) > 0 {
			return nil, upstreamError(envelope.Error)
		}
		if len(envelope.Response) > 0 {
			return envelope.Response, nil
		}
		return trimmed, nil
	}

	frames := make([][]byte, 0, 16)
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var envelope sseEnvelope
		if err := common.Unmarshal([]byte(payload), &envelope); err != nil {
			continue
		}
		if len(envelope.Error) > 0 {
			return nil, upstreamError(envelope.Error)
		}
		if len(envelope.Response) > 0 {
			frames = append(frames, envelope.Response)
		} else {
			frames = append(frames, []byte(payload))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(frames) == 0 {
		return nil, errors.New("antigravity channel: no frames in upstream response")
	}
	return mergeFrames(frames)
}

type geminiFrame struct {
	Candidates []struct {
		Content struct {
			Role  string           `json:"role"`
			Parts []map[string]any `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata json.RawMessage `json:"usageMetadata"`
	ModelVersion  string          `json:"modelVersion"`
}

// mergeFrames 把多个流式帧合并成一个 Gemini 响应，供非流式下游使用。
func mergeFrames(frames [][]byte) ([]byte, error) {
	state := &mergeState{thoughtIndex: -1, textIndex: -1}
	parts := make([]map[string]any, 0, 16)
	finishReason := ""
	modelVersion := ""
	var usage json.RawMessage
	for _, frame := range frames {
		var parsed geminiFrame
		if err := common.Unmarshal(frame, &parsed); err != nil {
			continue
		}
		if parsed.ModelVersion != "" {
			modelVersion = parsed.ModelVersion
		}
		if len(parsed.UsageMetadata) > 0 {
			usage = parsed.UsageMetadata
		}
		if len(parsed.Candidates) == 0 {
			continue
		}
		candidate := parsed.Candidates[0]
		parts = mergeParts(parts, candidate.Content.Parts, state)
		if candidate.FinishReason != "" {
			finishReason = candidate.FinishReason
		}
	}
	if len(parts) == 0 {
		return nil, errors.New("antigravity channel: upstream response has no content parts")
	}
	if finishReason == "" {
		finishReason = "STOP"
	}
	merged := map[string]any{
		"candidates": []any{
			map[string]any{
				"content":      map[string]any{"role": "model", "parts": parts},
				"finishReason": finishReason,
			},
		},
	}
	if modelVersion != "" {
		merged["modelVersion"] = modelVersion
	}
	if len(usage) > 0 {
		var usageValue any
		if err := common.Unmarshal(usage, &usageValue); err == nil {
			merged["usageMetadata"] = usageValue
		}
	}
	return common.Marshal(merged)
}

// mergeState 记录正在累积的思考段与文本段位置：同一段文字在相邻帧间被切开时要接上，
// 思考段固定放在最前，遇到工具调用后新到的文本另起一段。
type mergeState struct {
	thoughtIndex int
	textIndex    int
}

func mergeParts(target []map[string]any, incoming []map[string]any, state *mergeState) []map[string]any {
	for _, part := range incoming {
		if thought, _ := part["thought"].(bool); thought {
			if state.thoughtIndex >= 0 {
				existing := target[state.thoughtIndex]
				existing["text"] = textOf(existing) + textOf(part)
				if signature, ok := thoughtSignatureOf(part); ok {
					existing["thoughtSignature"] = signature
				}
				continue
			}
			thoughtPart := map[string]any{"thought": true, "text": textOf(part)}
			if signature, ok := thoughtSignatureOf(part); ok {
				thoughtPart["thoughtSignature"] = signature
			}
			target = append([]map[string]any{thoughtPart}, target...)
			state.thoughtIndex = 0
			if state.textIndex >= 0 {
				state.textIndex++
			}
			continue
		}
		if isPlainText(part) {
			if state.textIndex >= 0 {
				existing := target[state.textIndex]
				existing["text"] = textOf(existing) + textOf(part)
				continue
			}
			target = append(target, map[string]any{"text": textOf(part)})
			state.textIndex = len(target) - 1
			continue
		}
		target = append(target, part)
		state.textIndex = -1
	}
	return target
}

func textOf(part map[string]any) string {
	text, _ := part["text"].(string)
	return text
}

func thoughtSignatureOf(part map[string]any) (any, bool) {
	if signature, ok := part["thoughtSignature"]; ok {
		return signature, true
	}
	signature, ok := part["thought_signature"]
	return signature, ok
}

func isPlainText(part map[string]any) bool {
	if _, ok := part["text"].(string); !ok {
		return false
	}
	for _, key := range []string{"thought", "thoughtSignature", "thought_signature", "functionCall", "functionResponse", "inlineData", "fileData"} {
		if _, ok := part[key]; ok {
			return false
		}
	}
	return true
}

// unwrapReader 逐行处理上游 SSE，把 {"response":{...}} 拆成裸的 Gemini 帧。
type unwrapReader struct {
	scanner *bufio.Scanner
	closer  io.Closer
	out     bytes.Buffer
	done    bool
}

func newUnwrapReader(source io.ReadCloser) io.ReadCloser {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	return &unwrapReader{scanner: scanner, closer: source}
}

func (r *unwrapReader) Read(p []byte) (int, error) {
	for r.out.Len() == 0 {
		if r.done {
			return 0, io.EOF
		}
		if !r.scanner.Scan() {
			r.done = true
			if err := r.scanner.Err(); err != nil {
				return 0, err
			}
			continue
		}
		line := strings.TrimSpace(r.scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			r.out.WriteString("data: [DONE]\n\n")
			continue
		}
		var envelope sseEnvelope
		if err := common.Unmarshal([]byte(payload), &envelope); err != nil {
			continue
		}
		if len(envelope.Error) > 0 {
			if err := upstreamError(envelope.Error); err != nil {
				return 0, err
			}
			continue
		}
		frame := envelope.Response
		if len(frame) == 0 {
			frame = []byte(payload)
		}
		r.out.WriteString("data: ")
		r.out.Write(frame)
		r.out.WriteString("\n\n")
	}
	return r.out.Read(p)
}

func (r *unwrapReader) Close() error {
	return r.closer.Close()
}
