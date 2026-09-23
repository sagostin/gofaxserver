// This file is part of gofaxserver - https://github.com/sagostin/gofaxserver
// Copyright (C) 2025-2026 Shaun Agostinho
//
// This program is free software; you can redistribute it and/or
// modify it under the terms of the GNU General Public License
// as published by the Free Software Foundation; version 2
// of the License.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program; if not, write to the Free Software
// Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA  02110-1301, USA.

package gofaxserver

import (
	"testing"
	"time"

	"gofaxserver/gofaxlib"
)

func withFaxDefaults(t *testing.T) {
	t.Helper()
	oldEnable := gofaxlib.Config.Faxing.EnableT38
	oldRequest := gofaxlib.Config.Faxing.RequestT38
	oldPolicy := gofaxlib.Config.Faxing.Policy
	gofaxlib.Config.Faxing.EnableT38 = true
	gofaxlib.Config.Faxing.RequestT38 = true
	t.Cleanup(func() {
		gofaxlib.Config.Faxing.EnableT38 = oldEnable
		gofaxlib.Config.Faxing.RequestT38 = oldRequest
		gofaxlib.Config.Faxing.Policy = oldPolicy
	})
}

func serverWithRules(rules ...FaxPolicyRule) *Server {
	s := &Server{}
	s.faxPolicies.Store(&rules)
	return s
}

func TestResolveFaxPolicyDefaultsWithoutRules(t *testing.T) {
	withFaxDefaults(t)
	s := serverWithRules()

	p := s.ResolveFaxPolicy("111", "222", CallTypeSoftmodem)
	if !p.EnableT38 || !p.RequestT38 {
		t.Fatalf("expected config defaults (t38 on), got %+v", p)
	}
	if p.T38Decided || p.T38ForcedOff {
		t.Fatalf("no rule should decide T.38, got %+v", p)
	}
	if p.UseECM != nil || p.DisableV17 != nil {
		t.Fatalf("ECM/V17 should be unset (inherit), got %+v", p)
	}
}

func TestResolveFaxPolicyManualDstT38Off(t *testing.T) {
	withFaxDefaults(t)
	s := serverWithRules(FaxPolicyRule{
		ID: 1, Scope: PolicyScopeDst, DstNumber: "222",
		Effect: PolicyEffectT38Off, AppliesTo: "both",
		Origin: PolicyOriginManual, Enabled: true,
	})

	p := s.ResolveFaxPolicy("111", "222", CallTypeSoftmodem)
	if p.EnableT38 || p.RequestT38 {
		t.Fatalf("expected t38 off, got %+v", p)
	}
	if !p.T38Decided || !p.T38ForcedOff {
		t.Fatalf("expected decided+forced off, got %+v", p)
	}
	if len(p.AppliedRuleIDs) != 1 || p.AppliedRuleIDs[0] != 1 {
		t.Fatalf("expected rule 1 applied, got %v", p.AppliedRuleIDs)
	}
}

func TestResolveFaxPolicyPairBeatsDst(t *testing.T) {
	withFaxDefaults(t)
	s := serverWithRules(
		FaxPolicyRule{ID: 1, Scope: PolicyScopeDst, DstNumber: "222", Effect: PolicyEffectT38Off, AppliesTo: "both", Origin: PolicyOriginManual, Enabled: true},
		FaxPolicyRule{ID: 2, Scope: PolicyScopePair, SrcNumber: "111", DstNumber: "222", Effect: PolicyEffectT38On, AppliesTo: "both", Origin: PolicyOriginManual, Enabled: true},
	)

	// The pair rule is more specific: T.38 stays on for this sender only.
	p := s.ResolveFaxPolicy("111", "222", CallTypeSoftmodem)
	if !p.EnableT38 {
		t.Fatalf("pair t38_on should beat dst t38_off, got %+v", p)
	}
	// A different sender still gets the dst rule.
	p2 := s.ResolveFaxPolicy("999", "222", CallTypeSoftmodem)
	if p2.EnableT38 {
		t.Fatalf("other senders should still hit dst t38_off, got %+v", p2)
	}
}

