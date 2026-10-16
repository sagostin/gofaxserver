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

package gofaxlib

import (
	"testing"

	"github.com/fiorix/go-eventsocket/eventsocket"
	"github.com/google/uuid"
)

func newTestFaxResult() *FaxResult {
	// Loki disabled by default in tests: PushLog short-circuits and the
	// drained log channel never blocks SendLog.
	return NewFaxResult(uuid.New(), NewLogManager(&LokiClient{}), false)
}

func hangupEvent(vars map[string]string) *eventsocket.Event {
	h := eventsocket.EventHeader{"Event-Name": "CHANNEL_HANGUP"}
	for k, v := range vars {
		h["Variable_"+k] = v
	}
	return &eventsocket.Event{Header: h}
}

func customEvent(subclass string, headers map[string]string) *eventsocket.Event {
	h := eventsocket.EventHeader{
		"Event-Name":     "CUSTOM",
		"Event-Subclass": subclass,
	}
	for k, v := range headers {
		h[k] = v
	}
	return &eventsocket.Event{Header: h}
}

func TestHarvestResultVarsChannelHangup(t *testing.T) {
	f := newTestFaxResult()
	f.AddEvent(hangupEvent(map[string]string{
		"fax_success":                    "1",
		"fax_result_code":                "0",
		"fax_result_text":                "OK",
		"fax_filename":                   "/tmp/fax_123.tiff",
		"fax_document_transferred_pages": "3",
		"fax_document_total_pages":       "3",
		"fax_image_resolution":           "504x98",
		"fax_file_image_resolution":      "504x196",
		"fax_image_pixel_size":           "1728x1146",
		"fax_file_image_pixel_size":      "1728x2292",
		"fax_image_size":                 "48394",
		"fax_bad_rows":                   "2",
		"fax_longest_bad_row_run":        "5",
		"fax_encoding":                   "4",
		"fax_encoding_name":              "T.6",
		"fax_transfer_rate":              "14400",
		"fax_ecm_used":                   "on",
		"fax_ecm_requested":              "1",
		"fax_t38_status":                 "negotiated",
		"fax_v17_disabled":               "1",
		"fax_local_station_id":           "6045551234",
		"fax_remote_station_id":          "7785559876",
		"fax_remote_country":             "Canada",
		"fax_remote_vendor":              "Panasonic",
		"fax_remote_model":               "KX-FLB801",
		"fax_trace_file":                 "/var/log/faxtrace/123.log",
		// input variables sharing the fax_ prefix must be ignored
		"fax_enable_t38": "true",
		"fax_ident":      "MyFax",
	}))

	if !f.Success {
		t.Error("Success not set from fax_success=1")
	}
	if f.ResultText != "OK" {
		t.Errorf("ResultText = %q, want OK", f.ResultText)
	}
	if f.TransferredPages != 3 || f.TotalPages != 3 {
		t.Errorf("pages = %d/%d, want 3/3", f.TransferredPages, f.TotalPages)
	}
	if f.BadRows != 2 {
		t.Errorf("BadRows = %d, want 2", f.BadRows)
	}
	if f.LongestBadRowRun != 5 {
		t.Errorf("LongestBadRowRun = %d, want 5", f.LongestBadRowRun)
	}
	if f.Encoding != 4 || f.EncodingName != "T.6" {
		t.Errorf("Encoding = %d/%q, want 4/T.6", f.Encoding, f.EncodingName)
	}
	if f.ImageSize != 48394 {
		t.Errorf("ImageSize = %d, want 48394", f.ImageSize)
	}
	if f.ImageResolution == nil || f.ImageResolution.String() != "504x98" {
		t.Errorf("ImageResolution = %v, want 504x98", f.ImageResolution)
	}
	if f.FileImageResolution == nil || f.FileImageResolution.String() != "504x196" {
		t.Errorf("FileImageResolution = %v, want 504x196", f.FileImageResolution)
	}
	if f.ImagePixelSize == nil || f.ImagePixelSize.String() != "1728x1146" {
		t.Errorf("ImagePixelSize = %v, want 1728x1146", f.ImagePixelSize)
	}
	if f.FileImagePixelSize == nil || f.FileImagePixelSize.String() != "1728x2292" {
		t.Errorf("FileImagePixelSize = %v, want 1728x2292", f.FileImagePixelSize)
	}
	if f.TransferRate != 14400 {
		t.Errorf("TransferRate = %d, want 14400", f.TransferRate)
	}
	if !f.Ecm || !f.EcmRequested {
		t.Errorf("Ecm=%v EcmRequested=%v, want both true", f.Ecm, f.EcmRequested)
	}
	if f.T38Status != "negotiated" {
		t.Errorf("T38Status = %q, want negotiated", f.T38Status)
	}
	if !f.V17Disabled {
		t.Error("V17Disabled not set from fax_v17_disabled=1")
	}
	if f.LocalID != "6045551234" || f.RemoteID != "7785559876" {
		t.Errorf("LocalID/RemoteID = %q/%q", f.LocalID, f.RemoteID)
	}
	if f.RemoteCountry != "Canada" || f.RemoteVendor != "Panasonic" || f.RemoteModel != "KX-FLB801" {
		t.Errorf("remote station = %q/%q/%q", f.RemoteCountry, f.RemoteVendor, f.RemoteModel)
	}
	if f.TraceFile != "/var/log/faxtrace/123.log" {
		t.Errorf("TraceFile = %q", f.TraceFile)
	}

	// Verbatim dump: all result vars present, input vars excluded.
	if len(f.ResultVars) != 26 {
		t.Errorf("ResultVars has %d entries, want 26", len(f.ResultVars))
	}
	if f.ResultVars["fax_filename"] != "/tmp/fax_123.tiff" {
		t.Errorf("ResultVars[fax_filename] = %q", f.ResultVars["fax_filename"])
	}
	if _, ok := f.ResultVars["fax_enable_t38"]; ok {
		t.Error("input variable fax_enable_t38 leaked into ResultVars")
	}
	if _, ok := f.ResultVars["fax_ident"]; ok {
		t.Error("input variable fax_ident leaked into ResultVars")
	}

	// Loop control untouched: HangupCause/EndTs stay with CHANNEL_CALLSTATE.
	if f.HangupCause != "" {
		t.Errorf("HangupCause set by CHANNEL_HANGUP harvest: %q", f.HangupCause)
	}
	if !f.EndTs.IsZero() {
		t.Error("EndTs set by CHANNEL_HANGUP harvest")
	}
}

