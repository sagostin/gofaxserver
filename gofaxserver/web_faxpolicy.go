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
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kataras/iris/v12"
)

// Admin handlers for Postgres-backed fax policy rules (T.38 / ECM / V.17).
// These replace the old FreeSWITCH mod_db softmodem fallback. Mutations take
// effect immediately via reloadFaxPolicies.

var varNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// deniedVarOverrides are channel variables that must never be overridden via
// var_override rules (would break fax job tracking/result correlation).
var deniedVarOverrides = map[string]bool{
	"origination_uuid": true,
}

func validateFaxPolicyRule(r *FaxPolicyRule) error {
	switch r.Scope {
	case PolicyScopeDst:
		if strings.TrimSpace(r.DstNumber) == "" {
			return fmt.Errorf("dst_number is required for scope=dst")
		}
		r.SrcNumber = ""
	case PolicyScopeSrc:
		if strings.TrimSpace(r.SrcNumber) == "" {
			return fmt.Errorf("src_number is required for scope=src")
		}
		r.DstNumber = ""
	case PolicyScopePair:
		if strings.TrimSpace(r.SrcNumber) == "" || strings.TrimSpace(r.DstNumber) == "" {
			return fmt.Errorf("src_number and dst_number are required for scope=pair")
		}
	default:
		return fmt.Errorf("invalid scope %q (dst|src|pair)", r.Scope)
	}

	switch r.Effect {
	case PolicyEffectT38Off, PolicyEffectT38On, PolicyEffectECMOff,
		PolicyEffectECMOn, PolicyEffectV17Off, PolicyEffectSoftmodemOnly:
		r.VarName = ""
		r.VarValue = ""
	case PolicyEffectVarOverride:
		r.VarName = strings.TrimSpace(r.VarName)
		if !varNamePattern.MatchString(r.VarName) {
			return fmt.Errorf("var_name is required for effect=var_override and must match [A-Za-z0-9_]+")
		}
		if deniedVarOverrides[strings.ToLower(r.VarName)] {
			return fmt.Errorf("var_name %q may not be overridden (would break fax job tracking)", r.VarName)
		}
		if strings.TrimSpace(r.VarValue) == "" {
			return fmt.Errorf("var_value is required for effect=var_override")
		}
	default:
		return fmt.Errorf("invalid effect %q (t38_off|t38_on|ecm_off|ecm_on|v17_off|softmodem_only|var_override)", r.Effect)
	}

	switch r.AppliesTo {
	case "":
		r.AppliesTo = "both"
	case "both", CallTypeSoftmodem, CallTypeBridge:
	default:
		return fmt.Errorf("invalid applies_to %q (both|softmodem|bridge)", r.AppliesTo)
	}

	return nil
}

// handleListFaxPolicies lists rules, optionally filtered by number, scope,
// effect, origin, or applies_to.
func (s *Server) handleListFaxPolicies(ctx iris.Context) {
	q := s.DB.Model(&FaxPolicyRule{}).Order("id ASC")
	if number := strings.TrimSpace(ctx.URLParam("number")); number != "" {
		q = q.Where("src_number = ? OR dst_number = ?", number, number)
	}
	if scope := strings.TrimSpace(ctx.URLParam("scope")); scope != "" {
		q = q.Where("scope = ?", scope)
	}
	if effect := strings.TrimSpace(ctx.URLParam("effect")); effect != "" {
		q = q.Where("effect = ?", effect)
	}
	if origin := strings.TrimSpace(ctx.URLParam("origin")); origin != "" {
		q = q.Where("origin = ?", origin)
	}
	if appliesTo := strings.TrimSpace(ctx.URLParam("applies_to")); appliesTo != "" {
		q = q.Where("applies_to = ?", appliesTo)
	}

	var rules []FaxPolicyRule
	if err := q.Find(&rules).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to list fax policies: " + err.Error()})
		return
	}
	ctx.JSON(iris.Map{"rules": rules})
}

// handleCreateFaxPolicyRule creates a manual rule.
func (s *Server) handleCreateFaxPolicyRule(ctx iris.Context) {
	var rule FaxPolicyRule
	if err := ctx.ReadJSON(&rule); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid payload: " + err.Error()})
		return
	}
	rule.ID = 0
	rule.Origin = PolicyOriginManual
	if err := validateFaxPolicyRule(&rule); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}
	if err := s.DB.Create(&rule).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to create rule: " + err.Error()})
		return
	}
	if err := s.reloadFaxPolicies(); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "rule saved but reload failed: " + err.Error()})
		return
	}
	ctx.StatusCode(http.StatusCreated)
	ctx.JSON(rule)
}

