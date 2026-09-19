// Package pluginprocess contains the versioned wire model shared by the
// future Go plugin host and external Go plugin processes. It intentionally has
// no process, socket or database dependency.
package pluginprocess

import (
	"fmt"
	"sort"
	"strings"
)

// ProtocolVersion is the version of the plugin process handshake and message
// envelope. A breaking wire change must increment it.
const ProtocolVersion int = 1

// Capability is a host capability requested by an external plugin.
type Capability string

const (
	CapabilityEvents  Capability = "events"
	CapabilityRPC     Capability = "rpc"
	CapabilityRoutes  Capability = "routes"
	CapabilityMetrics Capability = "metrics"
	CapabilityExec    Capability = "exec"
)

// Handshake is the first message exchanged when an external plugin starts.
type Handshake struct {
	ProtocolVersion       int          `json:"protocol_version"`
	PluginID              string       `json:"plugin_id"`
	PluginVersion         string       `json:"plugin_version"`
	KomariAPIVersion      string       `json:"komari_api_version"`
	RequestedCapabilities []Capability `json:"requested_capabilities"`
	ApprovedCapabilities  []Capability `json:"approved_capabilities"`
}

// ValidateHandshake validates a plugin request and returns a copy containing
// the approved subset. The caller supplies the capabilities approved by the
// administrator for this plugin.
func ValidateHandshake(handshake Handshake, approved []Capability) (Handshake, error) {
	if handshake.ProtocolVersion != ProtocolVersion {
		return Handshake{}, newPluginError(
			"invalid_protocol_version",
			fmt.Sprintf("unsupported plugin protocol version %d; want %d", handshake.ProtocolVersion, ProtocolVersion),
		)
	}
	if strings.TrimSpace(handshake.PluginID) == "" {
		return Handshake{}, newPluginError("invalid_plugin_id", "plugin id is empty")
	}
	if strings.TrimSpace(handshake.PluginVersion) == "" {
		return Handshake{}, newPluginError("invalid_plugin_version", "plugin version is empty")
	}
	if strings.TrimSpace(handshake.KomariAPIVersion) == "" {
		return Handshake{}, newPluginError("invalid_komari_api_version", "komari API version is empty")
	}

	allowed, err := validateCapabilityList(approved, "administrator-approved")
	if err != nil {
		return Handshake{}, err
	}
	requested, err := validateCapabilityList(handshake.RequestedCapabilities, "requested")
	if err != nil {
		return Handshake{}, err
	}

	selected := make(map[Capability]struct{}, len(requested))
	for capability := range requested {
		if _, ok := allowed[capability]; !ok {
			return Handshake{}, newCapabilityError(
				capability,
				"capability_not_approved",
				fmt.Sprintf("plugin capability %q is not approved", capability),
			)
		}
		selected[capability] = struct{}{}
	}

	handshake.PluginID = strings.TrimSpace(handshake.PluginID)
	handshake.PluginVersion = strings.TrimSpace(handshake.PluginVersion)
	handshake.KomariAPIVersion = strings.TrimSpace(handshake.KomariAPIVersion)
	handshake.ApprovedCapabilities = make([]Capability, 0, len(selected))
	for capability := range selected {
		handshake.ApprovedCapabilities = append(handshake.ApprovedCapabilities, capability)
	}
	sort.Slice(handshake.ApprovedCapabilities, func(i, j int) bool {
		return handshake.ApprovedCapabilities[i] < handshake.ApprovedCapabilities[j]
	})
	return handshake, nil
}

func validateCapabilityList(capabilities []Capability, source string) (map[Capability]struct{}, error) {
	seen := make(map[Capability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		value := string(capability)
		if strings.TrimSpace(value) == "" {
			return nil, newCapabilityError(
				capability,
				"invalid_capability",
				fmt.Sprintf("%s capability is empty", source),
			)
		}
		if value != strings.TrimSpace(value) {
			return nil, newCapabilityError(
				capability,
				"invalid_capability",
				fmt.Sprintf("%s capability %q contains surrounding whitespace", source, capability),
			)
		}
		if !isKnownCapability(capability) {
			return nil, newCapabilityError(
				capability,
				"unknown_capability",
				fmt.Sprintf("%s capability %q is unknown", source, capability),
			)
		}
		if _, ok := seen[capability]; ok {
			return nil, newCapabilityError(
				capability,
				"duplicate_capability",
				fmt.Sprintf("%s capability %q is duplicated", source, capability),
			)
		}
		seen[capability] = struct{}{}
	}
	return seen, nil
}

func isKnownCapability(capability Capability) bool {
	switch capability {
	case CapabilityEvents, CapabilityRPC, CapabilityRoutes, CapabilityMetrics, CapabilityExec:
		return true
	default:
		return false
	}
}

// CapabilityError identifies a capability validation or approval failure.
type CapabilityError struct {
	Capability Capability `json:"capability"`
	PluginError
}

func (e *CapabilityError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("plugin capability %q is not approved", e.Capability)
}

func (e *CapabilityError) Unwrap() error {
	if e == nil {
		return nil
	}
	return &e.PluginError
}

func newCapabilityError(capability Capability, code, message string) *CapabilityError {
	return &CapabilityError{
		Capability: capability,
		PluginError: PluginError{
			Code:    code,
			Message: message,
		},
	}
}

type MessageType string

const (
	MessageCall   MessageType = "call"
	MessageEvent  MessageType = "event"
	MessageResult MessageType = "result"
)

// Message is the stable envelope used by the initial protocol. Payload is
// deliberately opaque so the transport can later use generated Protobuf
// payloads without changing the lifecycle handshake.
type Message struct {
	Type      MessageType  `json:"type"`
	RequestID string       `json:"request_id,omitempty"`
	Method    string       `json:"method,omitempty"`
	Payload   []byte       `json:"payload,omitempty"`
	Error     *PluginError `json:"error,omitempty"`
}

// PluginError is a structured remote error.
type PluginError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

func (e *PluginError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func newPluginError(code, message string) *PluginError {
	return &PluginError{Code: code, Message: message}
}
