package settings

// Every option key that the code reads or writes is registered here so the
// admin API can refuse unknown keys (400). Keys listed as Tab "internal" are
// read by the server but are not edited from a settings tab.
func init() {
	Register(seedSpecs()...)
}

func seedSpecs() []Spec {
	s := func(key, kind, def, tab string) Spec {
		return Spec{Key: key, Kind: kind, Default: def, Tab: tab}
	}
	return []Spec{
		// Existing keys read by the current code.
		s("site_title", KindString, "Jelly CMS", "site"),
		s("site_description", KindString, "", "site"),
		s("site_language", KindString, "zh-TW", "site"),
		s("active_theme", KindString, "default", "appearance"),
		s("trash_retention_days", KindInt, "30", "content"),
		s("installation_id", KindString, "", "internal"),
		s("oauth_google_enabled", KindBool, "false", "auth"),
		s("oauth_google_client_id", KindString, "", "auth"),
		s("oauth_google_client_secret", KindSecret, "", "auth"),
		s("oauth_github_enabled", KindBool, "false", "auth"),
		s("oauth_github_client_id", KindString, "", "auth"),
		s("oauth_github_client_secret", KindSecret, "", "auth"),
		s("payment_ecpay_enabled", KindBool, "false", "payment"),
		s("payment_ecpay_merchant_id", KindString, "", "payment"),
		s("payment_ecpay_hash_key", KindSecret, "", "payment"),
		s("payment_ecpay_hash_iv", KindSecret, "", "payment"),
		s("payment_ecpay_test_mode", KindBool, "true", "payment"),
		s("payment_newebpay_enabled", KindBool, "false", "payment"),
		s("payment_newebpay_merchant_id", KindString, "", "payment"),
		s("payment_newebpay_hash_key", KindSecret, "", "payment"),
		s("payment_newebpay_hash_iv", KindSecret, "", "payment"),
		s("payment_newebpay_test_mode", KindBool, "true", "payment"),

		// Keys listed in docs/tickets/08-system.md (選項鍵).
		s("site_timezone", KindString, "UTC", "site"),
		s("permalink_post", KindString, "/post/{slug}", "routes"),
		s("permalink_legacy", KindJSON, "[]", "routes"),
		s("permalink_category_prefix", KindString, "/c", "routes"),
		s("permalink_tag_prefix", KindString, "/t", "routes"),
		s("permalink_author_prefix", KindString, "/author", "routes"),
		s("permalink_archive_prefix", KindString, "/archive", "routes"),
		s("slug_strategy", KindString, "title", "routes"),
		s("home_show", KindString, "latest", "routes"),
		s("home_page_id", KindInt, "0", "routes"),
		s("per_page_home", KindInt, "10", "routes"),
		s("per_page_taxonomy", KindInt, "12", "routes"),
		s("per_page_author", KindInt, "12", "routes"),
		s("per_page_search", KindInt, "10", "routes"),
		s("excerpt_auto_length", KindInt, "110", "routes"),
		s("feed_mode", KindString, "summary", "routes"),
		s("feed_items", KindInt, "20", "routes"),

		s("seo_index_site", KindBool, "true", "crawlers"),
		s("seo_index_types", KindJSON, "{}", "crawlers"),
		s("seo_images_index", KindBool, "true", "crawlers"),
		s("seo_snippet_mode", KindString, "default", "crawlers"),
		s("seo_indexnow_enabled", KindBool, "false", "crawlers"),
		s("seo_indexnow_key", KindSecret, "", "crawlers"),
		s("seo_title_format", KindString, "{title} - {site}", "crawlers"),
		s("seo_default_og_image", KindString, "", "crawlers"),
		s("ai_policy_preset", KindString, "block_training_allow_search", "crawlers"),
		s("ai_bots", KindJSON, "{}", "crawlers"),
		s("ai_custom_rules", KindJSON, "[]", "crawlers"),
		s("ai_block_server_side", KindBool, "false", "crawlers"),
		s("tdm_reservation", KindBool, "false", "crawlers"),
		s("tdm_policy_url", KindString, "", "crawlers"),
		s("llms_txt_enabled", KindBool, "false", "crawlers"),
		s("llms_txt_intro", KindString, "", "crawlers"),

		s("inject_head_all", KindString, "", "injection"),
		s("inject_head_content", KindString, "", "injection"),
		s("inject_footer", KindString, "", "injection"),
		s("inject_on_commerce", KindBool, "false", "injection"),

		// Maintenance keys from ticket 05 plus retry_after from ticket 08.
		s("maintenance_enabled", KindBool, "false", "infra"),
		s("maintenance_message", KindString, "", "infra"),
		s("maintenance_started_at", KindString, "", "internal"),
		s("maintenance_retry_after", KindInt, "600", "infra"),

		s("cache_driver", KindString, "memory", "infra"),
		s("cache_url_enc", KindSecret, "", "infra"),
		s("cache_ttl_page", KindInt, "300", "infra"),
		s("cache_object_enabled", KindBool, "true", "infra"),
		s("queue_driver", KindString, "postgres", "infra"),
		s("queue_url_enc", KindSecret, "", "infra"),
		s("queue_workers", KindInt, "2", "infra"),
		s("queue_paused", KindBool, "false", "infra"),

		s("logs_retention_days", KindInt, "30", "logs"),
		s("logs_error_retention_days", KindInt, "90", "logs"),
		s("logs_http_level", KindString, "error", "logs"),

		s("update_auto_check", KindBool, "true", "update"),
		s("update_channel", KindString, "stable", "update"),
		s("update_on_new", KindString, "notify", "update"),
		s("update_auto_time", KindString, "03:00", "update"),
		s("update_keep_versions", KindInt, "2", "update"),
		s("update_latest", KindString, "", "internal"),

		s("backup_enabled", KindBool, "false", "backup"),
		s("backup_time", KindString, "03:00", "backup"),
		s("backup_keep_daily", KindInt, "14", "backup"),
		s("backup_keep_weekly_months", KindInt, "3", "backup"),
		s("backup_local_keep", KindInt, "3", "backup"),
		s("backup_policy_id", KindInt, "0", "backup"),

		s("media_webp", KindBool, "false", "media"),
		s("media_thumb_sizes", KindJSON, "[]", "media"),
		s("media_strip_exif", KindBool, "true", "media"),
		s("media_max_bytes", KindInt, "20971520", "media"),
		s("media_allowed_types", KindJSON, "[]", "media"),

		s("mail_host", KindString, "", "mail"),
		s("mail_port", KindInt, "587", "mail"),
		s("mail_user", KindString, "", "mail"),
		s("mail_password_enc", KindSecret, "", "mail"),
		s("mail_from", KindString, "", "mail"),
		s("mail_tls", KindString, "starttls", "mail"),
		s("mail_enabled", KindBool, "false", "mail"),
	}
}
