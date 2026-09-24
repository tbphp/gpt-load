package codexrouting

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/platform/encryption"
)

const persistedVersion = 1

type Kind string

const (
	KindBusiness Kind = "business"
	KindProbe    Kind = "probe"
)

type Action string

const (
	ActionInject  Action = "inject"
	ActionReuse   Action = "reuse"
	ActionEmpty   Action = "empty"
	ActionWrite   Action = "write"
	ActionRotated Action = "rotated"
	ActionSkip    Action = "skip"
)

type Verdict string

const (
	VerdictEmpty    Verdict = "empty"
	VerdictProbing  Verdict = "probing"
	VerdictPinned   Verdict = "pinned"
	VerdictRotated  Verdict = "rotated"
	VerdictStale    Verdict = "stale"
	VerdictDegraded Verdict = "degraded"
)

type AccountRef struct {
	CredentialID uint
	GroupID      uint
	GroupName    string
}

type Entry struct {
	CredentialID      uint              `json:"credential_id"`
	Cookies           []Cookie          `json:"cookies"`
	Region            string            `json:"region"`
	Host              string            `json:"host"`
	ExpiresAt         time.Time         `json:"expires_at"`
	IssuedAt          time.Time         `json:"issued_at"`
	ProbeSession      string            `json:"probe_session,omitempty"`
	ProbeColo         string            `json:"probe_colo,omitempty"`
	EdgeRegion        string            `json:"edge_region,omitempty"`
	LastAction        Action            `json:"last_action,omitempty"`
	BufferingEnabled  *bool             `json:"buffering_enabled,omitempty"`
	FasterModel       string            `json:"faster_model,omitempty"`
	TurnStateLen      int               `json:"turn_state_len,omitempty"`
	LastModel         string            `json:"last_model,omitempty"`
	LastReportedModel string            `json:"last_reported_model,omitempty"`
	LastProbeAt       time.Time         `json:"last_probe_at,omitempty"`
	LastBusinessAt    time.Time         `json:"last_business_at,omitempty"`
	InjectSuccess     bool              `json:"inject_success,omitempty"`
	CandyOK           bool              `json:"candy_ok,omitempty"`
	EdgeIP            string            `json:"edge_ip,omitempty"`
	ForceNewSession   bool              `json:"force_new_session,omitempty"`
	LastProxyRegion   string            `json:"last_proxy_region,omitempty"`
	Tickets           map[string]Ticket `json:"tickets,omitempty"`
	PendingInject     bool              `json:"-"`
}

type Event struct {
	TimeMS           int64    `json:"time_ms"`
	Kind             string   `json:"kind"`
	Action           string   `json:"action"`
	CredentialID     uint     `json:"credential_id"`
	Model            string   `json:"model,omitempty"`
	ReportedModel    string   `json:"reported_model,omitempty"`
	Region           string   `json:"region,omitempty"`
	PreviousRegion   string   `json:"previous_region,omitempty"`
	CookieNames      []string `json:"cookie_names,omitempty"`
	BufferingEnabled *bool    `json:"buffering_enabled,omitempty"`
	FasterModel      string   `json:"faster_model,omitempty"`
	TurnStateLen     int      `json:"turn_state_len,omitempty"`
	CFColo           string   `json:"cf_colo,omitempty"`
	EdgeRegion       string   `json:"edge_region,omitempty"`
	EdgeIP           string   `json:"edge_ip,omitempty"`
	ProxyRegion      string   `json:"proxy_region,omitempty"`
	Success          bool     `json:"success"`
	StatusCode       int      `json:"status_code,omitempty"`
}

type Status struct {
	Enabled              bool               `json:"enabled"`
	ProbeProxyConfigured bool               `json:"probe_proxy_configured"`
	EdgeIP               string             `json:"edge_ip,omitempty"`
	TargetGateway        string             `json:"target_gateway,omitempty"`
	Transparent          bool               `json:"transparent"`
	Mint                 bool               `json:"mint"`
	MaxRotates           int                `json:"max_rotates,omitempty"`
	ProbeRegions         []string           `json:"probe_regions,omitempty"`
	TicketTTLSeconds     int64              `json:"ticket_ttl_seconds,omitempty"`
	ObservedAtMS         int64              `json:"observed_at_ms"`
	Counts               StatusCounts       `json:"counts"`
	Credentials          []CredentialStatus `json:"credentials"`
}

