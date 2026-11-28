package redaction

import "context"

type requestObservationKey struct{}

type RequestObservation struct {
	Matches    int
	Categories int
}

func (s *Session) ProtectedContext(ctx context.Context) context.Context {
	observation := RequestObservation{Categories: len(s.counts)}
	for _, count := range s.counts {
		observation.Matches += count
	}
	return context.WithValue(ctx, requestObservationKey{}, observation)
}

func ObserveRequest(ctx context.Context) (RequestObservation, bool) {
	observation, protected := ctx.Value(requestObservationKey{}).(RequestObservation)
	return observation, protected
}

func IsProtectedRequest(ctx context.Context) bool {
	_, protected := ObserveRequest(ctx)
	return protected
}