// handleUpdateFaxPolicyRule updates an existing rule. Auto rules may be
// edited (e.g. notes, expiry, enabled) but keep their origin.
func (s *Server) handleUpdateFaxPolicyRule(ctx iris.Context) {
	id, err := strconv.Atoi(ctx.Params().Get("id"))
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid rule id"})
		return
	}

	var existing FaxPolicyRule
	if err := s.DB.First(&existing, id).Error; err != nil {
		ctx.StatusCode(http.StatusNotFound)
		ctx.JSON(iris.Map{"error": "rule not found"})
		return
	}

	var rule FaxPolicyRule
	if err := ctx.ReadJSON(&rule); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid payload: " + err.Error()})
		return
	}
	rule.ID = uint(id)
	rule.Origin = existing.Origin // origin is immutable
	if err := validateFaxPolicyRule(&rule); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}
	rule.CreatedAt = existing.CreatedAt
	if err := s.DB.Save(&rule).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to update rule: " + err.Error()})
		return
	}
	if err := s.reloadFaxPolicies(); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "rule saved but reload failed: " + err.Error()})
		return
	}
	ctx.JSON(rule)
}

// handleDeleteFaxPolicyRule deletes a rule by ID.
func (s *Server) handleDeleteFaxPolicyRule(ctx iris.Context) {
	id, err := strconv.Atoi(ctx.Params().Get("id"))
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid rule id"})
		return
	}
	if err := s.DB.Delete(&FaxPolicyRule{}, id).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to delete rule: " + err.Error()})
		return
	}
	if err := s.reloadFaxPolicies(); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "rule deleted but reload failed: " + err.Error()})
		return
	}
	ctx.JSON(iris.Map{"ok": true})
}

// handleExpireFaxPolicyRule forces a rule to expire immediately (reset to
// probing) by setting its expiry to now.
func (s *Server) handleExpireFaxPolicyRule(ctx iris.Context) {
	id, err := strconv.Atoi(ctx.Params().Get("id"))
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid rule id"})
		return
	}
	now := time.Now()
	if err := s.DB.Model(&FaxPolicyRule{}).Where("id = ?", id).Update("expires_at", &now).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to expire rule: " + err.Error()})
		return
	}
	if err := s.reloadFaxPolicies(); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "rule expired but reload failed: " + err.Error()})
		return
	}
	ctx.JSON(iris.Map{"ok": true})
}

// handleResolveFaxPolicy is a dry-run endpoint: given src, dst and call type
// it returns the effective policy and the applied rule set, without any call.
func (s *Server) handleResolveFaxPolicy(ctx iris.Context) {
	srcNum := strings.TrimSpace(ctx.URLParam("src"))
	dstNum := strings.TrimSpace(ctx.URLParam("dst"))
	callType := strings.TrimSpace(ctx.URLParam("type"))
	if callType == "" {
		callType = CallTypeSoftmodem
	}
	if callType != CallTypeSoftmodem && callType != CallTypeBridge {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "type must be softmodem or bridge"})
		return
	}
	if dstNum == "" && srcNum == "" {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "src and/or dst query parameters are required"})
		return
	}

	policy := s.ResolveFaxPolicy(srcNum, dstNum, callType)

	var rules []FaxPolicyRule
	if len(policy.AppliedRuleIDs) > 0 {
		if err := s.DB.Where("id IN ?", policy.AppliedRuleIDs).Find(&rules).Error; err != nil {
			ctx.StatusCode(http.StatusInternalServerError)
			ctx.JSON(iris.Map{"error": "failed to load applied rules: " + err.Error()})
			return
		}
	}

	ctx.JSON(iris.Map{
		"policy":        policy,
		"applied_rules": rules,
	})
}

// handleListFaxPairStates exposes the persisted flip-flop pair state.
func (s *Server) handleListFaxPairStates(ctx iris.Context) {
	q := s.DB.Model(&FaxPairState{}).Order("last_seen DESC")
	if number := strings.TrimSpace(ctx.URLParam("number")); number != "" {
		q = q.Where("src_number = ? OR dst_number = ?", number, number)
	}
	var states []FaxPairState
	if err := q.Limit(500).Find(&states).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to list pair states: " + err.Error()})
		return
	}
	ctx.JSON(iris.Map{"pair_states": states})
}

// handleDeleteFaxPairState clears a pair state (reset to first-call probing).
func (s *Server) handleDeleteFaxPairState(ctx iris.Context) {
	id, err := strconv.Atoi(ctx.Params().Get("id"))
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid pair state id"})
		return
	}
	var row FaxPairState
	if err := s.DB.First(&row, id).Error; err != nil {
		ctx.StatusCode(http.StatusNotFound)
		ctx.JSON(iris.Map{"error": "pair state not found"})
		return
	}
	if err := s.DB.Delete(&FaxPairState{}, id).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to delete pair state: " + err.Error()})
		return
	}
	s.pairStateMu.Lock()
	delete(s.pairStates, pairStateKey(row.SrcNumber, row.DstNumber, row.CallType))
	s.pairStateMu.Unlock()
	ctx.JSON(iris.Map{"ok": true})
}
