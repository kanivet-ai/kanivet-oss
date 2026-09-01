package faults

import (
	"encoding/json"
	"log"
)

func Init() error {
	return nil
}

func Flush() {}

func CaptureException(err error) {
	if err != nil {
		log.Printf("[ERROR] %v", err)
	}
}

func CaptureMessage(message string) {
	if message != "" {
		log.Printf("[ERROR] %s", message)
	}
}

func CaptureExceptionWithContext(err error, extra map[string]any) {
	if err == nil {
		return
	}
	if len(extra) == 0 {
		CaptureException(err)
		return
	}
	data, marshalErr := json.Marshal(extra)
	if marshalErr != nil {
		log.Printf("[ERROR] %v | context marshal failed: %v", err, marshalErr)
		return
	}
	log.Printf("[ERROR] %v | context=%s", err, data)
}
