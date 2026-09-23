package plugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/pluginprocess"
	"github.com/komari-monitor/komari/pkg/rpc"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

const (
	goWASIStartupTimeout  = 20 * time.Second
	goWASIMemoryPages     = 4096 // 256 MiB
	goWASIHTTPMaxBytes    = 4 << 20
	goWASIStorageMaxBytes = 2 << 20
	goWASIStorageMaxTotal = 128 << 20
)

type goWASIRuntime struct {
	short   string
	storage string
	info    models.Plugin
	wasm    []byte
	logs    io.Writer

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	ready  chan error

	mu        sync.RWMutex
	storageMu sync.Mutex
	session   *pluginprocess.Session
	allowed   map[pluginprocess.Capability]bool

	rpcReady     chan error
	rpcReadyOnce sync.Once
	readyOnce    sync.Once
	running      atomic.Bool
	started      atomic.Bool
	heartbeatAt  atomic.Int64
	hostMethods  map[string]struct{}
	httpClient   *http.Client
}

func (m *Manager) loadGoWASI(short, dir string, info models.Plugin) error {
	wasmPath := filepath.Join(dir, info.Entry)
	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		return fmt.Errorf("read Go/WASI plugin module %s: %w", info.Entry, err)
	}
	if len(wasm) == 0 {
		return fmt.Errorf("Go/WASI plugin module %s is empty", info.Entry)
	}
	if err := verifyEntrySHA256Bytes(wasm, info.EntrySHA256); err != nil {
		return fmt.Errorf("verify Go/WASI plugin entry: %w", err)
	}
	storage := filepath.Join(StorageDir, short)
	if err := os.MkdirAll(storage, 0700); err != nil {
		return fmt.Errorf("create Go/WASI plugin storage: %w", err)
	}
	if err := os.Chmod(storage, 0700); err != nil {
		return fmt.Errorf("restrict Go/WASI plugin storage permissions: %w", err)
	}

	logs := m.logStore(short)
	logs.Reset()
	_, _ = logs.Write([]byte("[plugin] loading Go/WASI " + short + "\n"))
	ctx, cancel := context.WithCancel(context.Background())
	instance := &Instance{
		info: info, dir: dir, handlers: make(map[string]goja.Callable),
		statics: make(map[string]*staticConfig), goRPCs: make(map[string]bool),
	}
	goRuntime := &goWASIRuntime{
		short: short, storage: storage, info: info, wasm: wasm, logs: logs,
		ctx: ctx, cancel: cancel, done: make(chan struct{}), ready: make(chan error, 1),
		rpcReady: make(chan error, 1), allowed: make(map[pluginprocess.Capability]bool),
		httpClient:  newGoPluginHTTPClient(goWASIRequestTimeout(info.Permissions.TimeoutSeconds)),
		hostMethods: make(map[string]struct{}),
	}
	for _, name := range info.Permissions.GoCapabilities {
		goRuntime.allowed[pluginprocess.Capability(name)] = true
	}
	for _, method := range info.GoHostRPCMethods {
		goRuntime.hostMethods[method] = struct{}{}
	}
	instance.goWASI = goRuntime

	m.mu.Lock()
	if _, ok := m.instances[short]; ok {
		m.mu.Unlock()
		cancel()
		return fmt.Errorf("plugin %q is already loaded", short)
	}
	m.instances[short] = instance
	m.mu.Unlock()

	for _, method := range info.RPCMethods {
		if err := rpc.Register(method, m.goRPCHandler(short, method)); err != nil {
			m.dropInstance(short, instance)
			return fmt.Errorf("register Go/WASI RPC %q: %w", method, err)
		}
		instance.goRPCs[method] = true
	}

	go goRuntime.run()
	select {
	case err := <-goRuntime.ready:
		if err != nil {
			m.dropInstance(short, instance)
			return fmt.Errorf("start Go/WASI plugin %q: %w", short, err)
		}
	case <-time.After(goWASIStartupTimeout):
		m.dropInstance(short, instance)
		return fmt.Errorf("Go/WASI plugin %q did not complete its startup handshake within %s", short, goWASIStartupTimeout)
	case <-ctx.Done():
		m.dropInstance(short, instance)
		return fmt.Errorf("Go/WASI plugin %q was stopped during startup", short)
	}
	select {
	case err := <-goRuntime.rpcReady:
		if err != nil {
			m.dropInstance(short, instance)
			return fmt.Errorf("register Go/WASI plugin %q RPC methods: %w", short, err)
		}
	case <-time.After(goWASIStartupTimeout):
		m.dropInstance(short, instance)
		return fmt.Errorf("Go/WASI plugin %q did not register its declared RPC methods", short)
	case <-ctx.Done():
		m.dropInstance(short, instance)
		return fmt.Errorf("Go/WASI plugin %q was stopped during startup", short)
	}
	_, _ = logs.Write([]byte("[plugin] Go/WASI plugin ready " + short + "\n"))
	return nil
}

