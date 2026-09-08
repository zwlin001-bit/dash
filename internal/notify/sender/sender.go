package sender

import (
	"context"
	"errors"
)

// ChannelConfig contains non-sensitive channel properties.
type ChannelConfig struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	ConfigJSON string `json:"config_json"`
}

// Sender defines the driver interface for delivering messages to external notification channels.
type Sender interface {
	Kind() string
	Send(ctx context.Context, cfg *ChannelConfig, secret string, text string) error
}

// NonRetriableError wraps an error that should not be retried (e.g. 401 Bad Token, 404 Chat Not Found).
type NonRetriableError struct {
	Err error
}

func (e *NonRetriableError) Error() string {
	return e.Err.Error()
}

func (e *NonRetriableError) Unwrap() error {
	return e.Err
}

// MarkNonRetriable wraps an error as non-retriable.
func MarkNonRetriable(err error) error {
	if err == nil {
		return nil
	}
	return &NonRetriableError{Err: err}
}

// IsNonRetriable checks if an error is marked non-retriable.
func IsNonRetriable(err error) bool {
	if err == nil {
		return false
	}
	var nre *NonRetriableError
	return errors.As(err, &nre)
}
