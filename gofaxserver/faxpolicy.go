package gofaxserver

// Fax policy engine: Postgres-backed replacement for the old FreeSWITCH
// mod_db softmodem fallback and the in-memory T.38 flip-flop map.
//
// Two call types are distinguished throughout:
//   - "softmodem" (non-bridged): SpanDSP terminates the fax (txfax/rxfax),
//     full result telemetry is available, so the adaptive escalation ladder
//     (T.38 -> ECM -> V.17) and success-healing apply here.
//   - "bridge" (transcoded): FreeSWITCH transcodes via t38_gateway and the
//     far end terminates the fax. There is no fax result telemetry, so
//     flip-flop probing plus (mostly manual) rules drive T.38 policy.
//
// Rules are composable: every matching, enabled, unexpired rule is collected
// into an "applied" set, and per attribute (T.38 / ECM / V.17) the most
// specific rule wins (pair > dst > src), manual beats auto, and at equal
// specificity "off" beats "on" (fail-safe).

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gofaxserver/gofaxlib"
)

// Call types the policy engine distinguishes.
const (
	CallTypeSoftmodem = "softmodem"
	CallTypeBridge    = "bridge"
)

// Rule scopes.
const (
	PolicyScopeDst  = "dst"
	PolicyScopeSrc  = "src"
	PolicyScopePair = "pair"
)

// Rule effects (single-purpose, composable).
const (
	PolicyEffectT38Off        = "t38_off"
	PolicyEffectT38On         = "t38_on"
	PolicyEffectECMOff        = "ecm_off"
	PolicyEffectECMOn         = "ecm_on"
	PolicyEffectV17Off        = "v17_off"
	PolicyEffectSoftmodemOnly = "softmodem_only" // never request/accept T.38
)

// Rule origins.
const (
	PolicyOriginManual = "manual"
	PolicyOriginAuto   = "auto"
)

