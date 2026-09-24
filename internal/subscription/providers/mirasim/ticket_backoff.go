package mirasim

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"
)

// TicketBackoffError preserves the upstream status while preventing another
// device-session mint before the bounded retry window has elapsed.
type TicketBackoffError struct {
	cause      error
	retryAfter time.Duration
}

func newTicketBackoffError(cause error, retryAfter time.Duration) *TicketBackoffError {
	if retryAfter < 0 {
		retryAfter = 0
	}
	return &TicketBackoffError{cause: cause, retryAfter: retryAfter}
}

func (e *TicketBackoffError) Error() string {
	if e == nil {
		return "Mirasim device ticket mint is backing off"
	}
	if e.cause == nil {
		return fmt.Sprintf("Mirasim device ticket mint is backing off for %s", e.retryAfter.Round(time.Millisecond))
	}
	return fmt.Sprintf("Mirasim device ticket mint is backing off for %s: %v", e.retryAfter.Round(time.Millisecond), e.cause)
}

func (e *TicketBackoffError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *TicketBackoffError) StatusCode() int {
	if e != nil {
		var status interface{ StatusCode() int }
		if errors.As(e.cause, &status) && status != nil {
			return status.StatusCode()
		}
	}
	return http.StatusServiceUnavailable
}

func (e *TicketBackoffError) RetryAfter() *time.Duration {
	if e == nil {
		return nil
	}
	value := e.retryAfter
	return &value
}

func (e *TicketBackoffError) Retryable() bool {
	if e != nil {
		var classified interface{ Retryable() bool }
		if errors.As(e.cause, &classified) && classified != nil {
			return classified.Retryable()
		}
	}
	return true
}

func (c *Client) nowTime() time.Time {
	if c.now == nil {
		return time.Now()
	}
	return c.now()
}

func (c *Client) markAccessRefreshRequired() {
	c.mu.Lock()
	c.refreshRequired = true
	c.mu.Unlock()
}

func (c *Client) clearTicketLocked() {
	c.invalidateTicketLocked()
	c.ticketRefusedUntil = time.Time{}
	c.resetTicketBackoffLocked()
}

func (c *Client) invalidateTicketLocked() {
	c.ticket = ""
	c.ticketExpiresAt = time.Time{}
}

func (c *Client) resetTicketBackoffLocked() {
	c.ticketRetryAt = time.Time{}
	c.ticketUnmintableUntil = time.Time{}
	c.ticketFailures = 0
	c.ticketLastError = nil
}

// ticketUnmintableWindow reports how long to stop minting after the relay says
// it has no device-session route, and zero for a failure that retrying can fix.
func ticketUnmintableWindow(status int) time.Duration {
	switch status {
	case http.StatusNotFound:
		return ticketRouteAbsentQuiet
	case http.StatusNotImplemented:
		return ticketUnimplementedQuiet
	}
	return 0
}

func (c *Client) noteTicketFailureLocked(cause error, headers http.Header, retryable bool) {
	now := c.nowTime()
	delay := ticketBackoffMax
	if retryable {
		delay = ticketBackoffBase
		for attempt := 0; attempt < c.ticketFailures && delay < ticketBackoffMax; attempt++ {
			delay *= 2
			if delay > ticketBackoffMax {
				delay = ticketBackoffMax
			}
		}
		c.ticketFailures++
	}
	if retryAfter := parseRetryAfter(headers, now); retryAfter != nil {
		delay = *retryAfter
	}
	if delay < ticketBackoffBase {
		delay = ticketBackoffBase
	}
	if delay > ticketRetryMax {
		delay = ticketRetryMax
	}
	c.ticketRetryAt = now.Add(delay)
	c.ticketLastError = cause
}

func (c *Client) staleTicketOrErrorLocked(now time.Time, cause error) (string, error) {
	if c.ticket != "" && now.Before(c.ticketExpiresAt) {
		return c.ticket, nil
	}
	return "", cause
}

// refuseTicketLocked mirrors the official client's refusal floor: the first
// relay 401 invalidates the ticket, while another rejection within the floor
// delegates access-token refresh to CPA without creating a mint storm.
func (c *Client) refuseTicketLocked() error {
	now := c.nowTime()
	if now.Before(c.ticketRefusedUntil) {
		c.refreshRequired = true
		cause := NewStatusError(http.StatusUnauthorized, []byte(`{"error":"Mirasim relay rejected the current device ticket"}`), nil)
		return newTicketBackoffError(cause, c.ticketRefusedUntil.Sub(now))
	}
	c.ticketRefusedUntil = now.Add(ticketRefusalFloor)
	c.invalidateTicketLocked()
	return nil
}

func resolveTicketExpiry(now time.Time, expiresIn, expiresAt *float64) time.Time {
	if now.IsZero() {
		now = time.Now()
	}
	if expiresIn != nil && finitePositive(*expiresIn) {
		maxSeconds := float64(math.MaxInt64) / float64(time.Second)
		if *expiresIn <= maxSeconds {
			candidate := now.Add(time.Duration(*expiresIn * float64(time.Second)))
			if candidate.After(now) {
				return candidate
			}
		}
	}
	if expiresAt != nil && finitePositive(*expiresAt) && *expiresAt <= float64(math.MaxInt64) {
		seconds, fraction := math.Modf(*expiresAt)
		candidate := time.Unix(int64(seconds), int64(fraction*float64(time.Second))).UTC()
		if candidate.After(now) {
			return candidate
		}
	}
	return now.Add(ticketDefaultTTL)
}

func finitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func sameInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
