package rightsizing

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
	"time"
)

// replayAnswer is one answer from a KANIVET_LIMITER_TRACE=1 log.
type replayAnswer struct {
	at                time.Time
	class             string
	latency, server   time.Duration
	bytes             int64
	loggedLimit       float64
	loggedLull        bool
	loggedGradientAbs float64
}

var replayLine = regexp.MustCompile(`^(\d{4}/\d\d/\d\d \d\d:\d\d:\d\d) .*\[LIMITER\] class=("[^"]*") latency=(\S+) server=(\S+) bytes=(\d+) limit=([\d.]+) inflight=\d+ ratio=[\d.]+ gradient=([\d.]+) warm=\w+ lull=(\w+)`)

func readReplay(path string) ([]replayAnswer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []replayAnswer
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		m := replayLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		at, err := time.ParseInLocation("2006/01/02 15:04:05", m[1], time.Local)
		if err != nil {
			continue
		}
		lat, err1 := time.ParseDuration(m[3])
		srv, err2 := time.ParseDuration(m[4])
		if err1 != nil || err2 != nil {
			continue
		}
		var bytes int64
		var lim, grad float64
		fmt.Sscan(m[5], &bytes)
		fmt.Sscan(m[6], &lim)
		fmt.Sscan(m[7], &grad)
		out = append(out, replayAnswer{at: at, class: m[2], latency: lat, server: srv, bytes: bytes, loggedLimit: lim, loggedLull: m[8] == "true", loggedGradientAbs: grad})
	}
	return out, sc.Err()
}

// TestLimiterReplay feeds the answers of a real run through the limiter as
// configured below, to compare designs on a store's actual noise:
//
//	KANIVET_LIMITER_REPLAY='/path/to/logs/*.log' go test -run TestLimiterReplay -v ./internal/rightsizing/
//
// The answers do not depend on the limit here, as they did in the run, so it
// shows how a design reads the same evidence, not how it would have changed
// it: a limit that is cut on noise, or stuck low, shows; whether the limit it
// reaches is safe is for a run against the store.
func TestLimiterReplay(t *testing.T) {
	pat := os.Getenv("KANIVET_LIMITER_REPLAY")
	if pat == "" {
		t.Skip("set KANIVET_LIMITER_REPLAY to a glob of limiter trace logs")
	}
	files, _ := filepath.Glob(pat)
	if len(files) == 0 {
		t.Fatalf("no files match %q", pat)
	}
	type variant struct {
		name   string
		signal bool
		bytes  bool
		floor  time.Duration
	}
	variants := []variant{
		{"round trip (the PR)", false, false, 0},
		{"store time, by bytes, no floor", true, true, 0},
		{"store time, by samples, cache apart, no floor", true, false, 0},
		{"... floor 10ms", true, false, 10 * time.Millisecond},
		{"... floor 25ms", true, false, 25 * time.Millisecond},
		{"... floor 50ms", true, false, 50 * time.Millisecond},
		{"... floor 100ms", true, false, 100 * time.Millisecond},
	}
	oldFloor, oldBytes := storeLatencyFloor, storeClassByBytes
	defer func() { storeLatencyFloor, storeClassByBytes = oldFloor, oldBytes }()

	fmt.Printf("%-48s %8s %8s %8s %8s %10s\n", "variant", "mean", "p10", "p50", "end", "cuts/100")
	for _, v := range variants {
		storeLatencyFloor, storeClassByBytes = v.floor, v.bytes
		var limits []float64
		var cuts, answers int
		var ends []float64
		for _, file := range files {
			trace, err := readReplay(file)
			if err != nil || len(trace) == 0 {
				continue
			}
			clock := &fakeClock{t: trace[0].at}
			l := newLimiter(clock.now, func() float64 { return 0.5 })
			prev := l.limit
			for _, a := range trace {
				if a.at.After(clock.t) {
					clock.t = a.at
				}
				// The client kept every slot it had in use, as a report does.
				l.mu.Lock()
				l.inflight = l.slots()
				l.mu.Unlock()
				o := outcome{latency: a.latency, work: 24, class: a.class}
				if v.signal {
					o.serverLatency, o.storeBytes = a.server, a.bytes
				}
				l.release(o)
				limits = append(limits, l.limit)
				if l.limit < prev-1e-9 {
					cuts++
				}
				prev = l.limit
				answers++
			}
			ends = append(ends, l.limit)
		}
		if len(limits) == 0 {
			t.Fatal("no answers in the traces")
		}
		sorted := slices.Clone(limits)
		slices.Sort(sorted)
		mean := 0.0
		for _, x := range limits {
			mean += x
		}
		mean /= float64(len(limits))
		end := 0.0
		for _, x := range ends {
			end += x
		}
		fmt.Printf("%-48s %8.2f %8.2f %8.2f %8.2f %10.1f\n", v.name, mean, sorted[len(sorted)/10], sorted[len(sorted)/2], end/math.Max(1, float64(len(ends))), 100*float64(cuts)/float64(answers))
	}
}
