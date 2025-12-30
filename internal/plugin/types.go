package plugin

import (
	"context"
)

// PluginMetadata defines the structure of a plugin.json file.
type PluginMetadata struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Author      string `json:"author"`
	Entrypoint  string `json:"entrypoint"` // e.g., "index.js"
}

// Manager handles the lifecycle of plugins.
type Manager interface {
	LoadAll(ctx context.Context) error
	Load(ctx context.Context, pluginID string) error
	Unload(ctx context.Context, pluginID string) error
	GetPlugin(pluginID string) (*PluginMetadata, error)
}

// Hook defines an extension point.
type Hook string

const (
	// Lifecycle Hooks
	HookOnBoot     Hook = "OnBoot"     // Called when plugin is loaded
	HookOnShutdown Hook = "OnShutdown" // Called when plugin is unloaded

	// Request Hooks
	HookOnRequest  Hook = "OnRequest"  // Called on each HTTP request (before processing)
	HookOnResponse Hook = "OnResponse" // Called on each HTTP response (after processing)

	// Content Hooks
	HookOnPostSave   Hook = "OnPostSave"   // Called when a post is saved (created or updated)
	HookOnPostDelete Hook = "OnPostDelete"  // Called when a post is deleted
	HookOnPostView   Hook = "OnPostView"    // Called when a post is viewed

	// Product Hooks
	HookOnProductSave   Hook = "OnProductSave"   // Called when a product is saved
	HookOnProductDelete Hook = "OnProductDelete" // Called when a product is deleted
	HookOnProductView   Hook = "OnProductView"   // Called when a product is viewed

	// Order Hooks
	HookOnOrderCreate Hook = "OnOrderCreate" // Called when an order is created
	HookOnOrderUpdate Hook = "OnOrderUpdate" // Called when an order status is updated
	HookOnOrderPaid   Hook = "OnOrderPaid"   // Called when an order is paid

	// User Hooks
	HookOnUserLogin  Hook = "OnUserLogin"  // Called when a user logs in
	HookOnUserLogout Hook = "OnUserLogout" // Called when a user logs out
	HookOnUserCreate Hook = "OnUserCreate"  // Called when a user is created

	// Payment Hooks
	HookOnPaymentSuccess Hook = "OnPaymentSuccess" // Called when payment succeeds
	HookOnPaymentFailed  Hook = "OnPaymentFailed"  // Called when payment fails

	// Theme Hooks
	HookOnThemeActivate Hook = "OnThemeActivate" // Called when a theme is activated
)
