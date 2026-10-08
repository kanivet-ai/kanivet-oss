package watcher

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kanivet/backend/internal/topics"
)

// ObjectSink is offered every cluster-wide watch of the kind it is registered
// for, so that a second reader of that kind's objects shares the list and the
// watch that already feed its list rows instead of running its own.
type ObjectSink interface {
	// Attach is called when such a watch starts. The feed it returns gets
	// the watch's objects until the watch ends.
	Attach(cluster string) ObjectFeed
}

// ObjectFeed receives one watch's objects, typed where the watch decodes them
// to a typed adapter's kind (*corev1.Pod) and as *unstructured.Unstructured
// otherwise. Its methods are called one at a time.
type ObjectFeed interface {
	// Reset opens a full listing. When Synced follows, every object not Put
	// since Reset is gone from the cluster.
	Reset()
	Put(obj runtime.Object)
	Delete(obj runtime.Object)
	// Synced closes a listing that ran to its end.
	Synced()
	// Close ends the feed: the watch stopped and nothing keeps what it gave
	// current any more.
	Close()
}

// noFeed is the feed of a topic no sink reads.
type noFeed struct{}

func (noFeed) Reset()                {}
func (noFeed) Put(runtime.Object)    {}
func (noFeed) Delete(runtime.Object) {}
func (noFeed) Synced()               {}
func (noFeed) Close()                {}

// topicFeed is a running watch's feed. owner is the context of that watch, as
// in syncStatus: a stopped watch that is still unwinding must not write to
// the feed of the watch that replaced it.
type topicFeed struct {
	feed  ObjectFeed
	owner context.Context
}

func sinkKey(group, version, kind string) string {
	return group + "/" + version + "/" + kind
}

// SetObjectSink registers the sink for a kind, named as StartWatch names it.
// It is meant to be called before any watch of that kind starts.
func (s *Service) SetObjectSink(group, version, kind string, sink ObjectSink) {
	s.feedMu.Lock()
	defer s.feedMu.Unlock()
	if s.sinks == nil {
		s.sinks = make(map[string]ObjectSink)
	}
	s.sinks[sinkKey(group, version, kind)] = sink
}

// openFeed attaches the topic's sink, if it has one, to the watch running
// under ctx, and returns what to call when that watch ends.
func (s *Service) openFeed(ctx context.Context, topic string) (closeFeed func()) {
	cluster, group, version, kind, namespace, ok := topics.ParseItemsTopic(topic)
	if !ok || namespace != "" {
		return func() {}
	}
	s.feedMu.Lock()
	sink := s.sinks[sinkKey(group, version, kind)]
	s.feedMu.Unlock()
	if sink == nil {
		return func() {}
	}
	feed := sink.Attach(cluster)
	s.feedMu.Lock()
	if s.feeds == nil {
		s.feeds = make(map[string]topicFeed)
	}
	s.feeds[topic] = topicFeed{feed: feed, owner: ctx}
	s.feedMu.Unlock()
	return func() {
		s.feedMu.Lock()
		if s.feeds[topic].owner == ctx {
			delete(s.feeds, topic)
		}
		s.feedMu.Unlock()
		feed.Close()
	}
}

// feedFor returns the feed of the watch running under ctx.
func (s *Service) feedFor(ctx context.Context, topic string) ObjectFeed {
	s.feedMu.RLock()
	tf, ok := s.feeds[topic]
	s.feedMu.RUnlock()
	if !ok || tf.owner != ctx {
		return noFeed{}
	}
	return tf.feed
}

// Retain keeps a kind's cluster-wide watch running for a reader that takes
// its objects through an ObjectSink, starting the watch if nothing else has.
// Unlike StartWatch it sends subscribers nothing. Release gives it back.
func (s *Service) Retain(cluster, group, version, kind string) error {
	_, err := s.acquireWatch(cluster, group, version, kind, "")
	return err
}

func (s *Service) Release(cluster, group, version, kind string) {
	s.StopWatch(cluster, group, version, kind, "")
}