func TestHarvestResultVarsMergeOnly(t *testing.T) {
	f := newTestFaxResult()
	// Simulate state populated earlier by the spandsp custom events.
	f.Success = true
	f.Ecm = true
	f.T38Status = "negotiated"
	f.RemoteID = "7785559876"
	f.LocalID = "6045551234"

	// A failed-session hangup dump must never unset existing state.
	f.AddEvent(hangupEvent(map[string]string{
		"fax_success":           "0",
		"fax_ecm_used":          "off",
		"fax_t38_status":        "refused",
		"fax_remote_station_id": "999",
		"fax_local_station_id":  "111",
		"fax_bad_rows":          "7",
	}))

	if !f.Success {
		t.Error("Success unset by fax_success=0")
	}
	if !f.Ecm {
		t.Error("Ecm unset by fax_ecm_used=off")
	}
	if f.T38Status != "negotiated" {
		t.Errorf("T38Status overwritten: %q", f.T38Status)
	}
	if f.RemoteID != "7785559876" || f.LocalID != "6045551234" {
		t.Errorf("station IDs overwritten: %q/%q", f.RemoteID, f.LocalID)
	}
	if f.BadRows != 7 {
		t.Errorf("BadRows = %d, want 7", f.BadRows)
	}
}

func TestHarvestResultVarsNoFaxVars(t *testing.T) {
	f := newTestFaxResult()
	// Bridged-call hangup: plenty of variables, no fax result vars.
	f.AddEvent(hangupEvent(map[string]string{
		"sip_to_user":    "1234",
		"fax_enable_t38": "true",
		"fax_ident":      "MyFax",
	}))
	if f.ResultVars != nil {
		t.Errorf("ResultVars = %v, want nil", f.ResultVars)
	}
	if f.Success || f.BadRows != 0 || f.EncodingName != "" {
		t.Error("fields populated from non-result variables")
	}
}