func (g *goWASIRuntime) run() {
	defer close(g.done)
	backoff := time.Second
	for {
		if g.ctx.Err() != nil {
			return
		}
		err := g.runModule()
		if g.ctx.Err() != nil {
			return
		}
		if !g.started.Load() {
			if err == nil {
				err = errors.New("Go/WASI plugin stopped before startup completed")
			}
			g.readyOnce.Do(func() { g.ready <- err })
			g.rpcReadyOnce.Do(func() { g.rpcReady <- fmt.Errorf("Go/WASI plugin stopped before RPC registration: %w", err) })
			return
		}
		if err == nil {
			err = errors.New("Go/WASI plugin exited unexpectedly")
		}
		_, _ = fmt.Fprintf(g.logs, "[plugin] Go/WASI runtime stopped: %s; restarting in %s\n", safePluginLog(err.Error()), backoff)
		timer := time.NewTimer(backoff)
		select {
		case <-g.ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func (g *goWASIRuntime) runModule() error {
	ctx, cancel := context.WithCancel(g.ctx)
	defer cancel()
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithMemoryLimitPages(goWASIMemoryPages).
		WithCloseOnContextDone(true))
	defer runtime.Close(context.Background())
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		return fmt.Errorf("initialize WASI imports: %w", err)
	}
	compiled, err := runtime.CompileModule(ctx, g.wasm)
	if err != nil {
		return fmt.Errorf("compile WASI module: %w", err)
	}
	defer compiled.Close(context.Background())

	guestInput, hostOutput := io.Pipe()
	hostInput, guestOutput := io.Pipe()
	defer guestInput.Close()
	defer hostInput.Close()
	defer guestOutput.Close()
	defer hostOutput.Close()
	stream := pluginprocess.NewStream(hostInput, hostOutput)
	moduleConfig := wazero.NewModuleConfig().
		WithName(g.short).
		WithArgs(g.short).
		WithStdin(guestInput).
		WithStdout(guestOutput).
		WithStderr(g.logs)

	moduleDone := make(chan error, 1)
	go func() {
		module, runErr := runtime.InstantiateModule(ctx, compiled, moduleConfig)
		if module != nil {
			_ = module.Close(context.Background())
		}
		_ = guestOutput.CloseWithError(runErr)
		_ = guestInput.Close()
		moduleDone <- runErr
	}()

	type handshakeResult struct {
		handshake pluginprocess.Handshake
		err       error
	}
	handshakeCh := make(chan handshakeResult, 1)
	go func() {
		handshake, readErr := stream.ReadHandshake()
		handshakeCh <- handshakeResult{handshake: handshake, err: readErr}
	}()
	startupTimer := time.NewTimer(goWASIStartupTimeout)
	defer startupTimer.Stop()
	var handshake pluginprocess.Handshake
	select {
	case result := <-handshakeCh:
		if result.err != nil {
			return result.err
		}
		handshake = result.handshake
	case runErr := <-moduleDone:
		if runErr == nil {
			return io.EOF
		}
		return fmt.Errorf("WASI module exited before handshake: %w", runErr)
	case <-startupTimer.C:
		return fmt.Errorf("WASI module did not send a handshake within %s", goWASIStartupTimeout)
	case <-ctx.Done():
		return ctx.Err()
	}

	accepted, handshakeErr := g.validateHandshake(handshake)
	if handshakeErr != nil {
		_ = stream.WriteHandshakeResponse(pluginprocess.HandshakeResponse{
			Accepted: false,
			Error:    &pluginprocess.PluginError{Code: "handshake_rejected", Message: handshakeErr.Error()},
		})
		return handshakeErr
	}
	if err := stream.WriteHandshakeResponse(pluginprocess.HandshakeResponse{Accepted: true, Handshake: &accepted}); err != nil {
		return err
	}
	g.mu.Lock()
	clear(g.allowed)
	for _, capability := range accepted.ApprovedCapabilities {
		g.allowed[capability] = true
	}
	g.mu.Unlock()
	pluginSession := pluginprocess.NewSession(ctx, stream, g.handleHostCall, g.handleEvent)
	g.mu.Lock()
	g.session = pluginSession
	g.mu.Unlock()
	g.running.Store(true)
	g.started.Store(true)
	g.heartbeatAt.Store(time.Now().UnixNano())
	g.readyOnce.Do(func() { g.ready <- nil })
	sessionDone := make(chan error, 1)
	go func() { sessionDone <- pluginSession.Serve() }()
	go g.wakeWASIGuest(ctx, pluginSession)
	go g.watchHeartbeat(ctx, cancel, pluginSession)

	select {
	case runErr := <-moduleDone:
		pluginSession.Close()
		g.clearSession(pluginSession)
		if runErr != nil {
			return fmt.Errorf("WASI module exited: %w", runErr)
		}
		return nil
	case sessionErr := <-sessionDone:
		cancel()
		pluginSession.Close()
		g.clearSession(pluginSession)
		if sessionErr != nil {
			return fmt.Errorf("plugin protocol stream stopped: %w", sessionErr)
		}
		return io.EOF
	case <-ctx.Done():
		pluginSession.Close()
		g.clearSession(pluginSession)
		return ctx.Err()
	}
}

func (g *goWASIRuntime) wakeWASIGuest(ctx context.Context, session *pluginprocess.Session) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// A no-op event wakes a Go/WASI guest whose stdin read is blocking,
			// allowing the guest scheduler and heartbeat goroutine to run.
			if err := session.Notify("wakeup", nil); err != nil {
				return
			}
		}
	}
}

