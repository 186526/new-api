package common

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type StreamEndReason string

const (
	StreamEndReasonNone        StreamEndReason = ""
	StreamEndReasonDone        StreamEndReason = "done"
	StreamEndReasonTimeout     StreamEndReason = "timeout"
	StreamEndReasonClientGone  StreamEndReason = "client_gone"
	StreamEndReasonScannerErr  StreamEndReason = "scanner_error"
	StreamEndReasonHandlerStop StreamEndReason = "handler_stop"
	StreamEndReasonEOF         StreamEndReason = "eof"
	StreamEndReasonPanic       StreamEndReason = "panic"
	StreamEndReasonPingFail    StreamEndReason = "ping_fail"
)

// ResponseOutcome is the protocol-level result of one response, independent of
// how the transport ended. Adaptors mark it from the events they already parse.
type ResponseOutcome string

const (
	ResponseOutcomeUnknown    ResponseOutcome = ""
	ResponseOutcomeCompleted  ResponseOutcome = "completed"
	ResponseOutcomeFailed     ResponseOutcome = "failed"
	ResponseOutcomeIncomplete ResponseOutcome = "incomplete"
	ResponseOutcomeCancelled  ResponseOutcome = "cancelled"
)

const maxStreamErrorEntries = 20

type StreamErrorEntry struct {
	Message   string
	Timestamp time.Time
}

type StreamStatus struct {
	endOnce sync.Once

	mu         sync.Mutex
	ErrorCount int

	endReason StreamEndReason
	endError  error
	Errors    []StreamErrorEntry

	response         ResponseOutcome
	errorCode        string
	errorType        string
	errorStatus      int
	incompleteReason string
	expectsTerminal  bool
}

// StreamOutcome holds classification facts only; upstream messages never
// enter it because they may contain credentials or request content.
type StreamOutcome struct {
	EndReason        StreamEndReason
	HasErrors        bool
	ExpectsTerminal  bool
	Response         ResponseOutcome
	ErrorCode        string
	ErrorType        string
	ErrorStatus      int
	IncompleteReason string
}

func NewStreamStatus() *StreamStatus {
	return &StreamStatus{}
}

// SetEndReason records the first reason the stream ended. endOnce keeps the
// first writer, and the fields it sets live under mu so a reader on another
// goroutine, such as a stream handler asking whether the client left, sees
// either the zero value or the recorded one rather than a torn state.
func (s *StreamStatus) SetEndReason(reason StreamEndReason, err error) {
	if s == nil {
		return
	}
	s.endOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.endReason = reason
		s.endError = err
	})
}

// EndReason returns the recorded end reason, or StreamEndReasonNone while the
// stream is still running.
func (s *StreamStatus) EndReason() StreamEndReason {
	if s == nil {
		return StreamEndReasonNone
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endReason
}

// EndError returns the error recorded with the end reason.
func (s *StreamStatus) EndError() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endError
}

// ResolveClientCancelAsDone turns a client cancellation that raced the
// delivered completion into a normal end, so it neither logs nor bills as a
// failure. It only touches a cancellation still recorded after every stream
// goroutine joined, and reports whether it did.
func (s *StreamStatus) ResolveClientCancelAsDone() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.endReason != StreamEndReasonClientGone || !errors.Is(s.endError, context.Canceled) || s.ErrorCount > 0 {
		return false
	}
	s.endReason = StreamEndReasonDone
	s.endError = nil
	return true
}

func (s *StreamStatus) RecordError(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ErrorCount++
	if len(s.Errors) < maxStreamErrorEntries {
		s.Errors = append(s.Errors, StreamErrorEntry{
			Message:   msg,
			Timestamp: time.Now(),
		})
	}
}

// RequireTerminal declares that the protocol always ends with an explicit
// terminal event, so a stream that ends without one was cut short.
func (s *StreamStatus) RequireTerminal() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expectsTerminal = true
}

// MarkCompleted, MarkIncomplete and MarkCancelled keep the first terminal seen;
// MarkFailed always wins because an error after completion is still a failure.
func (s *StreamStatus) MarkCompleted() {
	s.markTerminal(ResponseOutcomeCompleted, "")
}

func (s *StreamStatus) MarkIncomplete(reason string) {
	s.markTerminal(ResponseOutcomeIncomplete, reason)
}

func (s *StreamStatus) MarkCancelled() {
	s.markTerminal(ResponseOutcomeCancelled, "")
}

func (s *StreamStatus) markTerminal(outcome ResponseOutcome, incompleteReason string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.response != ResponseOutcomeUnknown {
		return
	}
	s.response = outcome
	s.incompleteReason = incompleteReason
}

// MarkFailed records a protocol failure. Empty details never erase details
// recorded earlier, so a bare error envelope keeps the structured error.
func (s *StreamStatus) MarkFailed(code, errorType string, status int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.response = ResponseOutcomeFailed
	if code != "" {
		s.errorCode = code
	}
	if errorType != "" {
		s.errorType = errorType
	}
	if status != 0 {
		s.errorStatus = status
	}
}

func (s *StreamStatus) ResponseOutcome() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.response)
}

func (s *StreamStatus) ResponseFailed() bool {
	return s.ResponseOutcome() == string(ResponseOutcomeFailed)
}

func (s *StreamStatus) OutcomeSnapshot() StreamOutcome {
	if s == nil {
		return StreamOutcome{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return StreamOutcome{
		EndReason:        s.endReason,
		HasErrors:        s.ErrorCount > 0,
		ExpectsTerminal:  s.expectsTerminal,
		Response:         s.response,
		ErrorCode:        s.errorCode,
		ErrorType:        s.errorType,
		ErrorStatus:      s.errorStatus,
		IncompleteReason: s.incompleteReason,
	}
}

func (s *StreamStatus) HasErrors() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount > 0
}

func (s *StreamStatus) TotalErrorCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount
}

// IsNormalEnd reports whether the recorded end reason is one a delivered
// stream can end with.
func (s *StreamStatus) IsNormalEnd() bool {
	if s == nil {
		return true
	}
	switch s.EndReason() {
	case StreamEndReasonDone, StreamEndReasonEOF, StreamEndReasonHandlerStop:
		return true
	default:
		return false
	}
}

func (s *StreamStatus) Summary() string {
	if s == nil {
		return "StreamStatus<nil>"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b := &strings.Builder{}
	fmt.Fprintf(b, "reason=%s", s.endReason)
	if s.endError != nil {
		fmt.Fprintf(b, " end_error=%q", s.endError.Error())
	}
	if s.ErrorCount > 0 {
		fmt.Fprintf(b, " soft_errors=%d", s.ErrorCount)
	}
	return b.String()
}
