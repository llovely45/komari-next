package plugin

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/pluginprocess"
)

// urlSchemeRE matches a leading URL scheme such as "http:" or "javascript:",
// mirroring the theme redirect validation.
var urlSchemeRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+\-.]*:`)

// readManifest loads and validates the manifest of an installed plugin
// directory.
func readManifest(dir string) (models.Plugin, error) {
	var info models.Plugin
	data, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		return info, fmt.Errorf("read plugin manifest: %w", err)
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, fmt.Errorf("parse plugin manifest: %w", err)
	}
	if err := validateManifest(&info); err != nil {
		return info, err
	}
	return info, nil
}

// validateManifest checks the fields the server relies on and fills in
// defaults.
func validateManifest(info *models.Plugin) error {
	if info.Short == "" {
		return fmt.Errorf("plugin short is required")
	}
	if !validShort(info.Short) {
		return fmt.Errorf("plugin short %q is invalid: only letters, digits, '_' and '-' are allowed", info.Short)
	}
	if !models.IsLocalizedText(info.Name) {
		return fmt.Errorf("plugin name is required")
	}
	if info.Runtime == "" {
		info.Runtime = "javascript"
	}
	switch info.Runtime {
	case "javascript":
		if info.Entry == "" {
			info.Entry = defaultEntry
		}
		if len(info.RPCMethods) != 0 || len(info.GoHostRPCMethods) != 0 || len(info.Permissions.GoCapabilities) != 0 || info.EntrySHA256 != "" {
			return fmt.Errorf("entrySha256, rpcMethods, goHostRPCMethods and goCapabilities are only valid for runtime \"go-wasi\"")
		}
	case "go-wasi":
		if info.Entry == "" {
			info.Entry = "plugin.wasm"
		}
		if err := validateGoWASIManifest(info); err != nil {
			return err
		}
	default:
		return fmt.Errorf("plugin runtime %q is invalid: use \"javascript\" or \"go-wasi\"", info.Runtime)
	}
	if !filepath.IsLocal(info.Entry) {
		return fmt.Errorf("plugin entry %q must be a relative path inside the plugin directory", info.Entry)
	}
	if info.Icon != "" && !filepath.IsLocal(info.Icon) {
		return fmt.Errorf("plugin icon %q must be a relative path inside the plugin directory", info.Icon)
	}
	for i := range info.Pages {
		if err := validatePage(&info.Pages[i]); err != nil {
			return err
		}
	}
	return nil
}

func validateGoWASIManifest(info *models.Plugin) error {
	checksum, err := hex.DecodeString(info.EntrySHA256)
	if err != nil || len(checksum) != 32 {
		return fmt.Errorf("Go/WASI runtime requires a 64-character SHA-256 entrySha256")
	}
	capabilities := make(map[string]struct{}, len(info.Permissions.GoCapabilities))
	for _, capability := range info.Permissions.GoCapabilities {
		if capability != string(pluginprocess.CapabilityRPC) && capability != string(pluginprocess.CapabilityRoutes) &&
			capability != string(pluginprocess.CapabilityNetwork) {
			return fmt.Errorf("Go/WASI capability %q is unsupported; available capabilities are rpc, routes and network", capability)
		}
		if _, exists := capabilities[capability]; exists {
			return fmt.Errorf("Go/WASI capability %q is duplicated", capability)
		}
		capabilities[capability] = struct{}{}
	}
	seenMethods := make(map[string]struct{}, len(info.RPCMethods))
	if len(info.RPCMethods) > 64 {
		return fmt.Errorf("Go/WASI plugin may declare at most 64 RPC methods")
	}
	for _, method := range info.RPCMethods {
		if !strings.HasPrefix(method, "plugin:"+info.Short+":") || len(method) > 160 || strings.TrimSpace(method) != method {
			return fmt.Errorf("Go/WASI RPC method %q must start with plugin:%s:", method, info.Short)
		}
		name := strings.TrimPrefix(method, "plugin:"+info.Short+":")
		if name == "" || strings.ContainsAny(name, " :/\\\t\r\n") {
			return fmt.Errorf("Go/WASI RPC method %q has an invalid method name", method)
		}
		if _, exists := seenMethods[method]; exists {
			return fmt.Errorf("Go/WASI RPC method %q is duplicated", method)
		}
		seenMethods[method] = struct{}{}
	}
	seenHostMethods := make(map[string]struct{}, len(info.GoHostRPCMethods))
	if len(info.GoHostRPCMethods) > 64 {
		return fmt.Errorf("Go/WASI plugin may declare at most 64 host RPC methods")
	}
	for _, method := range info.GoHostRPCMethods {
		if !strings.HasPrefix(method, "admin:") || strings.TrimSpace(method) != method || len(method) > 160 || strings.ContainsAny(method, " /\\\t\r\n") {
			return fmt.Errorf("Go/WASI host RPC method %q must be a valid admin:<method> name", method)
		}
		if _, exists := seenHostMethods[method]; exists {
			return fmt.Errorf("Go/WASI host RPC method %q is duplicated", method)
		}
		seenHostMethods[method] = struct{}{}
	}
	if len(info.RPCMethods) > 0 {
		if _, ok := capabilities[string(pluginprocess.CapabilityRoutes)]; !ok {
			return fmt.Errorf("Go/WASI RPC methods require the routes capability")
		}
	}
	if len(info.GoHostRPCMethods) > 0 {
		if _, ok := capabilities[string(pluginprocess.CapabilityRPC)]; !ok {
			return fmt.Errorf("Go/WASI host RPC methods require the rpc capability")
		}
	}
	return nil
}

// validatePage checks one declared plugin page. Type and visibility default
// to iframe/admin so existing manifests keep their behavior.
func validatePage(page *models.PluginPage) error {
	if page.Type == "" {
		page.Type = models.PageTypeIframe
	}
	if page.Visibility == "" {
		page.Visibility = models.PageVisibilityAdmin
	}
	switch page.Visibility {
	case models.PageVisibilityAdmin, models.PageVisibilityPublic:
	default:
		return fmt.Errorf("plugin page visibility %q is invalid: use \"admin\" or \"public\"", page.Visibility)
	}
	if page.Icon != "" && !filepath.IsLocal(page.Icon) {
		return fmt.Errorf("plugin page icon %q must be a relative path inside the plugin directory", page.Icon)
	}
	switch page.Type {
	case models.PageTypeIframe:
		if page.File == "" || !filepath.IsLocal(page.File) {
			return fmt.Errorf("plugin iframe page requires a relative file path inside the plugin directory")
		}
	case models.PageTypeRedirect:
		if page.URL == "" || !isSafeInternalPath(page.URL) {
			return fmt.Errorf("plugin redirect page requires an internal site path starting with /")
		}
	default:
		return fmt.Errorf("plugin page type %q is invalid: use \"iframe\" or \"redirect\"", page.Type)
	}
	if !models.IsLocalizedText(page.Title) {
		return fmt.Errorf("plugin page %q requires a title", page.Title)
	}
	return nil
}

// isSafeInternalPath mirrors the theme redirect rule: only same-origin
// relative paths are allowed, no scheme, no backslashes, no traversal.
func isSafeInternalPath(target string) bool {
	target = strings.TrimSpace(target)
	if !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") {
		return false
	}
	if strings.Contains(target, "\\") || urlSchemeRE.MatchString(target) {
		return false
	}
	for _, segment := range strings.Split(target, "/") {
		if segment == ".." {
			return false
		}
	}
	return true
}