func TestResultEventLastPageStats(t *testing.T) {
	f := newTestFaxResult()
	f.AddEvent(customEvent("spandsp::rxfaxresult", map[string]string{
		"Fax-Success":                    "1",
		"Fax-Result-Code":                "0",
		"Fax-Result-Text":                "OK",
		"Fax-Document-Total-Pages":       "2",
		"Fax-Document-Transferred-Pages": "2",
		"Fax-Bad-Rows":                   "3",
		"Fax-Longest-Bad-Row-Run":        "9",
		"Fax-Encoding":                   "4",
		"Fax-Encoding-Name":              "T.6",
		"Fax-Image-Size":                 "12345",
		"Fax-Image-Resolution":           "504x98",
		"Fax-Image-Pixel-Size":           "1728x1146",
		"Fax-Transfer-Rate":              "14400",
	}))

	if !f.Success || f.TotalPages != 2 || f.TransferredPages != 2 {
		t.Errorf("base result fields wrong: %+v", f)
	}
	if f.BadRows != 3 || f.LongestBadRowRun != 9 {
		t.Errorf("bad rows = %d/%d, want 3/9", f.BadRows, f.LongestBadRowRun)
	}
	if f.Encoding != 4 || f.EncodingName != "T.6" {
		t.Errorf("Encoding = %d/%q", f.Encoding, f.EncodingName)
	}
	if f.ImageSize != 12345 {
		t.Errorf("ImageSize = %d", f.ImageSize)
	}
	if f.ImageResolution == nil || f.ImageResolution.String() != "504x98" {
		t.Errorf("ImageResolution = %v", f.ImageResolution)
	}
	if f.ImagePixelSize == nil || f.ImagePixelSize.String() != "1728x1146" {
		t.Errorf("ImagePixelSize = %v", f.ImagePixelSize)
	}
}

func TestNegotiateEventRemoteStationInfo(t *testing.T) {
	f := newTestFaxResult()
	f.AddEvent(customEvent("spandsp::rxfaxnegociateresult", map[string]string{
		"Fax-Remote-Station-Id":       "7785559876",
		"Fax-Local-Station-Id":        "6045551234",
		"Fax-Transfer-Rate":           "14400",
		"Fax-Ecm-Used":                "on",
		"Variable_fax_remote_country": "Canada",
		"Variable_fax_remote_vendor":  "Panasonic",
		"Variable_fax_remote_model":   "KX-FLB801",
	}))
	if f.RemoteCountry != "Canada" || f.RemoteVendor != "Panasonic" || f.RemoteModel != "KX-FLB801" {
		t.Errorf("remote station = %q/%q/%q", f.RemoteCountry, f.RemoteVendor, f.RemoteModel)
	}
}

func TestPageResultEncoding(t *testing.T) {
	f := newTestFaxResult()
	f.AddEvent(customEvent("spandsp::txfaxpageresult", map[string]string{
		"Fax-Document-Transferred-Pages": "1",
		"Fax-Bad-Rows":                   "0",
		"Fax-Encoding":                   "4",
		"Fax-Encoding-Name":              "T.6",
		"Fax-Image-Pixel-Size":           "1728x1146",
	}))
	if len(f.PageResults) != 1 {
		t.Fatalf("PageResults len = %d, want 1", len(f.PageResults))
	}
	if f.PageResults[0].Encoding != 4 || f.PageResults[0].EncodingName != "T.6" {
		t.Errorf("page Encoding = %d/%q", f.PageResults[0].Encoding, f.PageResults[0].EncodingName)
	}
}

// ----------------------- bridge per-leg T.38 detection --------------------

func newTestBridgeFaxResult() *FaxResult {
	return NewFaxResult(uuid.New(), NewLogManager(&LokiClient{}), true)
}

const audioSDP = "v=0\r\n" +
	"o=FreeSWITCH 1699000000 1699000001 IN IP4 10.0.0.1\r\n" +
	"s=FreeSWITCH\r\n" +
	"c=IN IP4 10.0.0.1\r\n" +
	"t=0 0\r\n" +
	"m=audio 31926 RTP/AVP 0 8 101\r\n" +
	"a=rtpmap:0 PCMU/8000\r\n" +
	"a=rtpmap:101 telephone-event/8000\r\n"

const t38UDPTLSDP = "v=0\r\n" +
	"o=FreeSWITCH 1699000000 1699000002 IN IP4 10.0.0.1\r\n" +
	"s=FreeSWITCH\r\n" +
	"c=IN IP4 10.0.0.1\r\n" +
	"t=0 0\r\n" +
	"m=image 4002 udptl t38\r\n" +
	"a=T38FaxVersion:0\r\n" +
	"a=T38MaxBitRate:14400\r\n"

