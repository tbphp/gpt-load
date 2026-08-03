package requestlog

import (
	"gpt-load/internal/telemetry"
)

type queuedEvent struct {
	Event telemetry.RequestEvent
}
