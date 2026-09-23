// Package pluginprocess keeps the original internal import path as a
// compatibility facade. New Go plugins should use the public
// github.com/komari-monitor/komari/pkg/pluginprocess package for the same
// versioned wire contract and SDK.
package pluginprocess

import public "github.com/komari-monitor/komari/pkg/pluginprocess"

const ProtocolVersion = public.ProtocolVersion

type (
	Capability        = public.Capability
	Handshake         = public.Handshake
	HandshakeResponse = public.HandshakeResponse
	MessageType       = public.MessageType
	Message           = public.Message
	PluginError       = public.PluginError
	CapabilityError   = public.CapabilityError
	Stream            = public.Stream
	Session           = public.Session
)

const (
	CapabilityEvents  = public.CapabilityEvents
	CapabilityRPC     = public.CapabilityRPC
	CapabilityRoutes  = public.CapabilityRoutes
	CapabilityMetrics = public.CapabilityMetrics
	CapabilityExec    = public.CapabilityExec
	CapabilityNetwork = public.CapabilityNetwork

	MessageCall   = public.MessageCall
	MessageEvent  = public.MessageEvent
	MessageResult = public.MessageResult
)

var ValidateHandshake = public.ValidateHandshake
var NewStream = public.NewStream
var NewSession = public.NewSession