func TestResolveFaxPolicyManualBeatsAuto(t *testing.T) {
	withFaxDefaults(t)
	s := serverWithRules(
		FaxPolicyRule{ID: 1, Scope: PolicyScopeDst, DstNumber: "222", Effect: PolicyEffectT38Off, AppliesTo: "both", Origin: PolicyOriginAuto, Enabled: true},
		FaxPolicyRule{ID: 2, Scope: PolicyScopeDst, DstNumber: "222", Effect: PolicyEffectT38On, AppliesTo: "both", Origin: PolicyOriginManual, Enabled: true},
	)

	p := s.ResolveFaxPolicy("111", "222", CallTypeSoftmodem)
	if !p.EnableT38 {
		t.Fatalf("manual t38_on should beat auto t38_off, got %+v", p)
	}
}

func TestResolveFaxPolicyOffBeatsOnFailSafe(t *testing.T) {
	withFaxDefaults(t)
	s := serverWithRules(
		FaxPolicyRule{ID: 1, Scope: PolicyScopeDst, DstNumber: "222", Effect: PolicyEffectT38On, AppliesTo: "both", Origin: PolicyOriginManual, Enabled: true},
		FaxPolicyRule{ID: 2, Scope: PolicyScopeDst, DstNumber: "222", Effect: PolicyEffectSoftmodemOnly, AppliesTo: "both", Origin: PolicyOriginManual, Enabled: true},
	)

	p := s.ResolveFaxPolicy("111", "222", CallTypeSoftmodem)
	if p.EnableT38 || !p.SoftmodemOnly {
		t.Fatalf("softmodem_only should beat t38_on at equal specificity, got %+v", p)
	}
}

func TestResolveFaxPolicyAppliesToCallType(t *testing.T) {
	withFaxDefaults(t)
	s := serverWithRules(FaxPolicyRule{
		ID: 1, Scope: PolicyScopeDst, DstNumber: "222",
		Effect: PolicyEffectT38Off, AppliesTo: CallTypeSoftmodem,
		Origin: PolicyOriginManual, Enabled: true,
	})

	// Softmodem call: rule applies.
	if p := s.ResolveFaxPolicy("111", "222", CallTypeSoftmodem); p.EnableT38 {
		t.Fatalf("softmodem-scoped rule should disable t38 for softmodem, got %+v", p)
	}
	// Bridged call to the same number: untouched (the edge case).
	p := s.ResolveFaxPolicy("111", "222", CallTypeBridge)
	if !p.EnableT38 || p.T38Decided {
		t.Fatalf("bridge call should be unaffected by softmodem-scoped rule, got %+v", p)
	}
}

func TestResolveFaxPolicyAutoDstMatchesSrcSide(t *testing.T) {
	withFaxDefaults(t)
	s := serverWithRules(FaxPolicyRule{
		ID: 1, Scope: PolicyScopeDst, DstNumber: "555",
		Effect: PolicyEffectT38Off, AppliesTo: "both",
		Origin: PolicyOriginAuto, Enabled: true,
	})

	// Inbound call *from* the learned number: auto rule still protects.
	p := s.ResolveFaxPolicy("555", "222", CallTypeSoftmodem)
	if p.EnableT38 {
		t.Fatalf("auto dst rule should match the number on the src side, got %+v", p)
	}
}

func TestResolveFaxPolicyManualDstDoesNotMatchSrcSide(t *testing.T) {
	withFaxDefaults(t)
	s := serverWithRules(FaxPolicyRule{
		ID: 1, Scope: PolicyScopeDst, DstNumber: "555",
		Effect: PolicyEffectT38Off, AppliesTo: "both",
		Origin: PolicyOriginManual, Enabled: true,
	})

	p := s.ResolveFaxPolicy("555", "222", CallTypeSoftmodem)
	if !p.EnableT38 {
		t.Fatalf("manual dst rule must not match on the src side, got %+v", p)
	}
}

