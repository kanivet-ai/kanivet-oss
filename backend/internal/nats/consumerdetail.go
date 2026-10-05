package nats

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// GetConsumerInfo returns a consumer's full state: delivered sequence, ack
// floor, and its configuration (deliver policy, filter subject). This is a
// read call. It never calls Fetch, Next, or any Ack/Nak/Term method.
func (p *LivePool) GetConsumerInfo(ctx context.Context, cluster, streamName, consumerName string) (*jetstream.ConsumerInfo, error) {
	stream, err := p.openStream(ctx, cluster, streamName)
	if err != nil {
		return nil, err
	}
	consumer, err := stream.Consumer(ctx, consumerName)
	if err != nil {
		return nil, fmt.Errorf("open consumer %s on stream %s: %w", consumerName, streamName, err)
	}
	info, err := consumer.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("get consumer info for %s/%s: %w", streamName, consumerName, err)
	}
	return info, nil
}
