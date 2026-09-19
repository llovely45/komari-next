package pluginprocess

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func validHandshake() Handshake {
	return Handshake{
		ProtocolVersion:  ProtocolVersion,
		PluginID:         "traffic-report",
		PluginVersion:    "1.0.0",
		KomariAPIVersion: "v1",
	}
}

func TestValidateHandshakeApprovesRequestedCapabilitiesInStableOrder(t *testing.T) {
	handshake := validHandshake()
	handshake.RequestedCapabilities = []Capability{
		CapabilityRoutes,
		CapabilityEvents,
	}

	got, err := ValidateHandshake(handshake, []Capability{
		CapabilityRPC,
		CapabilityEvents,
		CapabilityRoutes,
	})
	if err != nil {
		t.Fatalf("ValidateHandshake: %v", err)
	}

	want := []Capability{CapabilityEvents, CapabilityRoutes}
	if !reflect.DeepEqual(got.ApprovedCapabilities, want) {
		t.Fatalf("approved capabilities = %#v, want %#v", got.ApprovedCapabilities, want)
	}
}

func TestValidateHandshakeDoesNotGrantUnrequestedCapabilities(t *testing.T) {
	handshake := validHandshake()
	handshake.ApprovedCapabilities = []Capability{CapabilityExec}

	got, err := ValidateHandshake(handshake, []Capability{
		CapabilityExec,
		CapabilityRoutes,
	})
	if err != nil {
		t.Fatalf("ValidateHandshake: %v", err)
	}
	if len(got.ApprovedCapabilities) != 0 {
		t.Fatalf("approved capabilities = %#v, want no defaults", got.ApprovedCapabilities)
	}
}

func TestZeroCapabilityHandshakeKeepsExplicitJSONFields(t *testing.T) {
	handshake := validHandshake()
	handshake.RequestedCapabilities = []Capability{}

	got, err := ValidateHandshake(handshake, []Capability{})
	if err != nil {
		t.Fatalf("ValidateHandshake: %v", err)
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal zero-capability handshake: %v", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("inspect zero-capability handshake JSON: %v", err)
	}
	for _, field := range []string{"requested_capabilities", "approved_capabilities"} {
		if _, ok := object[field]; !ok {
			t.Fatalf("encoded zero-capability handshake JSON %s does not contain field %q", encoded, field)
		}
	}

	var decoded Handshake
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal zero-capability handshake: %v", err)
	}
	if !reflect.DeepEqual(decoded, got) {
		t.Fatalf("decoded zero-capability handshake = %#v, want %#v", decoded, got)
	}
}

func TestValidateHandshakeRejectsInvalidIdentityAndVersionWithStructuredErrors(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Handshake)
		code string
	}{
		{name: "protocol version below current", edit: func(h *Handshake) { h.ProtocolVersion = ProtocolVersion - 1 }, code: "invalid_protocol_version"},
		{name: "protocol version above current", edit: func(h *Handshake) { h.ProtocolVersion = ProtocolVersion + 1 }, code: "invalid_protocol_version"},
		{name: "plugin id", edit: func(h *Handshake) { h.PluginID = "   " }, code: "invalid_plugin_id"},
		{name: "plugin version", edit: func(h *Handshake) { h.PluginVersion = "" }, code: "invalid_plugin_version"},
		{name: "api version", edit: func(h *Handshake) { h.KomariAPIVersion = "\t" }, code: "invalid_komari_api_version"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handshake := validHandshake()
			test.edit(&handshake)

			_, err := ValidateHandshake(handshake, nil)
			if err == nil {
				t.Fatalf("ValidateHandshake unexpectedly accepted invalid %s", test.name)
			}

			var pluginErr *PluginError
			if !errors.As(err, &pluginErr) {
				t.Fatalf("error = %T, want *PluginError: %v", err, err)
			}
			if pluginErr.Code != test.code {
				t.Fatalf("plugin error code = %q, want %q", pluginErr.Code, test.code)
			}
			if strings.TrimSpace(pluginErr.Message) == "" {
				t.Fatal("plugin error message is empty")
			}
		})
	}
}

