package common

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStreamStatus_SetEndReason_FirstWins(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	s.SetEndReason(StreamEndReasonDone, nil)
	s.SetEndReason(StreamEndReasonTimeout, nil)
	s.SetEndReason(StreamEndReasonClientGone, fmt.Errorf("context canceled"))

	assert.Equal(t, StreamEndReasonDone, s.EndReason())
	assert.Nil(t, s.EndError())
}

func TestStreamStatus_SetEndReason_WithError(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	expectedErr := fmt.Errorf("read: connection reset")
	s.SetEndReason(StreamEndReasonScannerErr, expectedErr)

	assert.Equal(t, StreamEndReasonScannerErr, s.EndReason())
	assert.Equal(t, expectedErr, s.EndError())
}

func TestStreamStatus_SetEndReason_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	s.SetEndReason(StreamEndReasonDone, nil)
}

func TestStreamStatus_SetEndReason_Concurrent(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	reasons := []StreamEndReason{
		StreamEndReasonDone,
		StreamEndReasonTimeout,
		StreamEndReasonClientGone,
		StreamEndReasonScannerErr,
		StreamEndReasonHandlerStop,
		StreamEndReasonEOF,
		StreamEndReasonPanic,
		StreamEndReasonPingFail,
	}

	var wg sync.WaitGroup
	for _, r := range reasons {
		wg.Add(1)
		go func(reason StreamEndReason) {
			defer wg.Done()
			s.SetEndReason(reason, nil)
		}(r)
	}
	wg.Wait()

	assert.NotEqual(t, StreamEndReasonNone, s.EndReason())
}

func TestStreamStatus_RecordError_Basic(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	s.RecordError("bad json")
	s.RecordError("another bad json")
	s.RecordError("client gone")

	assert.True(t, s.HasErrors())
	assert.Equal(t, 3, s.TotalErrorCount())
	assert.Len(t, s.Errors, 3)
}

func TestStreamStatus_RecordError_CapAtMax(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	for i := range 30 {
		s.RecordError(fmt.Sprintf("error_%d", i))
	}

	assert.Equal(t, maxStreamErrorEntries, len(s.Errors))
	assert.Equal(t, 30, s.TotalErrorCount())
}

func TestStreamStatus_RecordError_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	s.RecordError("should not panic")
}

func TestStreamStatus_RecordError_Concurrent(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s.RecordError(fmt.Sprintf("error_%d", idx))
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 100, s.TotalErrorCount())
	assert.LessOrEqual(t, len(s.Errors), maxStreamErrorEntries)
}

func TestStreamStatus_HasErrors_Empty(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()
	assert.False(t, s.HasErrors())
	assert.Equal(t, 0, s.TotalErrorCount())
}

func TestStreamStatus_HasErrors_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	assert.False(t, s.HasErrors())
	assert.Equal(t, 0, s.TotalErrorCount())
}

func TestStreamStatus_IsNormalEnd(t *testing.T) {
	t.Parallel()
	tests := []struct {
		reason StreamEndReason
		normal bool
	}{
		{StreamEndReasonDone, true},
		{StreamEndReasonEOF, true},
		{StreamEndReasonHandlerStop, true},
		{StreamEndReasonTimeout, false},
		{StreamEndReasonClientGone, false},
		{StreamEndReasonScannerErr, false},
		{StreamEndReasonPanic, false},
		{StreamEndReasonPingFail, false},
		{StreamEndReasonNone, false},
	}
	for _, tt := range tests {
		s := NewStreamStatus()
		s.SetEndReason(tt.reason, nil)
		assert.Equal(t, tt.normal, s.IsNormalEnd(), "reason=%s", tt.reason)
	}
}

func TestStreamStatus_IsNormalEnd_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	assert.True(t, s.IsNormalEnd())
}

func TestStreamStatus_Summary(t *testing.T) {
	t.Parallel()

	s := NewStreamStatus()
	s.SetEndReason(StreamEndReasonDone, nil)
	summary := s.Summary()
	assert.Contains(t, summary, "reason=done")
	assert.NotContains(t, summary, "soft_errors")

	s2 := NewStreamStatus()
	s2.SetEndReason(StreamEndReasonTimeout, nil)
	s2.RecordError("bad json")
	s2.RecordError("write failed")
	summary2 := s2.Summary()
	assert.Contains(t, summary2, "reason=timeout")
	assert.Contains(t, summary2, "soft_errors=2")
}

