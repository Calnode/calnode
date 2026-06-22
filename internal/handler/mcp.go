package handler

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/calnode/calnode/internal/buildinfo"
)

// MCPServer builds the Model Context Protocol server exposing Calnode's booking
// operations as typed tools (PRD §11). The same server instance is served over both
// transports: stdio (the `calnode mcp` subcommand, for local agents) and Streamable
// HTTP (mounted at POST /mcp behind API-key auth, for remote agents). Tools call the
// same internal services the REST handlers use — no parallel code path.
func (h *Handler) MCPServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "calnode",
		Title:   "Calnode booking",
		Version: buildinfo.Get().Version,
	}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_event_types",
		Description: "List the bookable event types in this workspace (active and public). Returns each type's slug (use it as event_type_id in other tools), name, duration, and location.",
	}, h.mcpListEventTypes)

	return s
}

// ── list_event_types ─────────────────────────────────────────────────────────

type listEventTypesIn struct{}

type eventTypeBrief struct {
	ID              string `json:"id" jsonschema:"the event type's slug — pass this as event_type_id to other tools"`
	Name            string `json:"name"`
	DurationMinutes int    `json:"duration_minutes"`
	LocationType    string `json:"location_type"`
	Description     string `json:"description,omitempty"`
}

type listEventTypesOut struct {
	EventTypes []eventTypeBrief `json:"event_types"`
}

func (h *Handler) mcpListEventTypes(ctx context.Context, _ *mcp.CallToolRequest, _ listEventTypesIn) (*mcp.CallToolResult, listEventTypesOut, error) {
	rows, err := h.db.QueryContext(ctx, `
		SELECT slug, name, duration_minutes, location_type, COALESCE(description, '')
		FROM event_types
		WHERE is_active = 1 AND is_public = 1
		ORDER BY name`)
	if err != nil {
		return nil, listEventTypesOut{}, fmt.Errorf("list event types: %w", err)
	}
	defer rows.Close()
	out := listEventTypesOut{EventTypes: []eventTypeBrief{}}
	for rows.Next() {
		var e eventTypeBrief
		if err := rows.Scan(&e.ID, &e.Name, &e.DurationMinutes, &e.LocationType, &e.Description); err != nil {
			return nil, listEventTypesOut{}, fmt.Errorf("list event types: scan: %w", err)
		}
		out.EventTypes = append(out.EventTypes, e)
	}
	return nil, out, rows.Err()
}
