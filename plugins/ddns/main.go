package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/komari-monitor/komari/pkg/pluginprocess"
)

const (
	pluginID      = "cloudflare-ddns"
	pluginVersion = "2.3.1"
)

func main() {
	client := pluginprocess.NewClient()
	service := newService(client)
	methods := map[string]pluginprocess.MessageHandler{
		"plugin:cloudflare-ddns:state":       service.rpcState,
		"plugin:cloudflare-ddns:clients":     service.rpcClients,
		"plugin:cloudflare-ddns:save":        service.rpcSave,
		"plugin:cloudflare-ddns:test":        service.rpcTest,
		"plugin:cloudflare-ddns:huaweiLines": service.rpcHuaweiLines,
		"plugin:cloudflare-ddns:sync":        service.rpcSync,
	}
	for name, handler := range methods {
		if err := client.RegisterRPC(name, protectRPC(service, name, handler)); err != nil {
			fmt.Fprintln(os.Stderr, "register DDNS RPC:", err)
			return
		}
	}
	handshake := pluginprocess.Handshake{
		ProtocolVersion:       pluginprocess.ProtocolVersion,
		PluginID:              pluginID,
		PluginVersion:         pluginVersion,
		KomariAPIVersion:      "v1",
		RequestedCapabilities: []pluginprocess.Capability{pluginprocess.CapabilityRPC, pluginprocess.CapabilityRoutes, pluginprocess.CapabilityNetwork},
	}
	if err := client.Start(context.Background(), os.Stdin, os.Stdout, handshake); err != nil {
		fmt.Fprintln(os.Stderr, "start DDNS plugin:", err)
		return
	}
	if err := service.load(context.Background()); err != nil {
		service.mu.Lock()
		service.lastError = "加载 DDNS 配置失败：" + err.Error()
		service.storageError = "插件配置或运行状态无法读取；保存前请检查插件日志和数据目录"
		service.mu.Unlock()
		_ = client.Log(context.Background(), "加载配置失败: "+err.Error())
	}
	go service.scheduleLoop(context.Background())
	if err := client.Wait(); err != nil {
		fmt.Fprintln(os.Stderr, "DDNS plugin stopped:", err)
	}
}

func protectRPC(service *service, method string, handler pluginprocess.MessageHandler) pluginprocess.MessageHandler {
	return func(ctx context.Context, message pluginprocess.Message) (payload []byte, failure *pluginprocess.PluginError) {
		defer func() {
			if recovered := recover(); recovered != nil {
				detail := fmt.Sprintf("%T", recovered)
				fmt.Fprintf(os.Stderr, "DDNS RPC %s panicked: %s\n%s", method, detail, debug.Stack())
				payload = nil
				failure = pluginErr("internal_error", "DDNS 插件内部错误，请查看插件运行日志")
			}
		}()
		return handler(ctx, message)
	}
}

func (s *service) rpcState(ctx context.Context, _ pluginprocess.Message) ([]byte, *pluginprocess.PluginError) {
	return encodeResponse(s.state(ctx))
}

func (s *service) rpcClients(ctx context.Context, _ pluginprocess.Message) ([]byte, *pluginprocess.PluginError) {
	clients, err := s.clients(ctx)
	if err != nil {
		return nil, pluginErr("clients_unavailable", "无法读取 Komari 节点列表："+s.redact(err.Error()))
	}
	return encodeResponse(clients)
}

func (s *service) rpcSave(ctx context.Context, message pluginprocess.Message) ([]byte, *pluginprocess.PluginError) {
	var input saveInput
	if err := json.Unmarshal(message.Payload, &input); err != nil {
		return nil, pluginErr("invalid_params", "配置内容无效")
	}
	state, err := s.save(ctx, input)
	if err != nil {
		return nil, s.toPluginError(err)
	}
	return encodeResponse(state)
}

func (s *service) rpcTest(ctx context.Context, message pluginprocess.Message) ([]byte, *pluginprocess.PluginError) {
	var input testInput
	if err := json.Unmarshal(message.Payload, &input); err != nil {
		return nil, pluginErr("invalid_params", "验证参数无效")
	}
	result, err := s.test(ctx, input)
	if err != nil {
		return nil, s.toPluginError(err)
	}
	return encodeResponse(result)
}

func (s *service) rpcHuaweiLines(ctx context.Context, message pluginprocess.Message) ([]byte, *pluginprocess.PluginError) {
	var input huaweiLinesInput
	if err := json.Unmarshal(message.Payload, &input); err != nil {
		return nil, pluginErr("invalid_params", "华为云线路查询参数无效")
	}
	lines, err := s.huaweiLines(ctx, input)
	if err != nil {
		return nil, s.toPluginError(err)
	}
	return encodeResponse(lines)
}

func (s *service) rpcSync(ctx context.Context, message pluginprocess.Message) ([]byte, *pluginprocess.PluginError) {
	var input struct {
		ID string `json:"id"`
	}
	if len(message.Payload) > 0 && string(message.Payload) != "null" {
		if err := json.Unmarshal(message.Payload, &input); err != nil {
			return nil, pluginErr("invalid_params", "同步参数无效")
		}
	}
	started, err := s.start(input.ID, true)
	if err != nil {
		return nil, s.toPluginError(err)
	}
	return encodeResponse(map[string]bool{"started": started})
}

func encodeResponse(value any) ([]byte, *pluginprocess.PluginError) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, pluginErr("encode_failed", "无法编码插件响应")
	}
	return data, nil
}

func pluginErr(code, message string) *pluginprocess.PluginError {
	return &pluginprocess.PluginError{Code: code, Message: message}
}