type StatusCounts struct {
	Empty    int `json:"empty"`
	Probing  int `json:"probing"`
	Pinned   int `json:"pinned"`
	Rotated  int `json:"rotated"`
	Stale    int `json:"stale"`
	Degraded int `json:"degraded"`
}

type CredentialStatus struct {
	CredentialID      uint   `json:"credential_id"`
	GroupID           uint   `json:"group_id"`
	GroupName         string `json:"group_name"`
	Region            string `json:"region"`
	Verdict           string `json:"verdict"`
	LastAction        string `json:"last_action"`
	InjectSuccess     bool   `json:"inject_success"`
	ExpiresAtMS       *int64 `json:"expires_at_ms,omitempty"`
	TTLSeconds        *int64 `json:"ttl_seconds,omitempty"`
	ProbeColo         string `json:"probe_colo,omitempty"`
	EdgeRegion        string `json:"edge_region,omitempty"`
	BufferingEnabled  *bool  `json:"buffering_enabled,omitempty"`
	FasterModel       string `json:"faster_model,omitempty"`
	LastModel         string `json:"last_model,omitempty"`
	LastReportedModel string `json:"last_reported_model,omitempty"`
	LastProbeAtMS     *int64 `json:"last_probe_at_ms,omitempty"`
	LastBusinessAtMS  *int64 `json:"last_business_at_ms,omitempty"`
	EdgeIP            string `json:"edge_ip,omitempty"`
	CandyOK           bool   `json:"candy_ok,omitempty"`
	TicketLen         int    `json:"ticket_len,omitempty"`
	TicketTTLSeconds  *int64 `json:"ticket_ttl_seconds,omitempty"`
	TicketExpiresAtMS *int64 `json:"ticket_expires_at_ms,omitempty"`
	ServedModel       string `json:"served_model,omitempty"`
	ProxyRegion       string `json:"proxy_region,omitempty"`
}

type persistedJar struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
}

type Store struct {
	mu         sync.Mutex
	cfg        Config
	dataDir    string
	encryption encryption.Service
	now        func() time.Time
	entries    map[uint]*Entry
	events     []Event
	probing    map[uint]struct{}
}

func NewStore(dataDir string, enc encryption.Service, cfg Config) *Store {
	if cfg.EventLimit <= 0 {
		cfg.EventLimit = defaultEventLimit
	}
	if cfg.RefreshBefore <= 0 {
		cfg.RefreshBefore = defaultRefreshBefore
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultInterval
	}
	if len(cfg.Models) == 0 {
		cfg.Models = []string{"gpt-6-astra"}
	}
	if cfg.MaxRotates <= 0 {
		cfg.MaxRotates = defaultMaxRotates
	}
	store := &Store{
		cfg:        cfg,
		dataDir:    dataDir,
		encryption: enc,
		now:        time.Now,
		entries:    make(map[uint]*Entry),
		probing:    make(map[uint]struct{}),
	}
	store.load()
	return store
}

func (s *Store) Config() Config {
	if s == nil {
		return Config{}
	}
	return s.cfg
}

func (s *Store) Inject(ctx context.Context, credentialID uint, model string, headers http.Header) {
	if s == nil || !s.cfg.Enabled || headers == nil || credentialID == 0 {
		return
	}
	if IsDiscovery(ctx) {
		return
	}
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[credentialID]
	edgeIP := s.edgeIPLocked(entry)
	if edgeIP != "" {
		headers.Set("X-Edge-IP", edgeIP)
	}
	if entry == nil || cookieHeader(entry.Cookies) == "" || (s.requiresPinLocked() && (!entry.CandyOK || !gatewayAllowed(s.cfg.TargetGateway, entry.Region))) {
		s.appendEventLocked(Event{
			TimeMS: now.UnixMilli(), Kind: string(KindBusiness), Action: string(ActionEmpty),
			CredentialID: credentialID, Model: model, EdgeIP: edgeIP,
		})
		return
	}
	if !entry.ExpiresAt.IsZero() && !entry.ExpiresAt.After(now) {
		s.appendEventLocked(Event{
			TimeMS: now.UnixMilli(), Kind: string(KindBusiness), Action: string(ActionSkip),
			CredentialID: credentialID, Model: model, Region: entry.Region, EdgeIP: edgeIP,
		})
		return
	}
	headers.Set("Cookie", cookieHeader(entry.Cookies))
	if s.cfg.Mint {
		if ticket := s.ticketValueLocked(entry, model, now); ticket != "" {
			headers.Set("X-Codex-Turn-State", ticket)
		}
	}
	action := ActionReuse
	if entry.LastAction == ActionWrite || entry.LastAction == ActionEmpty || entry.LastAction == "" {
		action = ActionInject
	}
	entry.LastAction = action
	entry.PendingInject = true
	entry.LastModel = model
	s.appendEventLocked(Event{
		TimeMS: now.UnixMilli(), Kind: string(KindBusiness), Action: string(action),
		CredentialID: credentialID, Model: model, Region: entry.Region,
		CookieNames: cookieNames(entry.Cookies), EdgeIP: edgeIP, Success: true,
	})
}

