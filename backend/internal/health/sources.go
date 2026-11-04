package health

import (
	"context"
	"log"
	"sync"
)

// SignalSource lets another feature contribute issues (for example expiring
// certificates) without the health package importing it. Sources are called
// on every Issues evaluation and should be cheap or cached; an error only
// drops that source's signals.
type SignalSource func(ctx context.Context, cluster string) ([]Signal, error)

var (
	sourcesMu sync.RWMutex
	sources   = map[string]SignalSource{}
)

// RegisterSignalSource adds (or replaces) a named external source.
func RegisterSignalSource(name string, src SignalSource) {
	sourcesMu.Lock()
	defer sourcesMu.Unlock()
	sources[name] = src
}

func externalSignals(ctx context.Context, cluster string) []Signal {
	sourcesMu.RLock()
	defer sourcesMu.RUnlock()
	var out []Signal
	for name, src := range sources {
		sigs, err := src(ctx, cluster)
		if err != nil {
			log.Printf("[health] signal source %s: %v", name, err)
			continue
		}
		out = append(out, sigs...)
	}
	return out
}

func init() {
	// Signals from external sources are reported as they are.
	RegisterRule(Rule{ID: "external", Eval: func(s *Snapshot, _ *owners, emit func(Signal)) {
		for _, sig := range s.External {
			if sig.Group.Name == "" {
				sig.Group = sig.Object
			}
			emit(sig)
		}
	}})
}