func (g *goWASIRuntime) validateHandshake(handshake pluginprocess.Handshake) (pluginprocess.Handshake, error) {
	if handshake.PluginID != g.info.Short {
		return pluginprocess.Handshake{}, fmt.Errorf("plugin id %q does not match manifest %q", handshake.PluginID, g.info.Short)
	}
	if handshake.PluginVersion != g.info.Version {
		return pluginprocess.Handshake{}, fmt.Errorf("plugin version %q does not match manifest %q", handshake.PluginVersion, g.info.Version)
	}
	if handshake.KomariAPIVersion != "v1" {
		return pluginprocess.Handshake{}, fmt.Errorf("unsupported Komari Go plugin API %q", handshake.KomariAPIVersion)
	}
	approved := make([]pluginprocess.Capability, 0, len(g.info.Permissions.GoCapabilities))
	for _, capability := range g.info.Permissions.GoCapabilities {
		approved = append(approved, pluginprocess.Capability(capability))
	}
	accepted, err := pluginprocess.ValidateHandshake(handshake, approved)
	if err != nil {
		return pluginprocess.Handshake{}, err
	}
	expected := append([]pluginprocess.Capability(nil), approved...)
	requested := append([]pluginprocess.Capability(nil), accepted.RequestedCapabilities...)
	sort.Slice(expected, func(i, j int) bool { return expected[i] < expected[j] })
	sort.Slice(requested, func(i, j int) bool { return requested[i] < requested[j] })
	if len(expected) != len(requested) {
		return pluginprocess.Handshake{}, errors.New("plugin requested capabilities do not match its manifest")
	}
	for i := range expected {
		if expected[i] != requested[i] {
			return pluginprocess.Handshake{}, errors.New("plugin requested capabilities do not match its manifest")
		}
	}
	for _, capability := range accepted.RequestedCapabilities {
		if capability != pluginprocess.CapabilityRPC && capability != pluginprocess.CapabilityRoutes && capability != pluginprocess.CapabilityNetwork {
			return pluginprocess.Handshake{}, fmt.Errorf("Go/WASI capability %q is not implemented by this host", capability)
		}
	}
	return accepted, nil
}

