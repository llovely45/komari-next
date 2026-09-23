package messageSender

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/komari-monitor/komari/database"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/utils/item"
	"github.com/komari-monitor/komari/utils/messageSender/factory"
	"gorm.io/gorm"
)

// NotificationChannelInfo is the channel metadata consumed by the admin UI.
type NotificationChannelInfo struct {
	ID            string               `json:"id"`
	Configuration models.Configuration `json:"configuration"`
}

// ListNotificationChannels adapts the registered message senders to the
// notification-channel shape expected by the current admin frontend.
func ListNotificationChannels() []NotificationChannelInfo {
	providers := factory.GetSenderConfigs()
	names := make([]string, 0, len(providers))
	for name := range providers {
		if name != "empty" {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	channels := make([]NotificationChannelInfo, 0, len(names))
	for _, name := range names {
		channels = append(channels, NotificationChannelInfo{
			ID:            name,
			Configuration: senderConfiguration(name, providers[name]),
		})
	}
	return channels
}

// NotificationChannelRegistered reports whether a selectable sender exists.
func NotificationChannelRegistered(id string) bool {
	if id == "" || id == "empty" {
		return false
	}
	_, exists := factory.GetSenderConfigs()[id]
	return exists
}

// GetNotificationChannelConfiguration returns the managed form definition and
// saved values for one registered sender.
func GetNotificationChannelConfiguration(id string) (models.Configuration, map[string]any, bool, error) {
	providers := factory.GetSenderConfigs()
	fields, exists := providers[id]
	if !exists || id == "empty" {
		return models.Configuration{}, nil, false, nil
	}

	values := make(map[string]any, len(fields))
	for _, field := range fields {
		if value := senderFieldDefault(field); value != nil {
			values[field.Name] = value
		}
	}

	saved, err := database.GetMessageSenderConfigByName(id)
	if err == nil {
		if strings.TrimSpace(saved.Addition) != "" {
			if err := json.Unmarshal([]byte(saved.Addition), &values); err != nil {
				return models.Configuration{}, nil, true, err
			}
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Configuration{}, nil, true, err
	}

	return senderConfiguration(id, fields), values, true, nil
}

// SaveNotificationChannelConfiguration persists settings for a registered
// sender. The caller reloads it when it is the currently active channel.
func SaveNotificationChannelConfiguration(id string, values map[string]any) error {
	if !NotificationChannelRegistered(id) {
		return gorm.ErrRecordNotFound
	}
	if values == nil {
		values = map[string]any{}
	}
	addition, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return database.SaveMessageSenderConfig(&models.MessageSenderProvider{
		Name:     id,
		Addition: string(addition),
	})
}

func senderConfiguration(id string, fields []item.Item) models.Configuration {
	data := make([]models.ManagedThemeConfigurationItem, 0, len(fields))
	for _, field := range fields {
		data = append(data, models.ManagedThemeConfigurationItem{
			Key:      field.Name,
			Name:     humanizeSenderField(field.Name),
			Required: field.Required,
			Type:     senderFieldType(field.Type),
			Options:  field.Options,
			Default:  senderFieldDefault(field),
			Help:     field.Help,
		})
	}
	return models.Configuration{
		Type: models.ThemeConfigurationManaged,
		Name: senderDisplayName(id),
		Data: data,
	}
}

func senderDisplayName(id string) string {
	if name, exists := map[string]string{
		"bark":            "Bark",
		"email":           "Email",
		"serverchan3":     "Server 酱³",
		"serverchanturbo": "Server 酱 Turbo",
		"telegram":        "Telegram",
		"webhook":         "Webhook",
		"Javascript":      "JavaScript",
	}[id]; exists {
		return name
	}
	return humanizeSenderField(id)
}

func humanizeSenderField(value string) string {
	words := strings.FieldsFunc(value, func(r rune) bool {
		return r == '_' || r == '-' || unicode.IsSpace(r)
	})
	for i, word := range words {
		runes := []rune(word)
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
			words[i] = string(runes)
		}
	}
	return strings.Join(words, " ")
}

func senderFieldType(fieldType string) string {
	switch strings.ToLower(fieldType) {
	case "option":
		return "select"
	case "richtext":
		return "richtext"
	case "bool", "boolean", "switch":
		return "switch"
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64", "number":
		return "number"
	default:
		return "string"
	}
}

func senderFieldDefault(field item.Item) any {
	if field.Default == "" {
		return nil
	}
	switch strings.ToLower(field.Type) {
	case "bool", "boolean", "switch":
		value, err := strconv.ParseBool(field.Default)
		if err == nil {
			return value
		}
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		value, err := strconv.ParseInt(field.Default, 10, 64)
		if err == nil {
			return value
		}
	case "float32", "float64", "number":
		value, err := strconv.ParseFloat(field.Default, 64)
		if err == nil {
			return value
		}
	}
	return field.Default
}
