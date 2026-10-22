package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/config"
	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
	"github.com/RaafidAfraazG/NodeWatch/internal/storage"
	client "github.com/redis/go-redis/v9"
)

type Store struct {
	client       *client.Client
	timeout, ttl time.Duration
}

func Open(c config.Collector) *Store {
	return &Store{client: client.NewClient(&client.Options{Addr: c.RedisAddr, Username: c.RedisUsername, Password: c.RedisPassword, DB: c.RedisDB, DialTimeout: c.DependencyTimeout, ReadTimeout: c.DependencyTimeout, WriteTimeout: c.DependencyTimeout, PoolTimeout: c.DependencyTimeout, ContextTimeoutEnabled: true, MaxRetries: -1}), timeout: c.DependencyTimeout, ttl: c.HeartbeatTTL}
}
func (s *Store) Close() error { return s.client.Close() }
func (s *Store) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.client.Ping(ctx).Err()
}
func keys(id string) (string, string) {
	return "nodewatch:host:" + id + ":latest", "nodewatch:host:" + id + ":heartbeat"
}

// Fixed-width decimal nanoseconds are compared as strings: Lua doubles cannot
// represent nanosecond timestamps exactly. One script serializes concurrent writes.
var update = client.NewScript(`
local old = redis.call('HGET', KEYS[1], 'collected_ns')
if not old or ARGV[1] >= old then
  redis.call('HSET', KEYS[1], 'collected_ns', ARGV[1], 'snapshot', ARGV[2])
end
local seen = redis.call('HGET', KEYS[1], 'seen_ns')
if not seen or ARGV[3] >= seen then
  redis.call('HSET', KEYS[1], 'seen_ns', ARGV[3])
  redis.call('SET', KEYS[2], ARGV[3])
  redis.call('PEXPIREAT', KEYS[2], ARGV[4])
end
return 1
`)

func (s *Store) Put(ctx context.Context, h storage.Host) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	data, err := json.Marshal(h.Latest)
	if err != nil {
		return err
	}
	latest, heartbeat := keys(h.ID)
	return update.Run(ctx, s.client, []string{latest, heartbeat}, fmt.Sprintf("%019d", h.Latest.CollectedAt.UnixNano()), data, fmt.Sprintf("%019d", h.LastSeen.UnixNano()), h.LastSeen.Add(s.ttl).UnixMilli()).Err()
}

// States uses one bounded pipeline instead of per-host network round trips.
// The durable MySQL hosts index supplies pagination; Redis never needs KEYS/SCAN.
func (s *Store) States(ctx context.Context, ids []string) (map[string]storage.State, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	pipe := s.client.Pipeline()
	type pair struct {
		snapshot  *client.StringCmd
		heartbeat *client.IntCmd
	}
	commands := make(map[string]pair, len(ids))
	for _, id := range ids {
		latest, heartbeat := keys(id)
		commands[id] = pair{pipe.HGet(ctx, latest, "snapshot"), pipe.Exists(ctx, heartbeat)}
	}
	_, err := pipe.Exec(ctx)
	if err != nil && err != client.Nil {
		return nil, err
	}
	states := make(map[string]storage.State, len(ids))
	for id, cmd := range commands {
		if err := cmd.heartbeat.Err(); err != nil {
			return nil, err
		}
		state := storage.State{Online: cmd.heartbeat.Val() == 1}
		if cmd.snapshot.Err() != nil && cmd.snapshot.Err() != client.Nil {
			return nil, cmd.snapshot.Err()
		}
		if data, err := cmd.snapshot.Result(); err == nil {
			var m metrics.Snapshot
			if err = json.Unmarshal([]byte(data), &m); err != nil {
				return nil, err
			}
			state.Latest = &m
		}
		states[id] = state
	}
	return states, nil
}
