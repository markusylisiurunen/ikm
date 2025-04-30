package model

import (
	"context"
	"encoding/json"
)

type Tool interface {
	Spec() (string, string, json.RawMessage)
	Call(ctx context.Context, args string) (string, error)
}