func (s *Store) Capture(ctx context.Context, credentialID uint, model string, headers http.Header, statusCode int) {
	if s == nil || !s.cfg.Enabled || headers == nil || credentialID == 0 {
		return
	}
	now := s.now().UTC()
	incoming := parseSetCookies(headers)
	buffering := optionalBoolHeader(headers, "X-Codex-Safety-Buffering-Enabled")
	faster := strings.TrimSpace(headers.Get("X-Codex-Safety-Buffering-Faster-Model"))
	turnLen := len(strings.TrimSpace(headers.Get("X-Codex-Turn-State")))
	colo := cfRayColo(headers)
	edgeRegion := strings.TrimSpace(headers.Get("X-Edge-Region"))
	kind := KindBusiness
	if IsDiscovery(ctx) {
		kind = KindProbe
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.ensureEntryLocked(credentialID)
	previousRegion := entry.Region
	injected := entry.PendingInject
	entry.PendingInject = false
	if kind == KindProbe {
		entry.LastProbeAt = now
		if colo != "" {
			entry.ProbeColo = colo
		}
	} else {
		entry.LastBusinessAt = now
	}
	if model != "" {
		entry.LastModel = model
	}
	if buffering != nil {
		entry.BufferingEnabled = buffering
	}
	if faster != "" {
		entry.FasterModel = faster
	}
	if turnLen > 0 {
		entry.TurnStateLen = turnLen
	}
	if edgeRegion != "" {
		entry.EdgeRegion = edgeRegion
	}

	action := ActionEmpty
	success := false
	region := entry.Region
	if oailb, ok := cookieByName(incoming, cookieOaiLB); ok {
		host, parsedRegion, expires, parsed := parseOaiLB(oailb.Value)
		entry.Cookies = mergeCookies(entry.Cookies, incoming)
		if parsed {
			entry.Host = host
			if parsedRegion != "" {
				region = parsedRegion
			}
			if !expires.IsZero() {
				entry.ExpiresAt = expires
			}
			if s.cfg.TicketTTL > 0 {
				capAt := now.Add(s.cfg.TicketTTL)
				if entry.ExpiresAt.IsZero() || entry.ExpiresAt.After(capAt) {
					entry.ExpiresAt = capAt
				}
			}
			if entry.IssuedAt.IsZero() {
				entry.IssuedAt = now
			}
		}
		if kind == KindProbe {
			action = ActionWrite
			success = region != ""
		} else if injected && previousRegion != "" && region != "" && region != previousRegion {
			action = ActionRotated
			entry.InjectSuccess = false
			success = false
		} else if injected {
			action = entry.LastAction
			if action == "" {
				action = ActionInject
			}
			entry.InjectSuccess = true
			success = true
		} else {
			action = ActionWrite
			success = region != ""
		}
	} else if injected {
		action = entry.LastAction
		if action == "" {
			action = ActionInject
		}
		entry.InjectSuccess = true
		success = true
		region = previousRegion
	} else if kind == KindProbe && previousRegion != "" {
		action = ActionReuse
		success = true
		region = previousRegion
	} else if kind == KindProbe {
		action = ActionEmpty
	}

	entry.Region = region
	entry.LastAction = action
	if kind == KindProbe && action == ActionWrite {
		entry.InjectSuccess = false
	}
	s.persistLocked()
	s.appendEventLocked(Event{
		TimeMS: now.UnixMilli(), Kind: string(kind), Action: string(action),
		CredentialID: credentialID, Model: model, Region: region, PreviousRegion: previousRegion,
		CookieNames: cookieNames(incoming), BufferingEnabled: buffering, FasterModel: faster,
		TurnStateLen: turnLen, CFColo: colo, EdgeRegion: edgeRegion, EdgeIP: s.edgeIPLocked(entry),
		ProxyRegion: entry.LastProxyRegion, Success: success, StatusCode: statusCode,
	})
}

func (s *Store) Status(accounts []AccountRef) Status {
	status := Status{
		Credentials: []CredentialStatus{},
	}
	if s == nil {
		return status
	}
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	status.Enabled = s.cfg.Enabled
	status.ProbeProxyConfigured = strings.TrimSpace(s.cfg.ProbeProxy) != ""
	status.EdgeIP = s.cfg.EdgeIP
	status.TargetGateway = s.cfg.TargetGateway
	status.Transparent = s.cfg.Transparent
	status.Mint = s.cfg.Mint
	status.MaxRotates = s.cfg.MaxRotates
	status.ProbeRegions = append([]string(nil), s.cfg.ProbeRegions...)
	if s.cfg.TicketTTL > 0 {
		status.TicketTTLSeconds = int64(s.cfg.TicketTTL / time.Second)
	}
	status.ObservedAtMS = now.UnixMilli()

	seen := make(map[uint]struct{}, len(accounts))
	for _, account := range accounts {
		if account.CredentialID == 0 {
			continue
		}
		seen[account.CredentialID] = struct{}{}
		status.Credentials = append(status.Credentials, s.credentialStatusLocked(account, now))
	}
	for id := range s.entries {
		if _, ok := seen[id]; ok {
			continue
		}
		status.Credentials = append(status.Credentials, s.credentialStatusLocked(AccountRef{CredentialID: id}, now))
	}
	sort.Slice(status.Credentials, func(i, j int) bool {
		return status.Credentials[i].CredentialID < status.Credentials[j].CredentialID
	})
	for _, item := range status.Credentials {
		switch Verdict(item.Verdict) {
		case VerdictProbing:
			status.Counts.Probing++
		case VerdictPinned:
			status.Counts.Pinned++
		case VerdictRotated:
			status.Counts.Rotated++
		case VerdictStale:
			status.Counts.Stale++
		case VerdictDegraded:
			status.Counts.Degraded++
		default:
			status.Counts.Empty++
		}
	}
	return status
}

func (s *Store) Events() []Event {
	if s == nil {
		return []Event{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

func (s *Store) Clear(credentialID uint) {
	if s == nil || credentialID == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, credentialID)
	delete(s.probing, credentialID)
	s.persistLocked()
}

func (s *Store) MarkProbing(credentialID uint) bool {
	if s == nil || credentialID == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.probing[credentialID]; exists {
		return false
	}
	s.probing[credentialID] = struct{}{}
	return true
}

func (s *Store) UnmarkProbing(credentialID uint) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.probing, credentialID)
}

func (s *Store) CookieHeader(credentialID uint) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[credentialID]
	if entry == nil {
		return ""
	}
	return cookieHeader(entry.Cookies)
}

