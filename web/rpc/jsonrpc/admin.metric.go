package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/internal/metricstore"
	"github.com/komari-monitor/komari/pkg/metric"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// admin.metric.go
// Shared PostgreSQL metric-table RPC methods (admin namespace).

func init() {
	reg("listMetricDefinitions", adminListMetricDefinitions, "List metric definitions and retention policies")
	reg("updateMetricDefinition", adminUpdateMetricDefinition, "Update a metric definition")
}

type metricDefinitionResponse struct {
	Name          string            `json:"name"`
	Description   any               `json:"description,omitempty"`
	Type          string            `json:"type"`
	Unit          string            `json:"unit,omitempty"`
	RetentionDays int               `json:"retention_days"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

func metricDescriptionValue(raw string) any {
	desc := strings.TrimSpace(raw)
	if desc == "" {
		return ""
	}
	var dict map[string]string
	if err := json.Unmarshal([]byte(desc), &dict); err == nil && len(dict) > 0 {
		return dict
	}
	return raw
}

func adminListMetricDefinitions(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	defs, err := metricstore.GetMetricDefinitions(ctx)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to list metric definitions: "+err.Error(), nil)
	}
	out := make([]metricDefinitionResponse, 0, len(defs))
	for _, def := range defs {
		out = append(out, metricDefinitionResponse{
			Name:          def.Name,
			Description:   metricDescriptionValue(def.Description),
			Type:          string(def.Type),
			Unit:          def.Unit,
			RetentionDays: def.RetentionDays,
			Metadata:      def.Metadata,
			CreatedAt:     def.CreatedAt,
			UpdatedAt:     def.UpdatedAt,
		})
	}
	return out, nil
}

func adminUpdateMetricDefinition(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Name          string `json:"name"`
		RetentionDays int    `json:"retention_days"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	params.Name = strings.TrimSpace(params.Name)
	if params.Name == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "name is required", nil)
	}
	if params.RetentionDays < 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "retention_days must be a non-negative integer", nil)
	}
	store := metricstore.GetStore()
	if store == nil {
		return nil, rpc.MakeError(rpc.InternalError, "metric store not initialized", nil)
	}
	def, err := store.UpdateMetricRetention(ctx, params.Name, params.RetentionDays)
	if errors.Is(err, metric.ErrNotFound) {
		return nil, rpc.MakeError(rpc.InvalidParams, "metric not found: "+params.Name, nil)
	}
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to update metric definition: "+err.Error(), nil)
	}
	metricstore.InvalidateMetricDefinitions(ctx)
	if params.RetentionDays == 0 {
		metricstore.DeleteMetricDataAsync(params.Name)
	}

	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "update metric definition: "+params.Name, "info")

	return metricDefinitionResponse{
		Name:          def.Name,
		Description:   metricDescriptionValue(def.Description),
		Type:          string(def.Type),
		Unit:          def.Unit,
		RetentionDays: def.RetentionDays,
		Metadata:      def.Metadata,
		CreatedAt:     def.CreatedAt,
		UpdatedAt:     def.UpdatedAt,
	}, nil
}
