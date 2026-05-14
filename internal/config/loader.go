package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"time"

	"github.com/hashicorp/consul/api"
)

// Loader fetches DHCP configuration from Consul KV and watches it for
// changes via blocking queries.
type Loader struct {
	client    *api.Client
	configKey string
	holder    *Holder
	log       *slog.Logger
}

// NewLoader returns a Loader for <kvPrefix>/config.
func NewLoader(client *api.Client, kvPrefix string, log *slog.Logger) *Loader {
	return &Loader{
		client:    client,
		configKey: path.Join(kvPrefix, "config"),
		log:       log,
	}
}

// LoadInitial fetches the configuration once at startup. Returns an
// error if the key does not exist or the value fails validation.
// Returns the initialized Holder and the ModifyIndex of the loaded value.
func (l *Loader) LoadInitial(ctx context.Context) (*Holder, uint64, error) {
	pair, _, err := l.client.KV().Get(l.configKey, (&api.QueryOptions{}).WithContext(ctx))
	if err != nil {
		return nil, 0, fmt.Errorf("consul get %s: %w", l.configKey, err)
	}
	if pair == nil {
		return nil, 0, fmt.Errorf("config key %s does not exist in Consul KV", l.configKey)
	}
	cfg, err := Parse(pair.Value)
	if err != nil {
		return nil, 0, fmt.Errorf("validate config at %s: %w", l.configKey, err)
	}
	l.holder = NewHolder(cfg)
	return l.holder, pair.ModifyIndex, nil
}

// Watch blocks until ctx is canceled, replacing the Holder's value
// whenever the Consul KV entry's ModifyIndex changes and the new value
// is valid. Invalid values are logged and ignored; the previous valid
// value continues to be served.
func (l *Loader) Watch(ctx context.Context, startIndex uint64) error {
	if l.holder == nil {
		return errors.New("Watch called before LoadInitial")
	}

	waitIndex := startIndex
	for {
		if ctx.Err() != nil {
			return nil
		}
		opts := (&api.QueryOptions{
			WaitIndex: waitIndex,
			WaitTime:  10 * time.Minute,
		}).WithContext(ctx)

		pair, meta, err := l.client.KV().Get(l.configKey, opts)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			l.log.Warn("config watch failed; retrying", "err", err, "key", l.configKey)
			time.Sleep(5 * time.Second)
			continue
		}

		// Blocking-query wait returns when LastIndex increases. If the
		// LastIndex is the same as our WaitIndex, this was just a timeout.
		if meta == nil || meta.LastIndex == waitIndex {
			continue
		}
		waitIndex = meta.LastIndex

		if pair == nil {
			l.log.Warn("config key disappeared from KV; keeping previous config",
				"key", l.configKey,
			)
			continue
		}

		cfg, err := Parse(pair.Value)
		if err != nil {
			l.log.Warn("config update rejected; keeping previous config",
				"key", l.configKey,
				"attempted_modify_index", pair.ModifyIndex,
				"err", err,
			)
			continue
		}

		l.log.Info("config reloaded", "modify_index", pair.ModifyIndex)
		l.holder.Set(cfg)
	}
}
