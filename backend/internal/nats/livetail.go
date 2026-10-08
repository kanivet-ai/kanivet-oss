package nats

import (
	"context"
	"fmt"

	natsgo "github.com/nats-io/nats.go"
)

// Subscribe starts a plain subject subscription and calls onMsg for every
// message received, until the returned unsubscribe func is called or ctx
// (used only to obtain the connection) is already done. This is a core NATS
// SUB, not a JetStream consumer: it creates nothing on the server and has no
// ack or redelivery state to affect.
func (p *LivePool) Subscribe(ctx context.Context, cluster, subject string, onMsg func(*natsgo.Msg)) (unsubscribe func(), err error) {
	lc, err := p.Get(ctx, cluster)
	if err != nil {
		return nil, err
	}
	sub, err := lc.nc.Subscribe(subject, onMsg)
	if err != nil {
		return nil, fmt.Errorf("subscribe to %s: %w", subject, err)
	}
	return func() { _ = sub.Unsubscribe() }, nil
}