func (s *Store) SessionFor(credentialID uint) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry := s.entries[credentialID]; entry != nil {
		return entry.ProbeSession
	}
	return ""
}

func (s *Store) NeedsNewSession(credentialID uint) bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[credentialID]
	if entry == nil || entry.ProbeSession == "" || entry.Region == "" || entry.ForceNewSession {
		return true
	}
	if s.cfg.Candy && !entry.CandyOK {
		return true
	}
	if s.cfg.TargetGateway != "" && !gatewayAllowed(s.cfg.TargetGateway, entry.Region) {
		return true
	}
	if entry.LastAction == ActionRotated {
		return true
	}
	return false
}

func (s *Store) HostFor(credentialID uint) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry := s.entries[credentialID]; entry != nil {
		return entry.Host
	}
	return ""
}

func (s *Store) ConfirmCandy(credentialID uint) {
	if s == nil || credentialID == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.ensureEntryLocked(credentialID)
	entry.CandyOK = true
	entry.ForceNewSession = false
	s.persistLocked()
}

func (s *Store) SetEdgeIP(credentialID uint, edgeIP string) {
	if s == nil || credentialID == 0 {
		return
	}
	edgeIP = strings.TrimSpace(edgeIP)
	if edgeIP == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.ensureEntryLocked(credentialID)
	entry.EdgeIP = edgeIP
	s.persistLocked()
}

func (s *Store) TouchProbe(credentialID uint) {
	if s == nil || credentialID == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.ensureEntryLocked(credentialID)
	entry.LastProbeAt = s.now().UTC()
}

