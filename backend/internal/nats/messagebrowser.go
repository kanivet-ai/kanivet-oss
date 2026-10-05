package nats

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// GetStreamMsg fetches one message by sequence number directly from a
// stream. This is a direct read, not a consumer fetch: it creates no
// consumer and has no ack or redelivery side effect.
func (p *LivePool) GetStreamMsg(ctx context.Context, cluster, streamName string, seq uint64) (*jetstream.RawStreamMsg, error) {
	stream, err := p.openStream(ctx, cluster, streamName)
	if err != nil {
		return nil, err
	}
	msg, err := stream.GetMsg(ctx, seq)
	if err != nil {
		return nil, fmt.Errorf("get message %d from stream %s: %w", seq, streamName, err)
	}
	return msg, nil
}

// GetLastStreamMsg fetches the newest message matching subjectFilter in a
// stream. Read-only, same as GetStreamMsg.
func (p *LivePool) GetLastStreamMsg(ctx context.Context, cluster, streamName, subjectFilter string) (*jetstream.RawStreamMsg, error) {
	stream, err := p.openStream(ctx, cluster, streamName)
	if err != nil {
		return nil, err
	}
	msg, err := stream.GetLastMsgForSubject(ctx, subjectFilter)
	if err != nil {
		return nil, fmt.Errorf("get last message for subject %s in stream %s: %w", subjectFilter, streamName, err)
	}
	return msg, nil
}

func (p *LivePool) openStream(ctx context.Context, cluster, streamName string) (jetstream.Stream, error) {
	lc, err := p.Get(ctx, cluster)
	if err != nil {
		return nil, err
	}
	stream, err := lc.js.Stream(ctx, streamName)
	if err != nil {
		return nil, fmt.Errorf("open stream %s: %w", streamName, err)
	}
	return stream, nil
}
