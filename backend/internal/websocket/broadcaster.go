package websocket

import (
	jsonv2 "encoding/json/v2"
	"github.com/kanivet/backend/internal/k8s/watcher"
	"github.com/kanivet/backend/internal/websocket/core"
	"sync"
	"sync/atomic"
	"time"
)

type WatcherBroadcaster struct {
	hub         *core.Hub
	batcher     *EventBatcher
	useBatching atomic.Bool
}

func NewWatcherBroadcaster(hub *core.Hub) *WatcherBroadcaster {
	batcher := NewEventBatcher(hub, 250*time.Millisecond, 100)
	wb := &WatcherBroadcaster{
		hub:     hub,
		batcher: batcher,
	}
	wb.useBatching.Store(true)
	return wb
}

func (wb *WatcherBroadcaster) Broadcast(topic string, message watcher.Message) error {
	if !wb.hub.HasSubscribers(topic) {
		return nil
	}
	coreMsg := &WatcherMessage{
		BaseMessage: core.BaseMessage{MessageType: "event"},
		msgData:     message.GetData(),
	}
	if wb.useBatching.Load() {
		return wb.batcher.AddEvent(topic, coreMsg)
	}
	return wb.hub.Broadcast(topic, coreMsg)
}

func messageType(msgData map[string]interface{}) core.MessageType {
	switch t := msgData["type"].(type) {
	case core.MessageType:
		return t
	case string:
		return core.MessageType(t)
	default:
		return "event"
	}
}

func (wb *WatcherBroadcaster) BroadcastDirect(topic string, message watcher.Message) error {
	if !wb.hub.HasSubscribers(topic) {
		return nil
	}
	msgData := message.GetData()
	coreMsg := &WatcherMessage{
		BaseMessage: core.BaseMessage{MessageType: messageType(msgData)},
		msgData:     msgData,
	}
	return wb.hub.Broadcast(topic, coreMsg)
}

func (wb *WatcherBroadcaster) BroadcastAll(message watcher.Message) error {
	msgData := message.GetData()
	coreMsg := &WatcherMessage{
		BaseMessage: core.BaseMessage{MessageType: messageType(msgData)},
		msgData:     msgData,
	}
	return wb.hub.BroadcastAll(coreMsg)
}

func (wb *WatcherBroadcaster) SetBatching(enabled bool) {
	wb.useBatching.Store(enabled)
}

func (wb *WatcherBroadcaster) FlushTopic(topic string) error {
	if wb.batcher != nil {
		return wb.batcher.FlushTopic(topic)
	}
	return nil
}

func (wb *WatcherBroadcaster) SetSortPreference(topic, sortBy, sortOrder string) {
	if wb.batcher != nil {
		wb.batcher.SetSortPreference(topic, sortBy, sortOrder)
	}
}

func (wb *WatcherBroadcaster) GetSortPreference(topic string) (sortBy, sortOrder string) {
	if wb.batcher != nil {
		return wb.batcher.GetSortPreference(topic)
	}
	return "age", "desc"
}

func (wb *WatcherBroadcaster) CleanupTopic(topic string) {
	if wb.batcher != nil {
		wb.batcher.CleanupTopic(topic)
	}
}

func (wb *WatcherBroadcaster) Shutdown() {
	if wb.batcher != nil {
		wb.batcher.Shutdown()
	}
}

type WatcherMessage struct {
	core.BaseMessage
	msgData    map[string]interface{}
	cacheOnce  sync.Once
	cachedData []byte
	cachedErr  error
}

func (wm *WatcherMessage) Marshal() ([]byte, error) {
	wm.cacheOnce.Do(func() {
		wm.cachedData, wm.cachedErr = jsonv2.Marshal(wm.msgData)
	})
	return wm.cachedData, wm.cachedErr
}

func (wm *WatcherMessage) GetData() map[string]interface{} {
	return wm.msgData
}
