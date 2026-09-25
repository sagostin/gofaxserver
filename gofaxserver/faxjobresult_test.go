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

	"gofaxserver/gofaxlib"
)

func TestClassifyFaxResultPlaceholderIsSubmission(t *testing.T) {
	job := &FaxJob{
		Result: &gofaxlib.FaxResult{HangupCause: placeholderHangupCause, ResultText: "queued"},
	}
	rt, epID, epType, attempt, gw := classifyFaxResult(job)
	if rt != "submission" {
		t.Errorf("placeholder should classify as submission, got %q", rt)
	}
	if attempt != 0 {
		t.Errorf("submission leg should have attempt_number 0, got %d", attempt)
	}
	if epID != 0 || epType != "" || gw != "" {
		t.Errorf("submission leg should carry no endpoint/gateway, got id=%d type=%q gw=%q", epID, epType, gw)
	}
}

func TestClassifyFaxResultReceptionArrivalGateway(t *testing.T) {
	job := &FaxJob{
		Result:     &gofaxlib.FaxResult{HangupCause: "NORMAL_CLEARING", Success: true},
		SourceInfo: FaxSourceInfo{SourceType: "gateway", Source: "carrier_sbc"},
	}
	rt, _, _, attempt, gw := classifyFaxResult(job)
	if rt != "reception" {
		t.Errorf("no endpoints should classify as reception, got %q", rt)
	}
	if attempt != 1 {
		t.Errorf("reception attempt number should be 1, got %d", attempt)
	}
	if gw != "carrier_sbc" {
		t.Errorf("reception should record the arrival gateway, got %q", gw)
	}
}

func TestClassifyFaxResultBridgeArrivalGateway(t *testing.T) {
	job := &FaxJob{
		IsBridge:   true,
		Result:     &gofaxlib.FaxResult{HangupCause: "NORMAL_CLEARING"},
		SourceInfo: FaxSourceInfo{SourceType: "gateway", Source: "upstream1"},
	}
	rt, _, _, _, gw := classifyFaxResult(job)
	if rt != "bridge" {
		t.Errorf("bridge job should classify as bridge, got %q", rt)
	}
	if gw != "upstream1" {
		t.Errorf("bridge should record the arrival gateway, got %q", gw)
	}
}

func TestClassifyFaxResultTransmissionCapturedGateway(t *testing.T) {
	job := &FaxJob{
		Result:  &gofaxlib.FaxResult{HangupCause: "NORMAL_CLEARING", Success: true},
		Gateway: "carrier_b", // captured from variable_gofax_gw on the winning leg
		Endpoints: []*Endpoint{
			{ID: 5, EndpointType: "gateway", Endpoint: "carrier_a:192.0.2.1"},
		},
		TotDials: 2,
	}
	rt, epID, epType, attempt, gw := classifyFaxResult(job)
	if rt != "transmission" || epType != "gateway" || epID != 5 {
		t.Errorf("classification wrong: rt=%q epType=%q epID=%d", rt, epType, epID)
	}
	if attempt != 2 {
		t.Errorf("attempt number should come from TotDials, got %d", attempt)
	}
	if gw != "carrier_b" {
		t.Errorf("captured winning gateway must win over the endpoint name, got %q", gw)
	}
}

func TestClassifyFaxResultTransmissionFallbackGateway(t *testing.T) {
	job := &FaxJob{
		Result: &gofaxlib.FaxResult{HangupCause: "NORMAL_CLEARING", Success: true},
		Endpoints: []*Endpoint{
			{ID: 5, EndpointType: "gateway", Endpoint: "carrier_a:192.0.2.1"},
		},
	}
	_, _, _, _, gw := classifyFaxResult(job)
	if gw != "carrier_a" {
		t.Errorf("without a captured gateway, fall back to the endpoint's gateway name, got %q", gw)
	}
}

func TestClassifyFaxResultDeliveryNoGateway(t *testing.T) {
	job := &FaxJob{
		Result: &gofaxlib.FaxResult{ResultText: "status 201", Success: true},
		Endpoints: []*Endpoint{
			{ID: 9, EndpointType: "portal", Endpoint: "svc_account"},
		},
	}
	rt, _, epType, _, gw := classifyFaxResult(job)
	if rt != "delivery" || epType != "portal" {
		t.Errorf("portal endpoint should classify as delivery, got rt=%q epType=%q", rt, epType)
	}
	if gw != "" {
		t.Errorf("delivery legs involve no FS gateway, got %q", gw)
	}
}