func (g *goWASIRuntime) handleHostCall(ctx context.Context, message pluginprocess.Message) ([]byte, *pluginprocess.PluginError) {
	// Synchronous host callbacks count as progress while the guest is waiting
	// for their result and may not be able to emit its own heartbeat.
	started := time.Now()
	callName := message.Method
	g.heartbeatAt.Store(started.UnixNano())
	defer func() {
		elapsed := time.Since(started)
		g.heartbeatAt.Store(time.Now().UnixNano())
		if elapsed >= 5*time.Second {
			_, _ = fmt.Fprintf(g.logs, "[plugin] Go/WASI host call %s completed in %s\n", safePluginLog(callName), elapsed.Round(time.Millisecond))
		}
	}()

	switch message.Method {
	case "host.register_rpc":
		if !g.allowed[pluginprocess.CapabilityRoutes] {
			return nil, pluginError("capability_denied", "plugin RPC registration requires the routes capability")
		}
		var request struct {
			Methods []string `json:"methods"`
		}
		if err := json.Unmarshal(message.Payload, &request); err != nil {
			return nil, pluginError("invalid_payload", "invalid RPC registration payload")
		}
		declared := append([]string(nil), g.info.RPCMethods...)
		provided := append([]string(nil), request.Methods...)
		sort.Strings(declared)
		sort.Strings(provided)
		if len(declared) != len(provided) {
			err := errors.New("registered RPC methods do not match the plugin manifest")
			g.rpcReadyOnce.Do(func() { g.rpcReady <- err })
			return nil, pluginError("rpc_manifest_mismatch", err.Error())
		}
		for i := range declared {
			if declared[i] != provided[i] {
				err := errors.New("registered RPC methods do not match the plugin manifest")
				g.rpcReadyOnce.Do(func() { g.rpcReady <- err })
				return nil, pluginError("rpc_manifest_mismatch", err.Error())
			}
		}
		g.rpcReadyOnce.Do(func() { g.rpcReady <- nil })
		return marshalGoPluginPayload(map[string]bool{"registered": true})
	case "host.rpc":
		if !g.allowed[pluginprocess.CapabilityRPC] {
			return nil, pluginError("capability_denied", "calling Komari RPC requires the rpc capability")
		}
		var request struct {
			Method string `json:"method"`
			Params any    `json:"params"`
		}
		if err := json.Unmarshal(message.Payload, &request); err != nil || !strings.HasPrefix(request.Method, "admin:") {
			return nil, pluginError("invalid_rpc", "host RPC calls must name an admin method")
		}
		if _, ok := g.hostMethods[request.Method]; !ok {
			return nil, pluginError("rpc_not_approved", "host RPC method is not declared in the plugin manifest")
		}
		if rpc.IsSensitive(request.Method) {
			return nil, pluginError("sensitive_rpc_denied", "Go/WASI plugins cannot call methods that require interactive 2FA")
		}
		callName += ":" + request.Method
		meta := &rpc.ContextMeta{Permission: rpc.RoleAdmin, Principal: rpc.PrincipalFromRole(rpc.RoleAdmin)}
		callCtx, cancel := context.WithTimeout(ctx, goWASIRequestTimeout(g.info.Permissions.TimeoutSeconds))
		defer cancel()
		response := rpc.CallWithContext(rpc.NewContextWithMeta(callCtx, meta), nil, request.Method, request.Params)
		if response.Error != nil {
			return nil, &pluginprocess.PluginError{Code: fmt.Sprintf("rpc_%d", response.Error.Code), Message: safePluginLog(response.Error.Message)}
		}
		data, err := json.Marshal(response.Result)
		if err != nil {
			return nil, pluginError("rpc_result_encode", "failed to encode Komari RPC result")
		}
		if request.Method == "admin:listClients" || request.Method == "admin:listDDNSClients" {
			data, err = sanitizeGoPluginClientList(data)
			if err != nil {
				return nil, pluginError("rpc_result_encode", "failed to sanitize Komari client list")
			}
		}
		return data, nil
	case "host.http":
		if !g.allowed[pluginprocess.CapabilityNetwork] {
			return nil, pluginError("capability_denied", "HTTP requests require the network capability")
		}
		return g.hostHTTP(ctx, message.Payload)
	case "host.storage.read":
		return g.storageRead(message.Payload)
	case "host.storage.write":
		return g.storageWrite(message.Payload)
	case "host.storage.delete":
		return g.storageDelete(message.Payload)
	case "host.storage.list":
		return g.storageList(message.Payload)
	case "host.log":
		var request struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(message.Payload, &request); err != nil {
			return nil, pluginError("invalid_payload", "invalid log payload")
		}
		request.Message = strings.TrimSpace(request.Message)
		if len(request.Message) > 4096 {
			request.Message = request.Message[:4096]
		}
		_, _ = fmt.Fprintf(g.logs, "[plugin] %s\n", safePluginLog(request.Message))
		return marshalGoPluginPayload(map[string]bool{"logged": true})
	default:
		return nil, pluginError("method_not_found", "host method not found")
	}
}

