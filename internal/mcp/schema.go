package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// SchemaCache wraps a file on disk holding the tools/list result plus metadata
// (server version + obtained_at). The filename embeds server version so version
// bumps invalidate the cache for free.
type SchemaCache struct {
	Path string
	TTL  time.Duration
}

type cachedSchema struct {
	ObtainedAt    time.Time        `json:"obtained_at"`
	ServerVersion string           `json:"server_version"`
	Tools         []ToolDefinition `json:"tools"`
}

func (s SchemaCache) Load() (*cachedSchema, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out cachedSchema
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse tools cache: %w", err)
	}
	return &out, nil
}

func (s SchemaCache) Save(cs *cachedSchema) error {
	data, err := json.MarshalIndent(cs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path, data, 0o600)
}

func (s SchemaCache) IsFresh(cs *cachedSchema) bool {
	if cs == nil || cs.ObtainedAt.IsZero() {
		return false
	}
	if s.TTL <= 0 {
		return false
	}
	return time.Since(cs.ObtainedAt) < s.TTL
}

// LoadOrFetch returns cached tools when fresh, otherwise re-fetches from the
// server and writes the result. force=true skips the freshness check.
func (s SchemaCache) LoadOrFetch(ctx context.Context, c *Client, force bool) ([]ToolDefinition, error) {
	if !force {
		cs, err := s.Load()
		if err == nil && s.IsFresh(cs) {
			return cs.Tools, nil
		}
	}

	init, err := c.Initialize(ctx)
	if err != nil {
		return nil, err
	}
	list, err := c.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	cs := &cachedSchema{
		ObtainedAt:    time.Now(),
		ServerVersion: init.ServerInfo.Version,
		Tools:         list.Tools,
	}
	if err := s.Save(cs); err != nil {
		return nil, err
	}
	return list.Tools, nil
}