func TestResolveFaxPolicyExpiredRuleSkipped(t *testing.T) {
	withFaxDefaults(t)
	past := time.Now().Add(-time.Hour)
	s := serverWithRules(FaxPolicyRule{
		ID: 1, Scope: PolicyScopeDst, DstNumber: "222",
		Effect: PolicyEffectT38Off, AppliesTo: "both",
		Origin: PolicyOriginManual, Enabled: true, ExpiresAt: &past,
	})

	p := s.ResolveFaxPolicy("111", "222", CallTypeSoftmodem)
	if !p.EnableT38 || p.T38Decided {
		t.Fatalf("expired rule must be ignored, got %+v", p)
	}
}

func TestResolveFaxPolicyECMAndV17(t *testing.T) {
	withFaxDefaults(t)
	s := serverWithRules(
		FaxPolicyRule{ID: 1, Scope: PolicyScopeDst, DstNumber: "222", Effect: PolicyEffectECMOff, AppliesTo: "both", Origin: PolicyOriginAuto, Enabled: true},
		FaxPolicyRule{ID: 2, Scope: PolicyScopeDst, DstNumber: "222", Effect: PolicyEffectV17Off, AppliesTo: "both", Origin: PolicyOriginAuto, Enabled: true},
	)

	p := s.ResolveFaxPolicy("111", "222", CallTypeSoftmodem)
	if p.UseECM == nil || *p.UseECM {
		t.Fatalf("expected ECM off, got %+v", p)
	}
	if p.DisableV17 == nil || !*p.DisableV17 {
		t.Fatalf("expected V17 off, got %+v", p)
	}
	// T.38 unaffected: no t38 rule matched, flip-flop may proceed.
	if p.T38Decided {
		t.Fatalf("T.38 should remain undecided, got %+v", p)
	}
}

func TestResolveFaxPolicyDisabled(t *testing.T) {
	withFaxDefaults(t)
	disabled := false
	gofaxlib.Config.Faxing.Policy.Enabled = &disabled

	s := serverWithRules(FaxPolicyRule{
		ID: 1, Scope: PolicyScopeDst, DstNumber: "222",
		Effect: PolicyEffectT38Off, AppliesTo: "both",
		Origin: PolicyOriginManual, Enabled: true,
	})

	p := s.ResolveFaxPolicy("111", "222", CallTypeSoftmodem)
	if !p.EnableT38 || p.T38Decided || len(p.AppliedRuleIDs) != 0 {
		t.Fatalf("disabled engine must return defaults, got %+v", p)
	}
}

func TestEscalateRetryChain(t *testing.T) {
	withFaxDefaults(t)

	// First attempt: never escalates.
	j := &FaxJob{}
	escalateRetryChain(j, 1)
	if j.ForceT38Off {
		t.Fatal("attempt 1 must not escalate")
	}

	// Previous attempt failed with repeated negotiations.
	j.Result = &gofaxlib.FaxResult{Success: false, NegotiateCount: 2}
	escalateRetryChain(j, 2)
	if !j.ForceT38Off {
		t.Fatal("attempt 2 after negotiation failure must force T.38 off")
	}

	// T.38 refusal signature also escalates.
	j2 := &FaxJob{Result: &gofaxlib.FaxResult{Success: false, T38Status: "rejected"}}
	escalateRetryChain(j2, 2)
	if !j2.ForceT38Off {
		t.Fatal("t38 refusal must force T.38 off on next attempt")
	}

	// Clean failure without a fax-level signature does not escalate.
	j3 := &FaxJob{Result: &gofaxlib.FaxResult{Success: false, HangupCause: "USER_BUSY"}}
	escalateRetryChain(j3, 2)
	if j3.ForceT38Off {
		t.Fatal("busy failure must not escalate")
	}
}

