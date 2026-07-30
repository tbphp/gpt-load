package locales

// MessagesEnUS contains English (US) control-plane translations.
var MessagesEnUS = map[string]string{
	"common.success":                                  "Success",
	"route.not_found":                                 "Route not found",
	"route.method_not_allowed":                        "Method not allowed",
	"bad_request":                                     "Bad request",
	"request_too_large":                               "Request body is too large",
	"bad_gateway":                                     "Upstream service error",
	"internal_error":                                  "Internal error",
	"idempotency.required":                            "Idempotency-Key is required",
	"idempotency.invalid":                             "Idempotency-Key must be a canonical UUID v4",
	"idempotency.reused":                              "Idempotency-Key was already used for another request",
	"idempotency.expired":                             "The idempotent result retention period expired",
	"control.operation_incomplete":                    "The resource was committed but runtime recovery is incomplete",
	"control.recovery_pending":                        "An earlier committed operation is still recovering",
	"settings.precondition_required":                  "If-Match is required",
	"settings.version_conflict":                       "Settings changed since they were loaded",
	"auth.invalid_key":                                "Invalid authorization key",
	"auth.locked":                                     "Too many authentication attempts; try again later",
	"group.not_found":                                 "Group not found",
	"group.name_exists":                               "Group name already exists",
	"group.in_use":                                    "The group is still referenced by access keys",
	"group.upstream_url_conflict":                     "An existing group already uses this upstream URL",
	"group.upstream_url_change_confirmation_required": "Changing the upstream URL requires explicit confirmation",
	"group.no_active_upstream_key":                    "No active upstream key is available for this group",
	"key.not_found":                                   "Key not found",
}