func TestDetectT38InSDP(t *testing.T) {
	cases := []struct {
		name string
		sdp  string
		want bool
	}{
		{"empty", "", false},
		{"audio only", audioSDP, false},
		{"udptl image", t38UDPTLSDP, true},
		{"uppercase UDPTL", "m=image 4002 UDPTL T38\r\n", true},
		{"tcp t38", "m=image 4002 tcp t38\r\n", true},
		{"audio with t38 attribute but no image m-line", "m=audio 1000 RTP/AVP 0\r\na=T38FaxVersion:0\r\n", false},
	}
	for _, c := range cases {
		if got := detectT38InSDP(c.sdp); got != c.want {
			t.Errorf("%s: detectT38InSDP = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBridgeHangupHarvestsALegT38(t *testing.T) {
	f := newTestBridgeFaxResult()
	f.AddEvent(hangupEvent(map[string]string{
		"switch_r_sdp":   t38UDPTLSDP,
		"switch_l_sdp":   t38UDPTLSDP,
		"fax_enable_t38": "true", // input variable, must not leak anywhere
	}))

	if f.BridgeALegT38 != "negotiated" {
		t.Errorf("BridgeALegT38 = %q, want negotiated", f.BridgeALegT38)
	}
	// SDP harvest must not touch fax result state or loop control.
	if f.ResultVars != nil {
		t.Errorf("ResultVars = %v, want nil", f.ResultVars)
	}
	if f.T38Status != "" {
		t.Errorf("T38Status = %q, want empty (no fax app ran)", f.T38Status)
	}
	if f.HangupCause != "" || !f.EndTs.IsZero() {
		t.Error("loop-control fields touched by CHANNEL_HANGUP harvest")
	}
}

func TestBridgeHangupAudioSDPLeavesT38Empty(t *testing.T) {
	f := newTestBridgeFaxResult()
	f.AddEvent(hangupEvent(map[string]string{
		"switch_r_sdp": audioSDP,
		"switch_l_sdp": audioSDP,
	}))
	if f.BridgeALegT38 != "" {
		t.Errorf("BridgeALegT38 = %q, want empty (audio final SDP proves nothing)", f.BridgeALegT38)
	}
}

func TestBridgeHangupNoSDPLeavesT38Empty(t *testing.T) {
	f := newTestBridgeFaxResult()
	f.AddEvent(hangupEvent(map[string]string{
		"sip_to_user": "1234",
	}))
	if f.BridgeALegT38 != "" {
		t.Errorf("BridgeALegT38 = %q, want empty (no SDP variables)", f.BridgeALegT38)
	}
}

func TestNonBridgeHangupIgnoresSDP(t *testing.T) {
	// Softmodem/reception results must never get bridge per-leg fields,
	// even if the hangup dump happens to carry SDP variables.
	f := newTestFaxResult()
	f.AddEvent(hangupEvent(map[string]string{
		"switch_r_sdp":   t38UDPTLSDP,
		"fax_t38_status": "negotiated",
	}))
	if f.BridgeALegT38 != "" || f.BridgeBLegT38 != "" {
		t.Errorf("bridge leg fields set on non-bridge result: %q/%q", f.BridgeALegT38, f.BridgeBLegT38)
	}
	if f.T38Status != "negotiated" {
		t.Errorf("T38Status = %q, want negotiated", f.T38Status)
	}
}

func TestBridgeLegT38MergeOnly(t *testing.T) {
	f := newTestBridgeFaxResult()
	f.BridgeALegT38 = "negotiated"
	// A later dump whose SDP is audio-only (e.g. re-INVITE back to audio
	// after the fax) must never unset the confirmed negotiation.
	f.AddEvent(hangupEvent(map[string]string{
		"switch_r_sdp": audioSDP,
		"switch_l_sdp": audioSDP,
	}))
	if f.BridgeALegT38 != "negotiated" {
		t.Errorf("BridgeALegT38 = %q, want negotiated (merge-only)", f.BridgeALegT38)
	}
}

func TestAddBridgeBLegEvent(t *testing.T) {
	f := newTestBridgeFaxResult()

	// Non-hangup b-leg events are ignored.
	f.AddBridgeBLegEvent(customEvent("spandsp::rxfaxresult", map[string]string{
		"Fax-Success": "1",
	}))
	if f.BridgeBLegT38 != "" || f.Success {
		t.Error("non-hangup b-leg event mutated the result")
	}

	// B-leg hangup dump with T.38 SDP confirms the bridge leg.
	f.AddBridgeBLegEvent(hangupEvent(map[string]string{
		"switch_l_sdp": t38UDPTLSDP,
	}))
	if f.BridgeBLegT38 != "negotiated" {
		t.Errorf("BridgeBLegT38 = %q, want negotiated", f.BridgeBLegT38)
	}
	// A-leg field untouched by b-leg events.
	if f.BridgeALegT38 != "" {
		t.Errorf("BridgeALegT38 = %q, want empty", f.BridgeALegT38)
	}

	// Non-bridge results ignore b-leg events entirely.
	plain := newTestFaxResult()
	plain.AddBridgeBLegEvent(hangupEvent(map[string]string{
		"switch_l_sdp": t38UDPTLSDP,
	}))
	if plain.BridgeBLegT38 != "" {
		t.Errorf("non-bridge BridgeBLegT38 = %q, want empty", plain.BridgeBLegT38)
	}
}