func (s *Store) SetProbeEgress(credentialID uint, region string) {
	if s == nil || credentialID == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.ensureEntryLocked(credentialID)
	entry.LastProxyRegion = strings.ToUpper(strings.TrimSpace(region))
}

func (s *Store) SetTicket(credentialID uint, model, value, served string) {
	if s == nil || credentialID == 0 || strings.TrimSpace(value) == "" {
		return
	}
	now := s.now().UTC()
	issued := fernetIssuedAt(value, now)
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.ensureEntryLocked(credentialID)
	if entry.Tickets == nil {
		entry.Tickets = make(map[string]Ticket)
	}
	key := strings.TrimSpace(model)
	if key == "" {
		key = entry.LastModel
	}
	entry.Tickets[key] = Ticket{
		Model:     key,
		Value:     value,
		Len:       len(value),
		Served:    strings.TrimSpace(served),
		IssuedAt:  issued,
		ExpiresAt: issued.Add(s.cfg.TicketTTL),
	}
	s.persistLocked()
}

func (s *Store) DiscardPin(credentialID uint) {
	if s == nil || credentialID == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.ensureEntryLocked(credentialID)
	entry.Cookies = nil
	entry.Region = ""
	entry.Host = ""
	entry.CandyOK = false
	entry.EdgeIP = ""
	entry.Tickets = nil
	entry.InjectSuccess = false
	entry.ForceNewSession = true
	entry.LastAction = ActionEmpty
	s.persistLocked()
}

func (s *Store) requiresPinLocked() bool {
	return s.cfg.Candy || s.cfg.TargetGateway != ""
}

func (s *Store) ticketValueLocked(entry *Entry, model string, now time.Time) string {
	if entry == nil || entry.Tickets == nil {
		return ""
	}
	ticket, ok := entry.Tickets[model]
	if !ok || ticket.Value == "" {
		return ""
	}
	if !ticket.ExpiresAt.IsZero() && !ticket.ExpiresAt.After(now) {
		return ""
	}
	return ticket.Value
}

func (s *Store) edgeIPLocked(entry *Entry) string {
	if entry != nil && strings.TrimSpace(entry.EdgeIP) != "" {
		return entry.EdgeIP
	}
	return strings.TrimSpace(s.cfg.EdgeIP)
}

func (s *Store) AssignSession(credentialID uint, session string) {
	if s == nil || credentialID == 0 || session == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.ensureEntryLocked(credentialID)
	entry.ProbeSession = session
}

func (s *Store) credentialStatusLocked(account AccountRef, now time.Time) CredentialStatus {
	item := CredentialStatus{
		CredentialID: account.CredentialID,
		GroupID:      account.GroupID,
		GroupName:    account.GroupName,
		Verdict:      string(VerdictEmpty),
	}
	_, probing := s.probing[account.CredentialID]
	entry := s.entries[account.CredentialID]
	if entry == nil {
		if probing {
			item.Verdict = string(VerdictProbing)
		}
		return item
	}
	item.Region = entry.Region
	item.LastAction = string(entry.LastAction)
	item.InjectSuccess = entry.InjectSuccess
	item.ExpiresAtMS = unixMilliPtr(entry.ExpiresAt)
	item.TTLSeconds = ttlSeconds(entry.ExpiresAt, now)
	item.ProbeColo = entry.ProbeColo
	item.EdgeRegion = entry.EdgeRegion
	item.BufferingEnabled = cloneBool(entry.BufferingEnabled)
	item.FasterModel = entry.FasterModel
	item.LastModel = entry.LastModel
	item.LastReportedModel = entry.LastReportedModel
	item.LastProbeAtMS = unixMilliPtr(entry.LastProbeAt)
	item.LastBusinessAtMS = unixMilliPtr(entry.LastBusinessAt)
	item.EdgeIP = s.edgeIPLocked(entry)
	item.CandyOK = entry.CandyOK
	item.ProxyRegion = entry.LastProxyRegion
	if ticket, ok := entry.Tickets[entry.LastModel]; ok {
		item.TicketLen = ticket.Len
		item.TicketTTLSeconds = ttlSeconds(ticket.ExpiresAt, now)
		item.TicketExpiresAtMS = unixMilliPtr(ticket.ExpiresAt)
		item.ServedModel = ticket.Served
	}
	item.Verdict = string(s.verdictLocked(entry, probing, now))
	return item
}