func TestValidateHandshakeRejectsInvalidCapabilities(t *testing.T) {
	tests := []struct {
		name       string
		requested  []Capability
		approved   []Capability
		code       string
		capability Capability
	}{
		{
			name:       "unknown requested",
			requested:  []Capability{"unknown"},
			approved:   []Capability{"unknown"},
			code:       "unknown_capability",
			capability: "unknown",
		},
		{
			name:       "empty requested",
			requested:  []Capability{""},
			approved:   []Capability{CapabilityEvents},
			code:       "invalid_capability",
			capability: "",
		},
		{
			name:       "duplicate requested",
			requested:  []Capability{CapabilityEvents, CapabilityEvents},
			approved:   []Capability{CapabilityEvents},
			code:       "duplicate_capability",
			capability: CapabilityEvents,
		},
		{
			name:       "unapproved requested",
			requested:  []Capability{CapabilityExec},
			approved:   []Capability{CapabilityEvents},
			code:       "capability_not_approved",
			capability: CapabilityExec,
		},
		{
			name:       "unknown administrator approval",
			requested:  nil,
			approved:   []Capability{"unknown"},
			code:       "unknown_capability",
			capability: "unknown",
		},
		{
			name:       "empty administrator approval",
			requested:  nil,
			approved:   []Capability{""},
			code:       "invalid_capability",
			capability: "",
		},
		{
			name:       "duplicate administrator approval",
			requested:  nil,
			approved:   []Capability{CapabilityRoutes, CapabilityRoutes},
			code:       "duplicate_capability",
			capability: CapabilityRoutes,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handshake := validHandshake()
			handshake.RequestedCapabilities = test.requested

			_, err := ValidateHandshake(handshake, test.approved)
			if err == nil {
				t.Fatalf("ValidateHandshake unexpectedly accepted %s", test.name)
			}

			var capabilityErr *CapabilityError
			if !errors.As(err, &capabilityErr) {
				t.Fatalf("error = %T, want *CapabilityError: %v", err, err)
			}
			if capabilityErr.Capability != test.capability {
				t.Fatalf("capability error = %#v, want %#v", capabilityErr.Capability, test.capability)
			}

			wrapped := errors.Unwrap(err)
			if wrapped == nil {
				t.Fatalf("error = %T, want a wrapped *PluginError: %v", err, err)
			}
			var pluginErr *PluginError
			if !errors.As(wrapped, &pluginErr) {
				t.Fatalf("wrapped error = %T, want *PluginError: %v", wrapped, wrapped)
			}
			if pluginErr.Code != test.code {
				t.Fatalf("plugin error code = %q, want %q", pluginErr.Code, test.code)
			}
		})
	}
}

func TestCapabilityErrorJSONIncludesStructuredFields(t *testing.T) {
	handshake := validHandshake()
	handshake.RequestedCapabilities = []Capability{CapabilityExec}
	_, err := ValidateHandshake(handshake, []Capability{CapabilityEvents})
	if err == nil {
		t.Fatal("ValidateHandshake unexpectedly accepted an unapproved capability")
	}

	var capabilityErr *CapabilityError
	if !errors.As(err, &capabilityErr) {
		t.Fatalf("error = %T, want *CapabilityError: %v", err, err)
	}

	encoded, marshalErr := json.Marshal(capabilityErr)
	if marshalErr != nil {
		t.Fatalf("marshal capability error: %v", marshalErr)
	}
	var object map[string]json.RawMessage
	if unmarshalErr := json.Unmarshal(encoded, &object); unmarshalErr != nil {
		t.Fatalf("inspect capability error JSON: %v", unmarshalErr)
	}
	for _, field := range []string{"capability", "code", "message"} {
		if _, ok := object[field]; !ok {
			t.Fatalf("encoded capability error JSON %s does not contain field %q", encoded, field)
		}
	}
	var decoded struct {
		Capability Capability `json:"capability"`
		Code       string     `json:"code"`
		Message    string     `json:"message"`
	}
	if unmarshalErr := json.Unmarshal(encoded, &decoded); unmarshalErr != nil {
		t.Fatalf("decode capability error fields: %v", unmarshalErr)
	}
	if decoded.Capability != CapabilityExec {
		t.Fatalf("capability = %q, want %q", decoded.Capability, CapabilityExec)
	}
	if decoded.Code != "capability_not_approved" {
		t.Fatalf("code = %q, want %q", decoded.Code, "capability_not_approved")
	}
	if decoded.Message != "plugin capability \"exec\" is not approved" {
		t.Fatalf("message = %q, want structured approval message", decoded.Message)
	}
}

