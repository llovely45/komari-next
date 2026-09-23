package clients

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/database/tasks"
	logger "github.com/komari-monitor/komari/utils/log"
	"gorm.io/gorm"
)

// ErrInvalidClientBackup marks a backup payload that cannot be safely restored.
var ErrInvalidClientBackup = errors.New("invalid client backup")

const maxBackupClients = 5000

// ClientRestoreResult describes the outcome for one backed-up client.
type ClientRestoreResult struct {
	UUID   string `json:"uuid"`
	Name   string `json:"name"`
	IPv4   string `json:"ipv4,omitempty"`
	Action string `json:"action"`
	Target string `json:"target_uuid,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// RestoreClients restores client records from an exported client backup.
// Existing nodes are matched by normalized IPv4 and only their administrator-
// managed metadata is updated; UUID, token, address, and agent-reported fields
// remain owned by the existing node. Nodes without an IPv4 match are inserted
// with their backed-up UUID and token, unless either identity is already used.
func RestoreClients(backup []models.Client) ([]ClientRestoreResult, error) {
	if len(backup) == 0 {
		return nil, fmt.Errorf("%w: no clients to restore", ErrInvalidClientBackup)
	}
	if len(backup) > maxBackupClients {
		return nil, fmt.Errorf("%w: client count exceeds %d", ErrInvalidClientBackup, maxBackupClients)
	}

	backupIPs := make(map[string]int, len(backup))
	seenUUIDs := make(map[string]struct{}, len(backup))
	seenTokens := make(map[string]struct{}, len(backup))
	clientUUIDKeys := make([]string, len(backup))
	clientIPs := make([]string, len(backup))
	for i, client := range backup {
		parsedUUID, err := uuid.Parse(client.UUID)
		if err != nil || strings.TrimSpace(client.UUID) == "" {
			return nil, fmt.Errorf("%w: client %d has an invalid UUID", ErrInvalidClientBackup, i+1)
		}
		if _, exists := seenUUIDs[parsedUUID.String()]; exists {
			return nil, fmt.Errorf("%w: duplicate UUID %q", ErrInvalidClientBackup, client.UUID)
		}
		clientUUIDKeys[i] = parsedUUID.String()
		seenUUIDs[clientUUIDKeys[i]] = struct{}{}
		if strings.TrimSpace(client.Token) == "" || len(client.Token) > 255 {
			return nil, fmt.Errorf("%w: client %q has a missing or oversized RPC token", ErrInvalidClientBackup, client.UUID)
		}
		stringFields := []struct {
			name  string
			value string
			limit int
		}{
			{name: "name", value: client.Name, limit: 100},
			{name: "cpu_name", value: client.CpuName, limit: 100},
			{name: "virtualization", value: client.Virtualization, limit: 50},
			{name: "arch", value: client.Arch, limit: 50},
			{name: "os", value: client.OS, limit: 100},
			{name: "kernel_version", value: client.KernelVersion, limit: 100},
			{name: "gpu_name", value: client.GpuName, limit: 100},
			{name: "ipv4", value: client.IPv4, limit: 100},
			{name: "ipv6", value: client.IPv6, limit: 100},
			{name: "region", value: client.Region, limit: 100},
			{name: "version", value: client.Version, limit: 100},
			{name: "currency", value: client.Currency, limit: 20},
			{name: "group", value: client.Group, limit: 100},
			{name: "traffic_limit_type", value: client.TrafficLimitType, limit: 10},
		}
		for _, field := range stringFields {
			if len(field.value) > field.limit {
				return nil, fmt.Errorf("%w: client %q field %s exceeds the database limit", ErrInvalidClientBackup, client.UUID, field.name)
			}
		}
		if _, exists := seenTokens[client.Token]; exists {
			return nil, fmt.Errorf("%w: duplicate RPC token in backup", ErrInvalidClientBackup)
		}
		seenTokens[client.Token] = struct{}{}

		ip := strings.TrimSpace(client.IPv4)
		if ip != "" {
			parsedIP := net.ParseIP(ip)
			if parsedIP == nil || parsedIP.To4() == nil {
				return nil, fmt.Errorf("%w: client %q has an invalid IPv4 address", ErrInvalidClientBackup, client.UUID)
			}
			ip = parsedIP.To4().String()
			backupIPs[ip]++
		}
		clientIPs[i] = ip
	}

	results := make([]ClientRestoreResult, len(backup))
	addedUUIDs := make([]string, 0)
	db := dbcore.GetDBInstance()
	err := db.Transaction(func(tx *gorm.DB) error {
		var current []models.Client
		if err := tx.Find(&current).Error; err != nil {
			return err
		}

		byIP := make(map[string][]models.Client)
		byUUID := make(map[string]struct{}, len(current))
		byToken := make(map[string]struct{}, len(current))
		for _, client := range current {
			if parsedUUID, err := uuid.Parse(client.UUID); err == nil {
				byUUID[parsedUUID.String()] = struct{}{}
			} else {
				byUUID[client.UUID] = struct{}{}
			}
			byToken[client.Token] = struct{}{}
			if ip := normalizedIPv4(client.IPv4); ip != "" {
				byIP[ip] = append(byIP[ip], client)
			}
		}

		for i, source := range backup {
			result := ClientRestoreResult{UUID: source.UUID, Name: source.Name, IPv4: source.IPv4}
			ip := clientIPs[i]
			if ip != "" && backupIPs[ip] > 1 {
				result.Action = "skipped"
				result.Reason = "备份中有多个节点使用相同 IPv4，无法确定匹配关系"
				results[i] = result
				continue
			}

			matches := byIP[ip]
			if ip != "" && len(matches) > 1 {
				result.Action = "skipped"
				result.Reason = "当前服务器列表中有多个节点使用相同 IPv4"
				results[i] = result
				continue
			}
			if ip != "" && len(matches) == 1 {
				target := matches[0]
				if err := tx.Model(&models.Client{}).Where("uuid = ?", target.UUID).Updates(restoreMetadata(source)).Error; err != nil {
					return fmt.Errorf("update client %s: %w", target.UUID, err)
				}
				result.Action = "updated"
				result.Target = target.UUID
				results[i] = result
				continue
			}

			if _, exists := byUUID[clientUUIDKeys[i]]; exists {
				result.Action = "skipped"
				result.Reason = "备份 UUID 已被不同 IPv4 的现有节点占用"
				results[i] = result
				continue
			}
			if _, exists := byToken[source.Token]; exists {
				result.Action = "skipped"
				result.Reason = "备份 RPC Token 已被现有节点占用"
				results[i] = result
				continue
			}

			created := source
			if created.CreatedAt.IsZero() {
				created.CreatedAt = time.Now().UTC()
			}
			created.UpdatedAt = time.Now().UTC()
			if err := tx.Create(&created).Error; err != nil {
				return fmt.Errorf("create client %s: %w", source.UUID, err)
			}
			byUUID[clientUUIDKeys[i]] = struct{}{}
			byToken[source.Token] = struct{}{}
			result.Action = "added"
			result.Target = source.UUID
			results[i] = result
			addedUUIDs = append(addedUUIDs, source.UUID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	for _, clientUUID := range addedUUIDs {
		if err := tasks.AddDefaultOnClientUUID(clientUUID); err != nil {
			logger.ErrorArgs("clients", "Failed to apply default-on ping tasks to restored client:", err)
		}
	}
	return results, nil
}

func normalizedIPv4(value string) string {
	parsed := net.ParseIP(strings.TrimSpace(value))
	if parsed == nil || parsed.To4() == nil {
		return ""
	}
	return parsed.To4().String()
}

func restoreMetadata(client models.Client) map[string]any {
	return map[string]any{
		"updated_at":         time.Now().UTC(),
		"name":               client.Name,
		"region":             client.Region,
		"remark":             client.Remark,
		"public_remark":      client.PublicRemark,
		"weight":             client.Weight,
		"price":              client.Price,
		"billing_cycle":      client.BillingCycle,
		"auto_renewal":       client.AutoRenewal,
		"currency":           client.Currency,
		"expired_at":         client.ExpiredAt,
		"group":              client.Group,
		"tags":               client.Tags,
		"hidden":             client.Hidden,
		"traffic_limit":      client.TrafficLimit,
		"traffic_limit_type": client.TrafficLimitType,
	}
}
