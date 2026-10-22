// Package storage defines the collector's persistence boundaries.
package storage

import (
	"context"
	"errors"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
)

var ErrNotFound = errors.New("host not found")
var ErrConflict = errors.New("snapshot identity conflicts with stored data")

type Host struct {
	ID       string           `json:"id"`
	Latest   metrics.Snapshot `json:"latest"`
	LastSeen time.Time        `json:"last_seen"`
	Status   string           `json:"status"`
}

type Query struct {
	From, To time.Time
	Before   *time.Time
	Limit    int
}

type History interface {
	Ping(context.Context) error
	Accept(context.Context, metrics.Snapshot, time.Time) (Host, bool, error)
	Host(context.Context, string) (Host, error)
	List(context.Context, string, int) ([]Host, error)
	Query(context.Context, string, Query) ([]metrics.Snapshot, error)
}

type State struct {
	Latest *metrics.Snapshot
	Online bool
}

type Cache interface {
	Ping(context.Context) error
	Put(context.Context, Host) error
	States(context.Context, []string) (map[string]State, error)
}
