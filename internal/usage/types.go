package usage

// Tokens is the provider-neutral breakdown of billable tokens.
type Tokens struct {
	UncachedInput     int64
	CacheRead         int64
	CacheWrite5M      int64
	CacheWrite1H      int64
	CacheWriteUnknown int64
	Output            int64
}

// State describes the completeness of a usage result.
type State string

const (
	StateComplete      State = "complete"
	StatePartial       State = "partial"
	StateMissing       State = "missing"
	StateNotApplicable State = "not_applicable"
)

// DiagnosticCode identifies a fixed usage extraction diagnostic.
type DiagnosticCode string

const (
	DiagnosticUnsupportedBillableDetail DiagnosticCode = "unsupported_billable_detail"
	DiagnosticCacheWriteDefaulted5M     DiagnosticCode = "cache_write_defaulted_5m"
	DiagnosticNegativeValue             DiagnosticCode = "negative_value"
	DiagnosticInvalidNumber             DiagnosticCode = "invalid_number"
	DiagnosticMissingRequiredField      DiagnosticCode = "missing_required_field"
	DiagnosticInconsistentTotal         DiagnosticCode = "inconsistent_total"
	DiagnosticInvalidPayload            DiagnosticCode = "invalid_payload"
	DiagnosticInvalidEventSequence      DiagnosticCode = "invalid_event_sequence"
)

const (
	diagnosticUnsupportedBillableDetail uint16 = 1 << iota
	diagnosticCacheWriteDefaulted5M
	diagnosticNegativeValue
	diagnosticInvalidNumber
	diagnosticMissingRequiredField
	diagnosticInconsistentTotal
	diagnosticInvalidPayload
	diagnosticInvalidEventSequence
)

// Diagnostics is a fixed set of usage extraction diagnostics.
type Diagnostics struct {
	flags        uint16
	totalDelta   int64
	deltaPresent bool
}

// Add records code when it is a supported diagnostic code.
func (d *Diagnostics) Add(code DiagnosticCode) {
	switch code {
	case DiagnosticUnsupportedBillableDetail:
		d.flags |= diagnosticUnsupportedBillableDetail
	case DiagnosticCacheWriteDefaulted5M:
		d.flags |= diagnosticCacheWriteDefaulted5M
	case DiagnosticNegativeValue:
		d.flags |= diagnosticNegativeValue
	case DiagnosticInvalidNumber:
		d.flags |= diagnosticInvalidNumber
	case DiagnosticMissingRequiredField:
		d.flags |= diagnosticMissingRequiredField
	case DiagnosticInconsistentTotal:
		d.flags |= diagnosticInconsistentTotal
	case DiagnosticInvalidPayload:
		d.flags |= diagnosticInvalidPayload
	case DiagnosticInvalidEventSequence:
		d.flags |= diagnosticInvalidEventSequence
	}
}

// Has reports whether code was recorded.
func (d Diagnostics) Has(code DiagnosticCode) bool {
	switch code {
	case DiagnosticUnsupportedBillableDetail:
		return d.flags&diagnosticUnsupportedBillableDetail != 0
	case DiagnosticCacheWriteDefaulted5M:
		return d.flags&diagnosticCacheWriteDefaulted5M != 0
	case DiagnosticNegativeValue:
		return d.flags&diagnosticNegativeValue != 0
	case DiagnosticInvalidNumber:
		return d.flags&diagnosticInvalidNumber != 0
	case DiagnosticMissingRequiredField:
		return d.flags&diagnosticMissingRequiredField != 0
	case DiagnosticInconsistentTotal:
		return d.flags&diagnosticInconsistentTotal != 0
	case DiagnosticInvalidPayload:
		return d.flags&diagnosticInvalidPayload != 0
	case DiagnosticInvalidEventSequence:
		return d.flags&diagnosticInvalidEventSequence != 0
	default:
		return false
	}
}

// SetTotalDelta records the difference between a reported and derived total.
func (d *Diagnostics) SetTotalDelta(delta int64) {
	d.Add(DiagnosticInconsistentTotal)
	d.totalDelta = delta
	d.deltaPresent = true
}

// TotalDelta returns the recorded total difference.
func (d Diagnostics) TotalDelta() (int64, bool) {
	return d.totalDelta, d.deltaPresent
}

// Merge combines supported diagnostic flags and uses other's present delta.
func (d *Diagnostics) Merge(other Diagnostics) {
	d.flags |= other.flags
	if other.deltaPresent {
		d.totalDelta = other.totalDelta
		d.deltaPresent = true
	}
}

// Result is a finalized provider-neutral usage result.
type Result struct {
	Tokens      Tokens
	State       State
	Diagnostics Diagnostics
}

// Patch updates one or more token fields in an accumulator.
type Patch struct {
	UncachedInput     *int64
	CacheRead         *int64
	CacheWrite5M      *int64
	CacheWrite1H      *int64
	CacheWriteUnknown *int64
	Output            *int64
	Final             bool
	Diagnostics       Diagnostics
}