func (s *Store) verdictLocked(entry *Entry, probing bool, now time.Time) Verdict {
	if probing {
		return VerdictProbing
	}
	if entry == nil || entry.Region == "" {
		return VerdictEmpty
	}
	if entry.LastAction == ActionRotated {
		return VerdictRotated
	}
	if !entry.ExpiresAt.IsZero() && entry.ExpiresAt.Sub(now) <= s.cfg.RefreshBefore {
		return VerdictStale
	}
	if entry.LastReportedModel != "" && entry.LastModel != "" && entry.LastReportedModel != entry.LastModel {
		return VerdictDegraded
	}
	if s.cfg.Candy && !entry.CandyOK {
		return VerdictDegraded
	}
	if s.cfg.TargetGateway != "" && !gatewayAllowed(s.cfg.TargetGateway, entry.Region) {
		return VerdictDegraded
	}
	return VerdictPinned
}

func (s *Store) ensureEntryLocked(credentialID uint) *Entry {
	entry := s.entries[credentialID]
	if entry == nil {
		entry = &Entry{CredentialID: credentialID}
		s.entries[credentialID] = entry
	}
	return entry
}

func (s *Store) appendEventLocked(event Event) {
	limit := s.cfg.EventLimit
	s.events = append(s.events, event)
	if len(s.events) > limit {
		s.events = append([]Event(nil), s.events[len(s.events)-limit:]...)
	}
}

func (s *Store) persistLocked() {
	if s.dataDir == "" || s.encryption == nil {
		return
	}
	payload := persistedJar{Version: persistedVersion, Entries: make(map[string]Entry, len(s.entries))}
	for id, entry := range s.entries {
		if entry == nil {
			continue
		}
		copied := *entry
		copied.Cookies = append([]Cookie(nil), entry.Cookies...)
		copied.BufferingEnabled = cloneBool(entry.BufferingEnabled)
		if entry.Tickets != nil {
			copied.Tickets = make(map[string]Ticket, len(entry.Tickets))
			for key, ticket := range entry.Tickets {
				copied.Tickets[key] = ticket
			}
		}
		payload.Entries[strconv.FormatUint(uint64(id), 10)] = copied
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		logrus.WithError(err).WithField("event", "codex_routing.persist").Warn("encode cookie jar")
		return
	}
	ciphertext, err := s.encryption.Encrypt(string(raw))
	if err != nil {
		logrus.WithError(err).WithField("event", "codex_routing.persist").Warn("encrypt cookie jar")
		return
	}
	path := filepath.Join(s.dataDir, jarFileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(ciphertext), 0o600); err != nil {
		logrus.WithError(err).WithField("event", "codex_routing.persist").Warn("write cookie jar")
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		logrus.WithError(err).WithField("event", "codex_routing.persist").Warn("replace cookie jar")
	}
}

func (s *Store) load() {
	if s.dataDir == "" || s.encryption == nil {
		return
	}
	path := filepath.Join(s.dataDir, jarFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logrus.WithError(err).WithField("event", "codex_routing.load").Warn("read cookie jar")
		}
		return
	}
	plaintext, err := s.encryption.Decrypt(string(raw))
	if err != nil {
		logrus.WithError(err).WithField("event", "codex_routing.load").Warn("decrypt cookie jar")
		return
	}
	var payload persistedJar
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		logrus.WithError(err).WithField("event", "codex_routing.load").Warn("decode cookie jar")
		return
	}
	for key, entry := range payload.Entries {
		id, err := strconv.ParseUint(key, 10, strconv.IntSize)
		if err != nil || id == 0 {
			continue
		}
		copied := entry
		copied.CredentialID = uint(id)
		copied.Cookies = append([]Cookie(nil), entry.Cookies...)
		copied.BufferingEnabled = cloneBool(entry.BufferingEnabled)
		if entry.Tickets != nil {
			copied.Tickets = make(map[string]Ticket, len(entry.Tickets))
			for key, ticket := range entry.Tickets {
				copied.Tickets[key] = ticket
			}
		}
		s.entries[uint(id)] = &copied
	}
}

func unixMilliPtr(value time.Time) *int64 {
	if value.IsZero() {
		return nil
	}
	ms := value.UnixMilli()
	return &ms
}

func ttlSeconds(expires, now time.Time) *int64 {
	if expires.IsZero() {
		return nil
	}
	sec := int64(expires.Sub(now).Seconds())
	return &sec
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