func TestQualifiesForLearning(t *testing.T) {
	if qualifiesForLearning(nil) {
		t.Fatal("nil result must not qualify")
	}
	if qualifiesForLearning(&gofaxlib.FaxResult{Success: true}) {
		t.Fatal("success must not qualify")
	}
	if qualifiesForLearning(&gofaxlib.FaxResult{Success: false, HangupCause: "USER_BUSY"}) {
		t.Fatal("busy must not qualify")
	}
	if !qualifiesForLearning(&gofaxlib.FaxResult{Success: false, NegotiateCount: 2}) {
		t.Fatal("repeated negotiations must qualify")
	}
	if !qualifiesForLearning(&gofaxlib.FaxResult{Success: false, T38Status: "rejected"}) {
		t.Fatal("t38 refusal must qualify")
	}
	bad := &gofaxlib.FaxResult{Success: false}
	bad.PageResults = append(bad.PageResults, gofaxlib.PageResult{BadRows: 3})
	if !qualifiesForLearning(bad) {
		t.Fatal("bad rows must qualify")
	}
}

func TestIsT38NegotiationHangup(t *testing.T) {
	for _, hc := range []string{"INCOMPATIBLE_DESTINATION", "NOT_ACCEPTABLE_HERE", "MEDIA_NEGOTIATION_FAILED"} {
		if !isT38NegotiationHangup(hc) {
			t.Fatalf("%s must be treated as negotiation failure", hc)
		}
	}
	for _, hc := range []string{"NORMAL_CLEARING", "USER_BUSY", "NO_ANSWER", ""} {
		if isT38NegotiationHangup(hc) {
			t.Fatalf("%s must not be treated as negotiation failure", hc)
		}
	}
}

func TestResolveFaxPolicyVarOverrideDst(t *testing.T) {
	withFaxDefaults(t)
	s := serverWithRules(FaxPolicyRule{
		ID: 1, Scope: PolicyScopeDst, DstNumber: "222",
		Effect: PolicyEffectVarOverride, AppliesTo: "both",
		Origin: PolicyOriginManual, Enabled: true,
		VarName: "ignore_early_media", VarValue: "false",
	})

	p := s.ResolveFaxPolicy("111", "222", CallTypeSoftmodem)
	if p.VarOverrides["ignore_early_media"] != "false" {
		t.Fatalf("expected var override, got %+v", p.VarOverrides)
	}
	if len(p.AppliedRuleIDs) != 1 || p.AppliedRuleIDs[0] != 1 {
		t.Fatalf("expected rule 1 applied, got %v", p.AppliedRuleIDs)
	}
}

func TestResolveFaxPolicyVarOverridePairBeatsDstAndMerges(t *testing.T) {
	withFaxDefaults(t)
	s := serverWithRules(
		FaxPolicyRule{ID: 1, Scope: PolicyScopeDst, DstNumber: "222", Effect: PolicyEffectVarOverride, AppliesTo: "both", Origin: PolicyOriginManual, Enabled: true, VarName: "fax_verbose", VarValue: "false"},
		FaxPolicyRule{ID: 2, Scope: PolicyScopeDst, DstNumber: "222", Effect: PolicyEffectVarOverride, AppliesTo: "both", Origin: PolicyOriginManual, Enabled: true, VarName: "sip_h_X-Custom", VarValue: "dst"},
		FaxPolicyRule{ID: 3, Scope: PolicyScopePair, SrcNumber: "111", DstNumber: "222", Effect: PolicyEffectVarOverride, AppliesTo: "both", Origin: PolicyOriginManual, Enabled: true, VarName: "sip_h_X-Custom", VarValue: "pair"},
	)

	p := s.ResolveFaxPolicy("111", "222", CallTypeSoftmodem)
	if p.VarOverrides["fax_verbose"] != "false" {
		t.Fatalf("dst var should merge in, got %+v", p.VarOverrides)
	}
	if p.VarOverrides["sip_h_X-Custom"] != "pair" {
		t.Fatalf("pair rule should win per var name, got %+v", p.VarOverrides)
	}

	// A different sender gets the dst value for the same var.
	p2 := s.ResolveFaxPolicy("999", "222", CallTypeSoftmodem)
	if p2.VarOverrides["sip_h_X-Custom"] != "dst" {
		t.Fatalf("other senders should get the dst rule, got %+v", p2.VarOverrides)
	}
}