func (g *goWASIRuntime) storageRead(payload []byte) ([]byte, *pluginprocess.PluginError) {
	var request struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, pluginError("invalid_storage_request", "invalid storage read request")
	}
	full, err := resolveGoPluginStoragePath(g.storage, request.Path, false, false)
	if err != nil {
		return nil, pluginError("storage_path_denied", err.Error())
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, pluginError("storage_read_failed", safePluginLog(err.Error()))
	}
	if len(data) > goWASIStorageMaxBytes {
		return nil, pluginError("storage_file_too_large", "plugin storage file exceeds the 2 MiB limit")
	}
	return marshalGoPluginPayload(map[string][]byte{"data": data})
}

func (g *goWASIRuntime) storageWrite(payload []byte) ([]byte, *pluginprocess.PluginError) {
	g.storageMu.Lock()
	defer g.storageMu.Unlock()
	var request struct {
		Path string `json:"path"`
		Data []byte `json:"data"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, pluginError("invalid_storage_request", "invalid storage write request")
	}
	if len(request.Data) > goWASIStorageMaxBytes {
		return nil, pluginError("storage_file_too_large", "plugin storage file exceeds the 2 MiB limit")
	}
	full, err := resolveGoPluginStoragePath(g.storage, request.Path, true, false)
	if err != nil {
		return nil, pluginError("storage_path_denied", err.Error())
	}
	currentSize, err := goPluginStorageSize(g.storage)
	if err != nil {
		return nil, pluginError("storage_write_failed", safePluginLog(err.Error()))
	}
	if existing, statErr := os.Stat(full); statErr == nil {
		currentSize -= existing.Size()
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, pluginError("storage_write_failed", safePluginLog(statErr.Error()))
	}
	if currentSize+int64(len(request.Data)) > goWASIStorageMaxTotal {
		return nil, pluginError("storage_quota_exceeded", "plugin storage is limited to 128 MiB")
	}
	file, err := os.CreateTemp(filepath.Dir(full), ".komari-plugin-*")
	if err != nil {
		return nil, pluginError("storage_write_failed", safePluginLog(err.Error()))
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return nil, pluginError("storage_write_failed", safePluginLog(err.Error()))
	}
	if _, err := file.Write(request.Data); err != nil {
		_ = file.Close()
		return nil, pluginError("storage_write_failed", safePluginLog(err.Error()))
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, pluginError("storage_write_failed", safePluginLog(err.Error()))
	}
	if err := file.Close(); err != nil {
		return nil, pluginError("storage_write_failed", safePluginLog(err.Error()))
	}
	if err := os.Rename(tmp, full); err != nil {
		return nil, pluginError("storage_write_failed", safePluginLog(err.Error()))
	}
	return marshalGoPluginPayload(map[string]bool{"written": true})
}

func (g *goWASIRuntime) storageDelete(payload []byte) ([]byte, *pluginprocess.PluginError) {
	g.storageMu.Lock()
	defer g.storageMu.Unlock()
	var request struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, pluginError("invalid_storage_request", "invalid storage delete request")
	}
	full, err := resolveGoPluginStoragePath(g.storage, request.Path, false, false)
	if err != nil {
		return nil, pluginError("storage_path_denied", err.Error())
	}
	if err := os.Remove(full); err != nil {
		return nil, pluginError("storage_delete_failed", safePluginLog(err.Error()))
	}
	return marshalGoPluginPayload(map[string]bool{"deleted": true})
}

func (g *goWASIRuntime) storageList(payload []byte) ([]byte, *pluginprocess.PluginError) {
	var request struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, pluginError("invalid_storage_request", "invalid storage list request")
	}
	full := g.storage
	var err error
	if request.Path != "" {
		full, err = resolveGoPluginStoragePath(g.storage, request.Path, false, true)
		if err != nil {
			return nil, pluginError("storage_path_denied", err.Error())
		}
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return nil, pluginError("storage_list_failed", safePluginLog(err.Error()))
	}
	result := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		info, statErr := entry.Info()
		if statErr != nil || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		result = append(result, map[string]any{"name": entry.Name(), "directory": entry.IsDir(), "size": info.Size()})
	}
	return marshalGoPluginPayload(map[string]any{"entries": result})
}

func marshalGoPluginPayload(value any) ([]byte, *pluginprocess.PluginError) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, pluginError("response_encode_failed", "failed to encode host response")
	}
	return data, nil
}

func resolveGoPluginStoragePath(root, name string, createParents, allowDirectory bool) (string, error) {
	if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") ||
		filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", errors.New("storage path must be a relative path without dot segments")
	}
	parts := strings.Split(name, "/")
	current := root
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("storage path must be a relative path without dot segments")
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && createParents && index < len(parts)-1 {
			if err := os.Mkdir(current, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return "", err
			}
			info, err = os.Lstat(current)
		}
		if errors.Is(err, os.ErrNotExist) && index == len(parts)-1 {
			return current, nil
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("symbolic links are not allowed in plugin storage")
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", errors.New("plugin storage path parent is not a directory")
		}
		if index == len(parts)-1 && info.IsDir() && !allowDirectory {
			return "", errors.New("plugin storage path names a directory")
		}
	}
	return current, nil
}

func goPluginStorageSize(root string) (int64, error) {
	var total int64
	count := 0
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		count++
		if count > 10000 {
			return errors.New("plugin storage contains too many files")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symbolic links are not allowed in plugin storage")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if total > goWASIStorageMaxTotal {
			return errors.New("plugin storage exceeds the 128 MiB quota")
		}
		return nil
	})
	return total, err
}

func (g *goWASIRuntime) handleEvent(_ context.Context, message pluginprocess.Message) {
	if message.Method == "heartbeat" {
		g.heartbeatAt.Store(time.Now().UnixNano())
		return
	}
	if message.Method == "log" {
		_, _ = fmt.Fprintf(g.logs, "[plugin] %s\n", safePluginLog(string(message.Payload)))
	}
}

func (g *goWASIRuntime) watchHeartbeat(ctx context.Context, cancel context.CancelFunc, session *pluginprocess.Session) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			last := time.Unix(0, g.heartbeatAt.Load())
			if time.Since(last) > 60*time.Second {
				// WASI stdin is backed by a blocking reader. While the guest is
				// idle waiting for the next host call, fd_read blocks the guest
				// runtime too, so its heartbeat goroutine cannot run. Only treat
				// a missed heartbeat as a hang when the host is waiting for a
				// response from the guest.
				if !session.HasPendingCalls() {
					g.heartbeatAt.Store(time.Now().UnixNano())
					continue
				}
				_, _ = fmt.Fprintf(g.logs, "[plugin] Go/WASI plugin %s missed heartbeats; restarting\n", g.short)
				session.Close()
				cancel()
				return
			}
		}
	}
}

func (g *goWASIRuntime) hostHTTP(ctx context.Context, payload []byte) ([]byte, *pluginprocess.PluginError) {
	var request struct {
		URL     string      `json:"url"`
		Method  string      `json:"method"`
		Headers http.Header `json:"headers"`
		Body    []byte      `json:"body"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, pluginError("invalid_http_request", "invalid HTTP request payload")
	}
	if err := validateGoPluginURL(request.URL); err != nil {
		return nil, pluginError("http_url_denied", err.Error())
	}
	if request.Method == "" {
		request.Method = http.MethodGet
	}
	switch request.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil, pluginError("http_method_denied", "HTTP method is not allowed")
	}
	if len(request.Body) > goWASIHTTPMaxBytes {
		return nil, pluginError("http_body_too_large", "HTTP request body exceeds the plugin limit")
	}
	callCtx, cancel := context.WithTimeout(ctx, goWASIRequestTimeout(g.info.Permissions.TimeoutSeconds))
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, request.Method, request.URL, bytes.NewReader(request.Body))
	if err != nil {
		return nil, pluginError("invalid_http_request", "invalid HTTP request")
	}
	for name, values := range request.Headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	response, err := g.httpClient.Do(req)
	if err != nil {
		return nil, pluginError("http_request_failed", safePluginLog(err.Error()))
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, goWASIHTTPMaxBytes+1))
	if err != nil {
		return nil, pluginError("http_read_failed", safePluginLog(err.Error()))
	}
	if len(body) > goWASIHTTPMaxBytes {
		return nil, pluginError("http_response_too_large", "HTTP response exceeds the plugin limit")
	}
	data, err := json.Marshal(map[string]any{"status": response.StatusCode, "headers": response.Header, "body": body})
	if err != nil {
		return nil, pluginError("http_response_encode", "failed to encode HTTP response")
	}
	return data, nil
}

