package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"github.com/ocman/ocman/sdk/plugin"
)

// Item is one synthetic catalog entry. The data is static sample content;
// the example performs no network or filesystem operations.
type Item struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Status   string `json:"status"`
	Priority string `json:"priority"`
	Owner    string `json:"owner"`
	Notes    string `json:"notes"`
}

var statuses = []string{"draft", "active", "retired"}

func sampleItems() []Item {
	return []Item{
		{ID: "lamp", Name: "Desk lamp", Category: "Furniture", Status: "active", Priority: "low", Owner: "Facilities", Notes: "Warm white, dimmable."},
		{ID: "router", Name: "Office router", Category: "Network", Status: "active", Priority: "high", Owner: "IT", Notes: "Replace before warranty ends."},
		{ID: "projector", Name: "Meeting room projector", Category: "Audio visual", Status: "draft", Priority: "medium", Owner: "Facilities", Notes: "Awaiting quote."},
		{ID: "badge-printer", Name: "Badge printer", Category: "Security", Status: "retired", Priority: "info", Owner: "Reception", Notes: "Kept for spare parts."},
	}
}

// Catalog is the mutex-guarded in-memory store behind the ui.v1 methods.
type Catalog struct {
	mu    sync.Mutex
	items []Item
}

func NewCatalog() *Catalog { return &Catalog{items: sampleItems()} }

func (c *Catalog) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = sampleItems()
}

// Call implements the plugin-defined methods the UI uses.
func (c *Catalog) Call(_ context.Context, method string, params json.RawMessage) (any, error) {
	var in struct {
		ID     string `json:"id"`
		Query  string `json:"query"`
		Status string `json:"status"`
	}
	if len(params) > 0 && json.Unmarshal(params, &in) != nil {
		return nil, plugin.Failure(plugin.ErrorInvalidArgument)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch method {
	case "items.list":
		query := strings.ToLower(strings.TrimSpace(in.Query))
		items := []Item{}
		for _, item := range c.items {
			if query == "" || strings.Contains(strings.ToLower(item.Name+" "+item.Category+" "+item.Owner), query) {
				items = append(items, item)
			}
		}
		return map[string]any{"items": items}, nil
	case "items.get":
		if i := c.index(in.ID); i >= 0 {
			return c.items[i], nil
		}
		return nil, plugin.Failure(plugin.ErrorNotFound)
	case "items.setStatus":
		i := c.index(in.ID)
		if i < 0 {
			return nil, plugin.Failure(plugin.ErrorNotFound)
		}
		if !slices.Contains(statuses, in.Status) {
			return nil, plugin.Failure(plugin.ErrorInvalidArgument)
		}
		c.items[i].Status = in.Status
		return c.items[i], nil
	}
	return nil, plugin.Failure(plugin.ErrorNotFound)
}

func (c *Catalog) index(id string) int {
	return slices.IndexFunc(c.items, func(item Item) bool { return item.ID == id })
}