func TestResolveFaxPolicyVarOverrideAppliesTo(t *testing.T) {
	withFaxDefaults(t)
	s := serverWithRules(FaxPolicyRule{
		ID: 1, Scope: PolicyScopeDst, DstNumber: "222",
		Effect: PolicyEffectVarOverride, AppliesTo: CallTypeSoftmodem,
		Origin: PolicyOriginManual, Enabled: true,
		VarName: "fax_verbose", VarValue: "true",
	})

	p := s.ResolveFaxPolicy("111", "222", CallTypeSoftmodem)
	if p.VarOverrides["fax_verbose"] != "true" {
		t.Fatalf("softmodem call should get the override, got %+v", p.VarOverrides)
	}
	p2 := s.ResolveFaxPolicy("111", "222", CallTypeBridge)
	if len(p2.VarOverrides) != 0 {
		t.Fatalf("bridge call must not get the softmodem-scoped override, got %+v", p2.VarOverrides)
	}
}

func TestValidateFaxPolicyRuleVarOverride(t *testing.T) {
	// Valid var_override rule.
	r := FaxPolicyRule{Scope: PolicyScopeDst, DstNumber: "222", Effect: PolicyEffectVarOverride, VarName: "fax_verbose", VarValue: "true"}
	if err := validateFaxPolicyRule(&r); err != nil {
		t.Fatalf("valid var_override rejected: %v", err)
	}

	// Missing / invalid var name.
	r = FaxPolicyRule{Scope: PolicyScopeDst, DstNumber: "222", Effect: PolicyEffectVarOverride, VarValue: "true"}
	if err := validateFaxPolicyRule(&r); err == nil {
		t.Fatal("missing var_name should be rejected")
	}
	r = FaxPolicyRule{Scope: PolicyScopeDst, DstNumber: "222", Effect: PolicyEffectVarOverride, VarName: "bad name!", VarValue: "true"}
	if err := validateFaxPolicyRule(&r); err == nil {
		t.Fatal("invalid var_name charset should be rejected")
	}

	// Empty value.
	r = FaxPolicyRule{Scope: PolicyScopeDst, DstNumber: "222", Effect: PolicyEffectVarOverride, VarName: "fax_verbose"}
	if err := validateFaxPolicyRule(&r); err == nil {
		t.Fatal("empty var_value should be rejected")
	}

	// Denylisted variable.
	r = FaxPolicyRule{Scope: PolicyScopeDst, DstNumber: "222", Effect: PolicyEffectVarOverride, VarName: "origination_uuid", VarValue: "x"}
	if err := validateFaxPolicyRule(&r); err == nil {
		t.Fatal("origination_uuid override should be rejected")
	}

	// Var fields are cleared on other effects.
	r = FaxPolicyRule{Scope: PolicyScopeDst, DstNumber: "222", Effect: PolicyEffectT38Off, VarName: "fax_verbose", VarValue: "true"}
	if err := validateFaxPolicyRule(&r); err != nil {
		t.Fatalf("t38_off rejected: %v", err)
	}
	if r.VarName != "" || r.VarValue != "" {
		t.Fatalf("var fields must be cleared for t38_off, got %q=%q", r.VarName, r.VarValue)
	}
}