// FaxPolicyRule is a single T.38 / ECM / V.17 policy rule stored in Postgres.
type FaxPolicyRule struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	Scope     string     `gorm:"index" json:"scope"` // dst | src | pair
	SrcNumber string     `gorm:"index" json:"src_number"`
	DstNumber string     `gorm:"index" json:"dst_number"`
	Effect    string     `json:"effect"`              // t38_off | t38_on | ecm_off | ecm_on | v17_off | softmodem_only
	AppliesTo string     `json:"applies_to"`          // both | softmodem | bridge
	Origin    string     `gorm:"index" json:"origin"` // manual | auto
	Enabled   bool       `json:"enabled"`
	ExpiresAt *time.Time `json:"expires_at"` // nil = never expires

	// Learning statistics (maintained for auto rules; informational for manual).
	FailureCount  int        `json:"failure_count"`
	SuccessCount  int        `json:"success_count"`
	LastSeenAt    *time.Time `json:"last_seen_at"`
	LastT38Status string     `json:"last_t38_status"`

	Notes     string    `json:"notes"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// FaxPairState persists the flip-flop probing state for a src/dst pair and
// call type. It replaces the old in-memory t38PairState map so probing
// survives restarts and is visible to admins.
type FaxPairState struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	SrcNumber   string    `gorm:"uniqueIndex:idx_pair_state" json:"src_number"`
	DstNumber   string    `gorm:"uniqueIndex:idx_pair_state" json:"dst_number"`
	CallType    string    `gorm:"uniqueIndex:idx_pair_state" json:"call_type"` // softmodem | bridge
	LastUsedT38 bool      `json:"last_used_t38"`
	LastSeen    time.Time `json:"last_seen"`
}

// FaxPolicy is the resolved outcome for one call.
type FaxPolicy struct {
	EnableT38  bool  `json:"enable_t38"`
	RequestT38 bool  `json:"request_t38"`
	UseECM     *bool `json:"use_ecm"`     // nil = keep job/default
	DisableV17 *bool `json:"disable_v17"` // nil = keep job/default

	// T38Decided is true when a rule (not flip-flop/defaults) decided T.38;
	// flip-flop probing must not override it.
	T38Decided bool `json:"t38_decided"`
	// T38ForcedOff is true when a rule forced T.38 off (softmodem fallback);
	// callers use it to skip pair-state updates and flag the job.
	T38ForcedOff  bool `json:"t38_forced_off"`
	SoftmodemOnly bool `json:"softmodem_only"`

	AppliedRuleIDs []uint `json:"applied_rule_ids"`
}

// DefaultFaxPolicy returns the config-default policy with no rules applied.
func DefaultFaxPolicy() FaxPolicy {
	return FaxPolicy{
		EnableT38:  gofaxlib.Config.Faxing.EnableT38,
		RequestT38: gofaxlib.Config.Faxing.RequestT38,
	}
}

func policyCfg() gofaxlib.FaxPolicyConfig { return gofaxlib.Config.Faxing.Policy }

func autoRuleTTL() time.Duration {
	if d, err := time.ParseDuration(policyCfg().AutoRuleTTL); err == nil && d > 0 {
		return d
	}
	return 720 * time.Hour
}

// PairStateTTL is how long flip-flop probing state is remembered.
func PairStateTTL() time.Duration {
	if d, err := time.ParseDuration(policyCfg().PairStateTTL); err == nil && d > 0 {
		return d
	}
	return 15 * time.Minute
}

func scopeRank(scope string) int {
	switch scope {
	case PolicyScopePair:
		return 0
	case PolicyScopeDst:
		return 1
	default:
		return 2
	}
}

func originRank(origin string) int {
	if origin == PolicyOriginManual {
		return 0
	}
	return 1
}

// effectIsOff ranks "off"/restrictive effects before permissive ones so that
// at equal specificity the fail-safe (restrictive) rule wins.
func effectRank(effect string) int {
	switch effect {
	case PolicyEffectT38On, PolicyEffectECMOn:
		return 1
	default:
		return 0
	}
}

// ruleMatches reports whether r applies to the given src/dst/callType.
//
// Scoping is strict for manual rules. Auto "dst" rules additionally match on
// the src side: they encode a learned capability of a *remote number*
// ("this endpoint can't do T.38"), which must protect calls in both
// directions — e.g. an inbound rxfax must refuse the far end's T.38
// re-INVITE when we already know negotiation will fail.
func ruleMatches(r *FaxPolicyRule, srcNum, dstNum, callType string, now time.Time) bool {
	if !r.Enabled {
		return false
	}
	if r.ExpiresAt != nil && now.After(*r.ExpiresAt) {
		return false
	}
	switch r.AppliesTo {
	case "", "both":
	case callType:
	default:
		return false
	}
	switch r.Scope {
	case PolicyScopePair:
		return r.SrcNumber == srcNum && r.DstNumber == dstNum
	case PolicyScopeDst:
		if r.DstNumber == dstNum {
			return true
		}
		return r.Origin == PolicyOriginAuto && r.DstNumber == srcNum
	case PolicyScopeSrc:
		return r.SrcNumber == srcNum
	}
	return false
}

// ResolveFaxPolicy collects all matching rules and resolves the effective
// T.38 / ECM / V.17 policy for a call. When the engine is disabled it
// returns config defaults with T38Decided=false (flip-flop proceeds).
func (s *Server) ResolveFaxPolicy(srcNum, dstNum, callType string) FaxPolicy {
	policy := DefaultFaxPolicy()
	if !policyCfg().PolicyEnabled() {
		return policy
	}

	rulesPtr := s.faxPolicies.Load()
	if rulesPtr == nil {
		return policy
	}

	now := time.Now()
	var matches []FaxPolicyRule
	for _, r := range *rulesPtr {
		if ruleMatches(&r, srcNum, dstNum, callType, now) {
			matches = append(matches, r)
		}
	}
	if len(matches) == 0 {
		return policy
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if a, b := scopeRank(matches[i].Scope), scopeRank(matches[j].Scope); a != b {
			return a < b
		}
		if a, b := originRank(matches[i].Origin), originRank(matches[j].Origin); a != b {
			return a < b
		}
		return effectRank(matches[i].Effect) < effectRank(matches[j].Effect)
	})

	for _, r := range matches {
		policy.AppliedRuleIDs = append(policy.AppliedRuleIDs, r.ID)
	}

	// Per attribute, the first (most specific / manual / fail-safe) rule wins.
	t38Decided := false
	for _, r := range matches {
		switch r.Effect {
		case PolicyEffectSoftmodemOnly:
			if !t38Decided {
				t38Decided = true
				policy.SoftmodemOnly = true
				policy.EnableT38 = false
				policy.RequestT38 = false
			}
		case PolicyEffectT38Off:
			if !t38Decided {
				t38Decided = true
				policy.EnableT38 = false
				policy.RequestT38 = false
			}
		case PolicyEffectT38On:
			if !t38Decided {
				t38Decided = true
				policy.EnableT38 = true
				policy.RequestT38 = gofaxlib.Config.Faxing.RequestT38
			}
		case PolicyEffectECMOff:
			if policy.UseECM == nil {
				b := false
				policy.UseECM = &b
			}
		case PolicyEffectECMOn:
			if policy.UseECM == nil {
				b := true
				policy.UseECM = &b
			}
		case PolicyEffectV17Off:
			if policy.DisableV17 == nil {
				b := true
				policy.DisableV17 = &b
			}
		}
	}

	policy.T38Decided = t38Decided
	policy.T38ForcedOff = t38Decided && !policy.EnableT38
	return policy
}

// reloadFaxPolicies reloads all rules from Postgres and swaps the cache
// atomically. Expired auto rules are pruned from the DB on load.
func (s *Server) reloadFaxPolicies() error {
	var rules []FaxPolicyRule
	if err := s.DB.Find(&rules).Error; err != nil {
		return err
	}

	now := time.Now()
	kept := rules[:0]
	for _, r := range rules {
		if r.ExpiresAt != nil && now.After(*r.ExpiresAt) {
			if r.Origin == PolicyOriginAuto {
				// Prune expired auto rules (re-probing happens naturally).
				s.DB.Delete(&FaxPolicyRule{}, r.ID)
				continue
			}
		}
		kept = append(kept, r)
	}
	s.faxPolicies.Store(&kept)
	return nil
}

// ------------------------- auto-learning ---------------------------------

// learnResultBadRows sums bad rows across page results.
func learnResultBadRows(result *gofaxlib.FaxResult) uint {
	var badrows uint
	for _, p := range result.PageResults {
		badrows += p.BadRows
	}
	return badrows
}

// T38Refused reports whether the result indicates the far end offered or was
// offered T.38 but negotiation failed — the classic "offers T.38 then
// refuses" case.
func T38Refused(result *gofaxlib.FaxResult) bool {
	if result == nil {
		return false
	}
	st := strings.TrimSpace(result.T38Status)
	return st != "" && !strings.EqualFold(st, "negotiated")
}

// qualifiesForLearning reports whether a failed result carries a fax-level
// failure signature worth learning from (not e.g. a busy/no-answer).
func qualifiesForLearning(result *gofaxlib.FaxResult) bool {
	if result == nil || result.Success {
		return false
	}
	return result.NegotiateCount > 1 || learnResultBadRows(result) > 0 || T38Refused(result)
}

// isT38NegotiationHangup reports whether a hangup cause indicates a media /
// T.38 negotiation failure at the SIP level (used for bridge auto-learning,
// where no fax telemetry exists).
func isT38NegotiationHangup(hangupCause string) bool {
	switch strings.ToUpper(strings.TrimSpace(hangupCause)) {
	case "INCOMPATIBLE_DESTINATION",
		"NOT_ACCEPTABLE_HERE",
		"MEDIA_NEGOTIATION_FAILED",
		"BEARERCAPABILITY_NOTAVAIL",
		"BEARERCAPABILITY_NOTIMPL",
		"FACILITY_REJECTED":
		return true
	}
	return false
}

// findAutoRule locates an existing auto rule for a remote number/effect.
func (s *Server) findAutoRule(remoteNumber, effect, appliesTo string) *FaxPolicyRule {
	var rule FaxPolicyRule
	err := s.DB.Where(
		"origin = ? AND scope = ? AND dst_number = ? AND effect = ? AND applies_to = ?",
		PolicyOriginAuto, PolicyScopeDst, remoteNumber, effect, appliesTo,
	).First(&rule).Error
	if err != nil {
		return nil
	}
	return &rule
}

// upsertAutoRule creates or refreshes an auto rule for a remote number.
func (s *Server) upsertAutoRule(remoteNumber, effect, appliesTo, t38Status string, failureCount int) (*FaxPolicyRule, error) {
	now := time.Now()
	expires := now.Add(autoRuleTTL())

	if existing := s.findAutoRule(remoteNumber, effect, appliesTo); existing != nil {
		existing.FailureCount = failureCount
		existing.SuccessCount = 0
		existing.Enabled = true
		existing.ExpiresAt = &expires
		existing.LastSeenAt = &now
		existing.LastT38Status = t38Status
		if err := s.DB.Save(existing).Error; err != nil {
			return nil, err
		}
		return existing, nil
	}

	rule := &FaxPolicyRule{
		Scope:         PolicyScopeDst,
		DstNumber:     remoteNumber,
		Effect:        effect,
		AppliesTo:     appliesTo,
		Origin:        PolicyOriginAuto,
		Enabled:       true,
		ExpiresAt:     &expires,
		FailureCount:  failureCount,
		LastSeenAt:    &now,
		LastT38Status: t38Status,
		Notes:         "auto-learned from fax failures",
	}
	if err := s.DB.Create(rule).Error; err != nil {
		return nil, err
	}
	return rule, nil
}

// LearnFaxPolicyFailure records a qualifying fax failure against a remote
// number (the callee for outbound, the caller for inbound) and escalates the
// auto rule ladder: t38_off (applies to both call types, so bridged calls
// stop requesting/accepting T.38 to a known-bad endpoint) -> ecm_off ->
// v17_off. Used on the softmodem path where full telemetry exists.
func (s *Server) LearnFaxPolicyFailure(remoteNumber string, result *gofaxlib.FaxResult) {
	cfg := policyCfg()
	if !cfg.PolicyEnabled() || !cfg.LearningEnabled() || remoteNumber == "" {
		return
	}

	t38Status := ""
	if result != nil {
		t38Status = result.T38Status
	}

	// The t38_off rule carries the failure counter for the ladder.
	t38Rule := s.findAutoRule(remoteNumber, PolicyEffectT38Off, "both")
	failures := 1
	if t38Rule != nil {
		failures = t38Rule.FailureCount + 1
	}

	if failures < cfg.T38Threshold() {
		return
	}
	if _, err := s.upsertAutoRule(remoteNumber, PolicyEffectT38Off, "both", t38Status, failures); err != nil {
		s.logPolicyError("failed to upsert auto t38_off rule for %s: %v", remoteNumber, err)
		return
	}

	if failures >= cfg.ECMThreshold() {
		if _, err := s.upsertAutoRule(remoteNumber, PolicyEffectECMOff, "both", t38Status, failures); err != nil {
			s.logPolicyError("failed to upsert auto ecm_off rule for %s: %v", remoteNumber, err)
		}
	}
	if failures >= cfg.V17Threshold() {
		if _, err := s.upsertAutoRule(remoteNumber, PolicyEffectV17Off, "both", t38Status, failures); err != nil {
			s.logPolicyError("failed to upsert auto v17_off rule for %s: %v", remoteNumber, err)
		}
	}

	s.LogManager.SendLog(s.LogManager.BuildLog(
		"FaxPolicy",
		"auto-learned policy escalation for %s (failures=%d, t38_status=%s)",
		logrus.WarnLevel,
		map[string]interface{}{
			"remote_number": remoteNumber,
			"failures":      failures,
			"t38_status":    t38Status,
		},
		remoteNumber, failures, t38Status,
	))

	if err := s.reloadFaxPolicies(); err != nil {
		s.logPolicyError("failed to reload fax policies after learning: %v", err)
	}
}

// LearnFaxPolicyBridgeFailure creates a bridge-scoped auto t38_off rule when
// a transcoded call failed with a SIP negotiation error while T.38 was
// enabled. Bridge calls have no fax telemetry, so only call-level failure
// signatures are used, and the rule never affects the softmodem path.
func (s *Server) LearnFaxPolicyBridgeFailure(remoteNumber, hangupCause string) {
	cfg := policyCfg()
	if !cfg.PolicyEnabled() || !cfg.LearningEnabled() || !cfg.BridgeLearnEnabled() || remoteNumber == "" {
		return
	}

	rule := s.findAutoRule(remoteNumber, PolicyEffectT38Off, CallTypeBridge)
	failures := 1
	if rule != nil {
		failures = rule.FailureCount + 1
	}
	if _, err := s.upsertAutoRule(remoteNumber, PolicyEffectT38Off, CallTypeBridge, hangupCause, failures); err != nil {
		s.logPolicyError("failed to upsert bridge auto t38_off rule for %s: %v", remoteNumber, err)
		return
	}

	s.LogManager.SendLog(s.LogManager.BuildLog(
		"FaxPolicy",
		"auto-learned bridge t38_off for %s after SIP negotiation failure %s",
		logrus.WarnLevel,
		map[string]interface{}{
			"remote_number": remoteNumber,
			"hangup_cause":  hangupCause,
			"failures":      failures,
		},
		remoteNumber, hangupCause,
	))

	if err := s.reloadFaxPolicies(); err != nil {
		s.logPolicyError("failed to reload fax policies after bridge learning: %v", err)
	}
}

// LearnFaxPolicySuccess records a successful fax to/from a remote number and
// heals auto rules: after RecoveryThreshold() consecutive successes all auto
// rules for the number are cleared so T.38 is re-probed.
func (s *Server) LearnFaxPolicySuccess(remoteNumber string) {
	cfg := policyCfg()
	if !cfg.PolicyEnabled() || !cfg.LearningEnabled() || remoteNumber == "" {
		return
	}

	var rules []FaxPolicyRule
	if err := s.DB.Where(
		"origin = ? AND scope = ? AND dst_number = ?",
		PolicyOriginAuto, PolicyScopeDst, remoteNumber,
	).Find(&rules).Error; err != nil || len(rules) == 0 {
		return
	}

	now := time.Now()
	changed := false
	for _, r := range rules {
		r.SuccessCount++
		r.FailureCount = 0
		r.LastSeenAt = &now
		if r.SuccessCount >= cfg.RecoveryThreshold() {
			// Healed: clear all auto rules for this number (re-probe).
			if err := s.DB.Where(
				"origin = ? AND scope = ? AND dst_number = ?",
				PolicyOriginAuto, PolicyScopeDst, remoteNumber,
			).Delete(&FaxPolicyRule{}).Error; err != nil {
				s.logPolicyError("failed to clear healed auto rules for %s: %v", remoteNumber, err)
			} else {
				s.LogManager.SendLog(s.LogManager.BuildLog(
					"FaxPolicy",
					"auto-learned rules for %s cleared after %d consecutive successes (re-probing)",
					logrus.InfoLevel,
					map[string]interface{}{"remote_number": remoteNumber, "successes": r.SuccessCount},
					remoteNumber, r.SuccessCount,
				))
			}
			changed = true
			break
		}
		if err := s.DB.Save(&r).Error; err != nil {
			s.logPolicyError("failed to update auto rule success count for %s: %v", remoteNumber, err)
		}
		changed = true
	}

	if changed {
		if err := s.reloadFaxPolicies(); err != nil {
			s.logPolicyError("failed to reload fax policies after success: %v", err)
		}
	}
}

// escalateRetryChain inspects the previous attempt's result of the same job
// and forces T.38 off for subsequent attempts when the failure signature
// points at T.38 negotiation trouble. ECM/V.17 retry escalation is handled
// separately in SendFax via disable_ecm_after_retry/disable_v17_after_retry.
func escalateRetryChain(ff *FaxJob, attempt int) {
	if attempt <= 1 || !policyCfg().RetryChainEnabled() || ff.ForceT38Off {
		return
	}
	prev := ff.Result
	if prev == nil || prev.Success {
		return
	}
	if prev.NegotiateCount > 1 || T38Refused(prev) {
		ff.ForceT38Off = true
	}
}

func (s *Server) logPolicyError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	s.LogManager.SendLog(s.LogManager.BuildLog(
		"FaxPolicy", msg, logrus.ErrorLevel, nil,
	))
}

// --------------------- persisted flip-flop pair state ---------------------

func pairStateKey(srcNum, dstNum, callType string) string {
	return callType + "|" + srcNum + "|" + dstNum
}

// loadPairStates warms the pair-state cache from Postgres at startup.
func (s *Server) loadPairStates() {
	var rows []FaxPairState
	if err := s.DB.Find(&rows).Error; err != nil {
		s.logPolicyError("failed to load pair states: %v", err)
		return
	}
	s.pairStateMu.Lock()
	defer s.pairStateMu.Unlock()
	for i := range rows {
		r := rows[i]
		s.pairStates[pairStateKey(r.SrcNumber, r.DstNumber, r.CallType)] = &r
	}
}

// ShouldAllowT38ForPair implements flip-flop probing: alternate T.38 on/off
// for consecutive calls of the same src→dst pair and call type while no
// policy rule decides T.38. Defaults to allowing T.38 with no recent state.
func (s *Server) ShouldAllowT38ForPair(srcNum, dstNum, callType string, now time.Time) bool {
	key := pairStateKey(srcNum, dstNum, callType)

	s.pairStateMu.Lock()
	st, ok := s.pairStates[key]
	s.pairStateMu.Unlock()

	if !ok || now.Sub(st.LastSeen) > PairStateTTL() {
		return true
	}
	return !st.LastUsedT38
}

// UpdateT38PairState records what was actually used for a call, persisting
// the pair state to Postgres (write-through cache).
func (s *Server) UpdateT38PairState(srcNum, dstNum, callType string, usedT38 bool, now time.Time) {
	key := pairStateKey(srcNum, dstNum, callType)

	s.pairStateMu.Lock()
	st, ok := s.pairStates[key]
	if !ok {
		st = &FaxPairState{SrcNumber: srcNum, DstNumber: dstNum, CallType: callType}
		s.pairStates[key] = st
	}
	st.LastUsedT38 = usedT38
	st.LastSeen = now
	row := *st
	s.pairStateMu.Unlock()

	if row.ID == 0 {
		if err := s.DB.Create(&row).Error; err != nil {
			// Row may exist outside the cache (e.g. written by another
			// process); fall back to updating by the unique key.
			if err2 := s.DB.Model(&FaxPairState{}).
				Where("src_number = ? AND dst_number = ? AND call_type = ?", srcNum, dstNum, callType).
				Updates(map[string]interface{}{"last_used_t38": row.LastUsedT38, "last_seen": row.LastSeen}).Error; err2 != nil {
				s.logPolicyError("failed to persist pair state %s: %v / %v", key, err, err2)
			}
			return
		}
		s.pairStateMu.Lock()
		st.ID = row.ID
		s.pairStateMu.Unlock()
	} else {
		if err := s.DB.Model(&FaxPairState{}).Where("id = ?", row.ID).
			Updates(map[string]interface{}{"last_used_t38": row.LastUsedT38, "last_seen": row.LastSeen}).Error; err != nil {
			s.logPolicyError("failed to update pair state %s: %v", key, err)
		}
	}
}