func TestProtocolJSONUsesStableFieldNamesAndRoundTrips(t *testing.T) {
	handshake := validHandshake()
	handshake.RequestedCapabilities = []Capability{CapabilityEvents, CapabilityRPC}
	handshake.ApprovedCapabilities = []Capability{CapabilityEvents}
	assertJSONRoundTrip(t, handshake, []string{
		"protocol_version",
		"plugin_id",
		"plugin_version",
		"komari_api_version",
		"requested_capabilities",
		"approved_capabilities",
	}, &Handshake{})

	messageTypes := []MessageType{MessageCall, MessageEvent, MessageResult}
	for _, messageType := range messageTypes {
		t.Run(string(messageType), func(t *testing.T) {
			message := Message{
				Type:      messageType,
				RequestID: "req-1",
				Method:    "node.online",
				Payload:   []byte(`{"uuid":"node-a"}`),
				Error: &PluginError{
					Code:      "upstream_unavailable",
					Message:   "upstream unavailable",
					Retryable: true,
				},
			}
			assertJSONRoundTrip(t, message, []string{
				"type",
				"request_id",
				"method",
				"payload",
				"error",
			}, &Message{})
		})
	}
}

func TestPluginErrorRoundTripsAsStructuredError(t *testing.T) {
	original := &PluginError{
		Code:      "upstream_unavailable",
		Message:   "upstream unavailable",
		Retryable: true,
	}
	stringer, ok := any(original).(interface{ Error() string })
	if !ok {
		t.Fatal("PluginError does not implement error")
	}
	if stringer.Error() != original.Message {
		t.Fatalf("error string = %q, want %q", stringer.Error(), original.Message)
	}

	encoded, marshalErr := json.Marshal(original)
	if marshalErr != nil {
		t.Fatalf("marshal plugin error: %v", marshalErr)
	}
	var decoded PluginError
	if unmarshalErr := json.Unmarshal(encoded, &decoded); unmarshalErr != nil {
		t.Fatalf("unmarshal plugin error: %v", unmarshalErr)
	}
	var object map[string]json.RawMessage
	if unmarshalErr := json.Unmarshal(encoded, &object); unmarshalErr != nil {
		t.Fatalf("inspect plugin error JSON: %v", unmarshalErr)
	}
	for _, field := range []string{"code", "message", "retryable"} {
		if _, ok := object[field]; !ok {
			t.Fatalf("encoded plugin error JSON %s does not contain field %q", encoded, field)
		}
	}
	if !reflect.DeepEqual(decoded, *original) {
		t.Fatalf("decoded plugin error = %#v, want %#v", decoded, *original)
	}
}

func assertJSONRoundTrip[T any](t *testing.T, original T, fields []string, decoded *T) {
	t.Helper()

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal %T: %v", original, err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("inspect %T JSON: %v", original, err)
	}
	for _, field := range fields {
		if _, ok := object[field]; !ok {
			t.Fatalf("encoded %T JSON %s does not contain field %q", original, encoded, field)
		}
	}

	if err := json.Unmarshal(encoded, decoded); err != nil {
		t.Fatalf("unmarshal %T: %v", original, err)
	}
	if !reflect.DeepEqual(*decoded, original) {
		t.Fatalf("decoded %T = %#v, want %#v", original, *decoded, original)
	}
}
