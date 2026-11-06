package plugin

import (
	"context"
	"encoding/json"
	"strings"
)

// PluginMetadata defines the structure of a plugin.json file. The manifest v2
// fields are optional; a v1 manifest leaves them empty. See manifest.go.
type PluginMetadata struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Version     string           `json:"version"`
	Description string           `json:"description"`
	Author      string           `json:"author"`
	Entrypoint  string           `json:"entrypoint"` // e.g., "index.js"
	Settings    []SettingSpec    `json:"settings,omitempty"`
	Permissions []PermissionSpec `json:"permissions,omitempty"`
	Actions     []ActionSpec     `json:"actions,omitempty"`
	Extensions  []ExtensionSpec  `json:"extensions,omitempty"`
	// WebMCPTools holds the raw webmcp_tools list: extra read-only WebMCP tools
	// this plugin offers while it is loaded (ticket 13). It stays raw so a
	// malformed entry skips that tool instead of failing the plugin load; each
	// entry decodes into core.WebMCPToolDecl.
	WebMCPTools json.RawMessage `json:"webmcp_tools,omitempty"`
}

// Manager handles the lifecycle of plugins.
type Manager interface {
	LoadAll(ctx context.Context) error
	Load(ctx context.Context, pluginID string) error
	Unload(ctx context.Context, pluginID string) error
	GetPlugin(pluginID string) (*PluginMetadata, error)
}

// Hook names an event the site dispatches to plugins through
// JellyCMS.registerHook. The catalogue below is the complete list the site
// dispatches; docs/tickets/27-plugin-events.md and docs/plugin-events.md
// describe when each fires and what the payload carries. KnownHooks returns
// the list for validation and documentation.
type Hook string

const (
	// Lifecycle hooks.
	HookOnBoot     Hook = "OnBoot"     // Plugin loaded and its entry script ran
	HookOnShutdown Hook = "OnShutdown" // Plugin about to be unloaded

	// Content hooks (posts and pages).
	HookOnPostSave    Hook = "OnPostSave"    // Created, updated, restored from trash or from a revision
	HookOnPostPublish Hook = "OnPostPublish" // First became publicly readable (save or schedule)
	HookOnPostDelete  Hook = "OnPostDelete"  // Moved to trash or purged
	HookOnPostView    Hook = "OnPostView"    // Public GET of a post or page

	// Product hooks.
	HookOnProductSave   Hook = "OnProductSave"   // Created, updated, duplicated or restored
	HookOnProductDelete Hook = "OnProductDelete" // Moved to trash or purged
	HookOnProductView   Hook = "OnProductView"   // Public GET of a product page

	// Order and payment hooks.
	HookOnOrderCreate    Hook = "OnOrderCreate"    // Checkout committed an order
	HookOnOrderUpdate    Hook = "OnOrderUpdate"    // Any order status transition
	HookOnOrderPaid      Hook = "OnOrderPaid"      // Order transitioned to paid
	HookOnOrderRefund    Hook = "OnOrderRefund"    // Admin refund recorded
	HookOnPaymentSuccess Hook = "OnPaymentSuccess" // Verified gateway callback paid the order
	HookOnPaymentFailed  Hook = "OnPaymentFailed"  // Verified gateway callback reported a failure

	// User hooks.
	HookOnUserCreate      Hook = "OnUserCreate"      // User row created (invite, OAuth, checkout)
	HookOnUserUpdate      Hook = "OnUserUpdate"      // Role, status or display name changed; invite accepted
	HookOnUserDelete      Hook = "OnUserDelete"      // User deleted by an admin
	HookOnUserLogin       Hook = "OnUserLogin"       // Login succeeded
	HookOnUserLoginFailed Hook = "OnUserLoginFailed" // Password login failed
	HookOnUserLogout      Hook = "OnUserLogout"      // Logout revoked a login

	// Comment hooks.
	HookOnCommentCreate Hook = "OnCommentCreate" // Public comment or admin reply stored
	HookOnCommentUpdate Hook = "OnCommentUpdate" // Status changed (approve, unapprove, spam, trash, restore)
	HookOnCommentDelete Hook = "OnCommentDelete" // Comment purged

	// Media hooks.
	HookOnMediaUpload Hook = "OnMediaUpload" // Attachment stored (upload, fetch, legacy upload)
	HookOnMediaDelete Hook = "OnMediaDelete" // Attachment moved to trash

	// Theme and settings hooks.
	HookOnThemeActivate  Hook = "OnThemeActivate"  // Theme switched
	HookOnSettingsChange Hook = "OnSettingsChange" // settings.Set wrote keys
)

// knownHooks is the dispatch catalogue in documentation order.
var knownHooks = []Hook{
	HookOnBoot, HookOnShutdown,
	HookOnPostSave, HookOnPostPublish, HookOnPostDelete, HookOnPostView,
	HookOnProductSave, HookOnProductDelete, HookOnProductView,
	HookOnOrderCreate, HookOnOrderUpdate, HookOnOrderPaid, HookOnOrderRefund, HookOnPaymentSuccess, HookOnPaymentFailed,
	HookOnUserCreate, HookOnUserUpdate, HookOnUserDelete, HookOnUserLogin, HookOnUserLoginFailed, HookOnUserLogout,
	HookOnCommentCreate, HookOnCommentUpdate, HookOnCommentDelete,
	HookOnMediaUpload, HookOnMediaDelete,
	HookOnThemeActivate, HookOnSettingsChange,
}

// KnownHooks returns a copy of the hook catalogue the site dispatches.
func KnownHooks() []Hook {
	return append([]Hook(nil), knownHooks...)
}

// IsKnownHook reports whether name is in the catalogue or is an action hook
// ("action:<id>") for an action meta declares.
func IsKnownHook(meta PluginMetadata, name string) bool {
	for _, hook := range knownHooks {
		if string(hook) == name {
			return true
		}
	}

	actionID, ok := strings.CutPrefix(name, "action:")
	if !ok || actionID == "" {
		return false
	}
	for _, action := range meta.Actions {
		if action.ID == actionID {
			return true
		}
	}

	return false
}
