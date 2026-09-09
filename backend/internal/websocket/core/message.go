package core

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"time"
)

type MessageType string

type Message interface {
	Type() MessageType
	Marshal() ([]byte, error)
}

type BaseMessage struct {
	MessageType MessageType            `json:"type"`
	ID          string                 `json:"id,omitempty"`
	Timestamp   time.Time              `json:"timestamp"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

func (m BaseMessage) Type() MessageType {
	return m.MessageType
}

func (m BaseMessage) Marshal() ([]byte, error) {
	return jsonv2.Marshal(m)
}

type IncomingMessage struct {
	BaseMessage
	Payload json.RawMessage `json:"payload"`
}

func (m *IncomingMessage) UnmarshalPayload(v interface{}) error {
	return jsonv2.Unmarshal(m.Payload, v)
}

type OutgoingMessage struct {
	BaseMessage
	Payload interface{} `json:"payload,omitempty"`
}

func NewOutgoingMessage(msgType MessageType, payload interface{}) *OutgoingMessage {
	return &OutgoingMessage{
		BaseMessage: BaseMessage{
			MessageType: msgType,
			Timestamp:   time.Now(),
		},
		Payload: payload,
	}
}

func (m OutgoingMessage) Marshal() ([]byte, error) {
	return jsonv2.Marshal(m)
}

type ErrorMessage struct {
	BaseMessage
	Error   string      `json:"error"`
	Code    string      `json:"code,omitempty"`
	Details interface{} `json:"details,omitempty"`
}

func NewErrorMessage(err error, code string) *ErrorMessage {
	return &ErrorMessage{
		BaseMessage: BaseMessage{
			MessageType: "error",
			Timestamp:   time.Now(),
		},
		Error: err.Error(),
		Code:  code,
	}
}

func MarshalMessage(msg interface{}) ([]byte, error) {
	return jsonv2.Marshal(msg)
}