func (m *Manager) goRPCHandler(short, method string) rpc.Handler {
	return func(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
		inst := m.instanceFor(short)
		if inst == nil {
			return nil, &rpc.JsonRpcError{Code: rpc.Unavailable, Message: "Go/WASI plugin is not loaded"}
		}
		inst.mu.RLock()
		goRuntime := inst.goWASI
		inst.mu.RUnlock()
		if goRuntime == nil {
			return nil, &rpc.JsonRpcError{Code: rpc.Unavailable, Message: "Go/WASI plugin is unavailable"}
		}
		payload, err := json.Marshal(req.Params)
		if err != nil {
			return nil, &rpc.JsonRpcError{Code: rpc.InvalidParams, Message: "plugin RPC params are invalid"}
		}
		timeout := time.Duration(goRuntime.info.Permissions.TimeoutSeconds) * time.Second
		if timeout <= 0 || timeout > 60*time.Second {
			timeout = 30 * time.Second
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		result, callErr := goRuntime.call(callCtx, method, payload)
		if callErr != nil {
			return nil, &rpc.JsonRpcError{Code: rpc.Unavailable, Message: safePluginLog(callErr.Error())}
		}
		var value any
		if err := json.Unmarshal(result, &value); err != nil {
			return nil, &rpc.JsonRpcError{Code: rpc.InternalError, Message: "Go/WASI plugin returned invalid JSON"}
		}
		return value, nil
	}
}

func (g *goWASIRuntime) call(ctx context.Context, method string, payload []byte) ([]byte, error) {
	g.mu.RLock()
	session := g.session
	g.mu.RUnlock()
	if session == nil || !g.running.Load() {
		return nil, errors.New("Go/WASI plugin is restarting")
	}
	return session.Call(ctx, method, payload)
}

func (g *goWASIRuntime) clearSession(session *pluginprocess.Session) {
	g.mu.Lock()
	if g.session == session {
		g.session = nil
	}
	g.mu.Unlock()
	g.running.Store(false)
}

func (g *goWASIRuntime) Running() bool { return g.running.Load() }

func (g *goWASIRuntime) Close() {
	g.cancel()
	g.mu.RLock()
	session := g.session
	g.mu.RUnlock()
	if session != nil {
		session.Close()
	}
	select {
	case <-g.done:
	case <-time.After(2 * time.Second):
	}
}

func pluginError(code, message string) *pluginprocess.PluginError {
	return &pluginprocess.PluginError{Code: code, Message: safePluginLog(message)}
}

func safePluginLog(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	if len(value) > 4096 {
		value = value[:4096]
	}
	return value
}

func validateGoPluginURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("Go/WASI plugins may only make HTTPS requests to public hosts")
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return errors.New("Go/WASI plugins may only connect to HTTPS port 443")
	}
	return nil
}

