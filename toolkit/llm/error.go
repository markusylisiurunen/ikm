package llm

import (
	"encoding/json"
	"fmt"
)

type StreamError struct {
	Code     int
	Message  string
	Metadata map[string]any
}

func (e StreamError) Error() string {
	meta := []byte("null")
	if e.Metadata != nil {
		b, err := json.Marshal(e.Metadata)
		if err != nil {
			panic(err)
		}
		meta = b
	}
	return fmt.Sprintf("%s (%d): %s", e.Message, e.Code, meta)
}
