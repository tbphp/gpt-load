package locales

// MessagesEnUS contains English (US) control-plane translations.
var MessagesEnUS = map[string]string{
	"common.success":               "Success",
	"bad_request":                  "Bad request",
	"request_too_large":            "Request body is too large",
	"bad_gateway":                  "Upstream service error",
	"internal_error":               "Internal error",
	"auth.invalid_key":             "Invalid authorization key",
	"group.not_found":              "Group not found",
	"group.name_exists":            "Group name already exists",
	"group.upstream_url_conflict":  "An existing group already uses this upstream URL",
	"group.no_active_upstream_key": "No active upstream key is available for this group",
	"key.not_found":                "Key not found",
}