func goWASIRequestTimeout(timeoutSeconds int) time.Duration {
	timeout := time.Duration(timeoutSeconds) * time.Second
	if timeout <= 0 || timeout > 60*time.Second {
		timeout = 30 * time.Second
	}
	if timeout > 45*time.Second {
		timeout = 45 * time.Second
	}
	return timeout
}

func newGoPluginHTTPClient(timeout time.Duration) *http.Client {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	transport := base.Clone()
	transport.Proxy = nil
	transport.DialContext = dialGoPluginAddress
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func sanitizeGoPluginClientList(data []byte) ([]byte, error) {
	var clients []map[string]json.RawMessage
	if err := json.Unmarshal(data, &clients); err != nil {
		return nil, err
	}
	safe := make([]map[string]json.RawMessage, 0, len(clients))
	for _, client := range clients {
		item := make(map[string]json.RawMessage, 5)
		for _, name := range []string{"uuid", "name", "ipv4", "ipv6", "group"} {
			if value, ok := client[name]; ok {
				item[name] = value
			}
		}
		safe = append(safe, item)
	}
	return json.Marshal(safe)
}

func dialGoPluginAddress(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid HTTP destination")
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("HTTP destination could not be resolved")
	}
	for _, ip := range ips {
		if !publicPluginIP(ip) {
			return nil, errors.New("requests to private or reserved network addresses are denied")
		}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, ip := range ips {
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	return nil, lastErr
}

var blockedGoPluginPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fec0::/10"),
}

func publicPluginIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, prefix := range blockedGoPluginPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func verifyEntrySHA256(path, expected string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return verifyEntrySHA256Bytes(data, expected)
}

func verifyEntrySHA256Bytes(data []byte, expected string) error {
	if expected == "" {
		return errors.New("Go/WASI plugin manifest must declare entrySHA256")
	}
	want, err := hex.DecodeString(expected)
	if err != nil || len(want) != sha256.Size {
		return errors.New("Go/WASI plugin entrySHA256 is invalid")
	}
	digest := sha256.Sum256(data)
	if subtle.ConstantTimeCompare(want, digest[:]) != 1 {
		return errors.New("Go/WASI plugin entry checksum does not match manifest")
	}
	return nil
}