func TestStreamStatus_Summary_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	assert.Equal(t, "StreamStatus<nil>", s.Summary())
}

// The writer runs on its own goroutine after the reader has started, so the
// two overlap: a write on the test goroutine would happen before the reader
// exists, and the race detector would see no concurrency to report.
//
// This is a secondary check that puts one write next to every accessor at
// once. It catches a missing lock on most runs but not on every run, because
// a report needs the two goroutines to overlap on the same iteration. The
// per-path guarantee lives in
// TestStreamStatus_EachAccessorReadsWhileAnotherGoroutineSetsTheEndReason,
// whose subtests each overlap a write with a single accessor.
func TestStreamStatus_EndReason_ReadableWhileAnotherGoroutineSetsIt(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	started := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		close(started)
		for i := 0; i < 100000; i++ {
			_ = s.EndReason()
			_ = s.IsNormalEnd()
			_ = s.Summary()
			_ = s.OutcomeSnapshot()
			_ = s.EndError()
		}
	}()

	<-started
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		s.SetEndReason(StreamEndReasonClientGone, fmt.Errorf("context canceled"))
	}()

	<-readerDone
	<-writerDone

	assert.Equal(t, StreamEndReasonClientGone, s.EndReason())
	assert.EqualError(t, s.EndError(), "context canceled")
}

// Overlaps one SetEndReason write with the reads of each accessor, so every
// read path has a test that reports a missing lock on its own.
func TestStreamStatus_EachAccessorReadsWhileAnotherGoroutineSetsTheEndReason(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*StreamStatus){
		"EndReason":       func(s *StreamStatus) { _ = s.EndReason() },
		"EndError":        func(s *StreamStatus) { _ = s.EndError() },
		"IsNormalEnd":     func(s *StreamStatus) { _ = s.IsNormalEnd() },
		"Summary":         func(s *StreamStatus) { _ = s.Summary() },
		"OutcomeSnapshot": func(s *StreamStatus) { _ = s.OutcomeSnapshot() },
	}
	for name, read := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := NewStreamStatus()

			started := make(chan struct{})
			readerDone := make(chan struct{})
			go func() {
				defer close(readerDone)
				close(started)
				for i := 0; i < 100000; i++ {
					read(s)
				}
			}()

			<-started
			writerDone := make(chan struct{})
			go func() {
				defer close(writerDone)
				s.SetEndReason(StreamEndReasonClientGone, fmt.Errorf("context canceled"))
			}()

			<-readerDone
			<-writerDone

			assert.Equal(t, StreamEndReasonClientGone, s.EndReason())
		})
	}
}

func TestStreamStatus_Accessors_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	assert.Equal(t, StreamEndReasonNone, s.EndReason())
	assert.NoError(t, s.EndError())
	assert.False(t, s.ResolveClientCancelAsDone())
}

func TestStreamStatus_ResolveClientCancelAsDone(t *testing.T) {
	t.Parallel()

	t.Run("client cancellation becomes done", func(t *testing.T) {
		t.Parallel()
		s := NewStreamStatus()
		s.SetEndReason(StreamEndReasonClientGone, context.Canceled)

		assert.True(t, s.ResolveClientCancelAsDone())
		assert.Equal(t, StreamEndReasonDone, s.EndReason())
		assert.NoError(t, s.EndError())
		assert.True(t, s.IsNormalEnd())
	})

	t.Run("a soft error keeps the cancellation", func(t *testing.T) {
		t.Parallel()
		s := NewStreamStatus()
		s.SetEndReason(StreamEndReasonClientGone, context.Canceled)
		s.RecordError("write failed")

		assert.False(t, s.ResolveClientCancelAsDone())
		assert.Equal(t, StreamEndReasonClientGone, s.EndReason())
	})

	t.Run("another cancellation error keeps the reason", func(t *testing.T) {
		t.Parallel()
		s := NewStreamStatus()
		s.SetEndReason(StreamEndReasonClientGone, context.DeadlineExceeded)

		assert.False(t, s.ResolveClientCancelAsDone())
		assert.Equal(t, StreamEndReasonClientGone, s.EndReason())
	})

	t.Run("a normal end is left alone", func(t *testing.T) {
		t.Parallel()
		s := NewStreamStatus()
		s.SetEndReason(StreamEndReasonDone, nil)

		assert.False(t, s.ResolveClientCancelAsDone())
		assert.Equal(t, StreamEndReasonDone, s.EndReason())
	})
}
