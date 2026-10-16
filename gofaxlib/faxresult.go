// This file is part of gofaxserver - https://github.com/sagostin/gofaxserver
// Originally part of the GOfax.IP project - https://github.com/gonicus/gofaxip
// Copyright (C) 2014 GONICUS GmbH, Germany - http://www.gonicus.de
// Modifications Copyright (C) 2025-2026 Shaun Agostinho
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
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fiorix/go-eventsocket/eventsocket"
	"github.com/google/uuid"
)

// Resolution is the image resolution of a fax
type Resolution struct {
	X uint
	Y uint
}

func (r Resolution) String() string {
	return fmt.Sprintf("%vx%v", r.X, r.Y)
}

func parseResolution(resstr string) (*Resolution, error) {
	parts := strings.SplitN(resstr, "x", 2)
	if len(parts) != 2 {
		return nil, errors.New("error parsing resolution string")
	}
	res := new(Resolution)
	if x, err := strconv.Atoi(parts[0]); err == nil {
		res.X = uint(x)
	} else {
		return nil, err
	}
	if y, err := strconv.Atoi(parts[1]); err == nil {
		res.Y = uint(y)
	} else {
		return nil, err
	}
	return res, nil
}

// PageResult is the result of a transmitted Faxing page as reported by SpanDSP
type PageResult struct {
	Ts               time.Time  `json:"ts"`
	Page             uint       `json:"page,omitempty"`
	BadRows          uint       `json:"bad_rows,omitempty"`
	LongestBadRowRun uint       `json:"longest_bad_row_run,omitempty"`
	Encoding         int        `json:"encoding,omitempty"`
	EncodingName     string     `json:"encoding_name,omitempty"`
	ImagePixelSize   Resolution `json:"image_pixel_size"`
	FilePixelSize    Resolution `json:"file_pixel_size"`
	ImageResolution  Resolution `json:"image_resolution"`
	FileResolution   Resolution `json:"file_resolution"`
	ImageSize        uint       `json:"image_size,omitempty"`
}

func (p PageResult) String() string {
	return fmt.Sprintf("Image Size: %v, Compression: %v, Comp Size: %v bytes, Bad Rows: %v",
		p.ImagePixelSize, p.EncodingName, p.ImageSize, p.BadRows)
}

// FaxResult is the result of a completed or aborted Faxing transmission
type FaxResult struct {
	UUID       uuid.UUID `json:"uuid,omitempty"`
	logManager *LogManager

	StartTs time.Time `json:"start_ts"`
	EndTs   time.Time `json:"end_ts"`

	HangupCause string `json:"hangupcause,omitempty"`

	TotalPages       uint   `json:"total_pages,omitempty"`
	TransferredPages uint   `json:"transferred_pages,omitempty"`
	Ecm              bool   `json:"ecm,omitempty"`
	EcmRequested     bool   `json:"ecm_requested,omitempty"` // was ECM requested (vs actually used)
	RemoteID         string `json:"remote_id,omitempty"`
	LocalID          string `json:"local_id,omitempty"` // local station ID
	ResultCode       int    `json:"result_code,omitempty"`
	ResultText       string `json:"result_text,omitempty"`
	Success          bool   `json:"success"`
	TransferRate     uint   `json:"transfer_rate,omitempty"`
	NegotiateCount   uint   `json:"negotiate_count,omitempty"`
	T38Status        string `json:"t38_status,omitempty"`   // "negotiated", "rejected", etc.
	V17Disabled      bool   `json:"v17_disabled,omitempty"` // was V.17 disabled
	bridge           bool
	PageResults      []PageResult `json:"page_results,omitempty"`

	// Session-level completion metadata, harvested from the fax result
	// variables mod_spandsp sets on the channel when rxfax/txfax finishes
	// (see harvestResultVars). The per-page stats mirror the last
	// transferred page; the remote station details are reported by the far
	// end at T.30 phase B negotiation (empty when not provided).
	BadRows             uint        `json:"bad_rows,omitempty"`
	LongestBadRowRun    uint        `json:"longest_bad_row_run,omitempty"`
	Encoding            int         `json:"encoding,omitempty"`
	EncodingName        string      `json:"encoding_name,omitempty"`
	ImageSize           uint        `json:"image_size,omitempty"`
	ImageResolution     *Resolution `json:"image_resolution,omitempty"`
	FileImageResolution *Resolution `json:"file_image_resolution,omitempty"`
	ImagePixelSize      *Resolution `json:"image_pixel_size,omitempty"`
	FileImagePixelSize  *Resolution `json:"file_image_pixel_size,omitempty"`
	RemoteCountry       string      `json:"remote_country,omitempty"`
	RemoteVendor        string      `json:"remote_vendor,omitempty"`
	RemoteModel         string      `json:"remote_model,omitempty"`
	TraceFile           string      `json:"trace_file,omitempty"`

	// ResultVars is the verbatim dump of every fax result variable seen on
	// the channel at completion (names without the "Variable_" prefix),
	// persisted as the results entry's metadata JSON.
	ResultVars map[string]string `json:"result_vars,omitempty"`
}

// NewFaxResult creates a new FaxResult structure
func NewFaxResult(uuid uuid.UUID, logManager *LogManager, bridge bool) *FaxResult {
	f := &FaxResult{
		UUID:       uuid,
		logManager: logManager,
		bridge:     bridge,
	}
	return f
}

// AddEvent parses a FreeSWITCH EventSocket event and merges contained information into the FaxResult
func (f *FaxResult) AddEvent(ev *eventsocket.Event) {
	// fmt.Printf("DEBUG: %v", ev.String())

	switch ev.Get("Event-Name") {
	case "CHANNEL_CALLSTATE":
		// Call state has changed
		callstate := ev.Get("Channel-Call-State")
		f.logManager.SendLog(f.logManager.BuildLog(
			"FaxResult",
			"Call state change: %v",
			logrus.InfoLevel,
			map[string]interface{}{"uuid": f.UUID.String()}, callstate))
		if callstate == "ACTIVE" {
			f.StartTs = time.Now()
		}
		if callstate == "HANGUP" {
			f.EndTs = time.Now()
			f.HangupCause = ev.Get("Hangup-Cause")
		}

	case "CHANNEL_HANGUP":
		// Full channel-variable dump: carries every fax result variable
		// (Variable_fax_*) mod_spandsp set when rxfax/txfax completed.
		// Fires before the CHANNEL_CALLSTATE HANGUP event both event loops
		// use as their exit signal, so this is the authoritative final
		// snapshot. Deliberately does NOT touch HangupCause/EndTs — loop
		// control stays with CHANNEL_CALLSTATE exactly as before.
		f.harvestResultVars(ev)

	case "CUSTOM":
		// Faxing results have changed
		action := ""
		subclass := ev.Get("Event-Subclass")

		// Debug: Log all fax-related headers for SpanDSP events
		if strings.HasPrefix(subclass, "spandsp::") {
			// Build a map of all fax-related headers
			faxHeaders := make(map[string]string)
			for key, value := range ev.Header {
				// Capture all Fax-* headers and other relevant ones
				if strings.HasPrefix(key, "Fax-") ||
					key == "Event-Subclass" ||
					key == "Channel-Call-Uuid" ||
					strings.Contains(key, "fax_v17") ||
					strings.Contains(key, "fax_ecm") ||
					strings.Contains(key, "fax_t38") ||
					strings.Contains(key, "fax_result") {
					// Type-assert the value to string
					if strVal, ok := value.(string); ok {
						faxHeaders[key] = strVal
					} else {
						faxHeaders[key] = fmt.Sprintf("%v", value)
					}
				}
			}
			f.logManager.SendLog(f.logManager.BuildLog(
				"FaxResult",
				"SpanDSP event: %s",
				logrus.DebugLevel,
				map[string]interface{}{
					"uuid":        f.UUID.String(),
					"subclass":    subclass,
					"fax_headers": faxHeaders,
				},
				subclass,
			))
		}

		switch subclass {
		case "spandsp::rxfaxnegociateresult",
			"spandsp::txfaxnegociateresult":
			f.NegotiateCount++
			if ecm := ev.Get("Fax-Ecm-Used"); ecm == "on" {
				f.Ecm = true
			}
			// Capture ECM requested (whether ECM was requested, even if not used)
			if ecmReq := ev.Get("Variable_fax_ecm_requested"); ecmReq == "1" {
				f.EcmRequested = true
			}
			f.RemoteID = ev.Get("Fax-Remote-Station-Id")
			f.LocalID = ev.Get("Fax-Local-Station-Id")
			f.T38Status = ev.Get("Fax-T38-Status")
			// V17 disabled tracking
			if v17 := ev.Get("Variable_fax_v17_disabled"); v17 == "1" {
				f.V17Disabled = true
			}
			// Remote station details, reported at T.30 phase B negotiation
			// (opportunistic: also harvested from the hangup variable dump)
			if country := ev.Get("Variable_fax_remote_country"); country != "" {
				f.RemoteCountry = country
			}
			if vendor := ev.Get("Variable_fax_remote_vendor"); vendor != "" {
				f.RemoteVendor = vendor
			}
			if model := ev.Get("Variable_fax_remote_model"); model != "" {
				f.RemoteModel = model
			}
			if rate, err := strconv.Atoi(ev.Get("Fax-Transfer-Rate")); err == nil {
				f.TransferRate = uint(rate)
			}
			f.logManager.SendLog(f.logManager.BuildLog(
				"FaxResult",
				"Negotiation: RemoteID=%q, LocalID=%q, Rate=%v, ECM=%v, T38=%v",
				logrus.InfoLevel,
				map[string]interface{}{
					"uuid":          f.UUID.String(),
					"remote_id":     f.RemoteID,
					"local_id":      f.LocalID,
					"transfer_rate": f.TransferRate,
					"ecm":           f.Ecm,
					"ecm_requested": f.EcmRequested,
					"t38_status":    f.T38Status,
					"v17_disabled":  f.V17Disabled,
				}, f.RemoteID, f.LocalID, f.TransferRate, f.Ecm, f.T38Status))

		case "spandsp::rxfaxpageresult":
			action = "received"
			fallthrough
		case "spandsp::txfaxpageresult":
			if action == "" {
				action = "sent"
			}
			// A page was transferred
			if pages, err := strconv.Atoi(ev.Get("Fax-Document-Transferred-Pages")); err == nil {
				f.TransferredPages = uint(pages)
			}

			pr := new(PageResult)
			pr.Page = f.TransferredPages

			if badrows, err := strconv.Atoi(ev.Get("Fax-Bad-Rows")); err == nil {
				pr.BadRows = uint(badrows)
			}
			pr.EncodingName = ev.Get("Fax-Encoding-Name")
			if enc, err := strconv.Atoi(ev.Get("Fax-Encoding")); err == nil {
				pr.Encoding = enc
			}
			if imgsize, err := parseResolution(ev.Get("Fax-Image-Pixel-Size")); err == nil {
				pr.ImagePixelSize = *imgsize
			}
			if filesize, err := parseResolution(ev.Get("Fax-File-Image-Pixel-Size")); err == nil {
				pr.FilePixelSize = *filesize
			}
			if imgres, err := parseResolution(ev.Get("Fax-Image-Resolution")); err == nil {
				pr.ImageResolution = *imgres
			}
			if fileres, err := parseResolution(ev.Get("Fax-File-Image-Resolution")); err == nil {
				pr.FileResolution = *fileres
			}
			if size, err := strconv.Atoi(ev.Get("Fax-Image-Size")); err == nil {
				pr.ImageSize = uint(size)
			}
			if badrowrun, err := strconv.Atoi(ev.Get("Fax-Longest-Bad-Row-Run")); err == nil {
				pr.LongestBadRowRun = uint(badrowrun)
			}

			pr.Ts = time.Now()

			f.PageResults = append(f.PageResults, *pr)
			f.logManager.SendLog(f.logManager.BuildLog(
				"FaxResult",
				"Page %d %v: %v",
				logrus.InfoLevel,
				map[string]interface{}{"uuid": f.UUID.String()}, f.TransferredPages, action, *pr))

		case "spandsp::rxfaxresult",
			"spandsp::txfaxresult":
			if totalpages, err := strconv.Atoi(ev.Get("Fax-Document-Total-Pages")); err == nil {
				f.TotalPages = uint(totalpages)
			}
			if transferredpages, err := strconv.Atoi(ev.Get("Fax-Document-Transferred-Pages")); err == nil {
				f.TransferredPages = uint(transferredpages)
			}
			if ecm := ev.Get("Fax-Ecm-Used"); ecm == "on" {
				f.Ecm = true
			}
			f.RemoteID = ev.Get("Fax-Remote-Station-Id")
			if resultcode, err := strconv.Atoi(ev.Get("Fax-Result-Code")); err == nil {
				f.ResultCode = resultcode
			}
			f.ResultText = ev.Get("Fax-Result-Text")
			if ev.Get("Fax-Success") == "1" {
				f.Success = true
			}
			if rate, err := strconv.Atoi(ev.Get("Fax-Transfer-Rate")); err == nil {
				f.TransferRate = uint(rate)
			}
			// Last-page stats. Only a subset of mod_spandsp builds attaches
			// these to the completion event — the CHANNEL_HANGUP variable
			// dump (harvestResultVars) is the guaranteed source; parse them
			// here too so either path populates the session metadata.
			if badrows, err := strconv.Atoi(ev.Get("Fax-Bad-Rows")); err == nil {
				f.BadRows = uint(badrows)
			}
			if badrowrun, err := strconv.Atoi(ev.Get("Fax-Longest-Bad-Row-Run")); err == nil {
				f.LongestBadRowRun = uint(badrowrun)
			}
			if enc, err := strconv.Atoi(ev.Get("Fax-Encoding")); err == nil {
				f.Encoding = enc
			}
			if encName := ev.Get("Fax-Encoding-Name"); encName != "" {
				f.EncodingName = encName
			}
			if size, err := strconv.Atoi(ev.Get("Fax-Image-Size")); err == nil {
				f.ImageSize = uint(size)
			}
			if res, err := parseResolution(ev.Get("Fax-Image-Resolution")); err == nil {
				f.ImageResolution = res
			}
			if res, err := parseResolution(ev.Get("Fax-File-Image-Resolution")); err == nil {
				f.FileImageResolution = res
			}
			if res, err := parseResolution(ev.Get("Fax-Image-Pixel-Size")); err == nil {
				f.ImagePixelSize = res
			}
			if res, err := parseResolution(ev.Get("Fax-File-Image-Pixel-Size")); err == nil {
				f.FileImagePixelSize = res
			}

		}
	}

}

// faxResultVarNames is the whitelist of fax result variables mod_spandsp
// sets on the channel when rxfax/txfax completes. Only these are harvested —
// the channel also carries *input* variables (fax_enable_t38, fax_ident,
// fax_use_ecm, ...) that must not be misread as results.
var faxResultVarNames = []string{
	"fax_success",
	"fax_result_code",
	"fax_result_text",
	"fax_filename",
	"fax_document_transferred_pages",
	"fax_document_total_pages",
	"fax_image_resolution",
	"fax_file_image_resolution",
	"fax_image_pixel_size",
	"fax_file_image_pixel_size",
	"fax_image_size",
	"fax_bad_rows",
	"fax_longest_bad_row_run",
	"fax_encoding",
	"fax_encoding_name",
	"fax_transfer_rate",
	"fax_ecm_used",
	"fax_ecm_requested",
	"fax_t38_status",
	"fax_v17_disabled",
	"fax_local_station_id",
	"fax_remote_station_id",
	"fax_remote_country",
	"fax_remote_vendor",
	"fax_remote_model",
	"fax_trace_file",
}

// harvestResultVars reads the fax result variables (Variable_fax_* headers)
// off an event carrying the channel-variable dump — CHANNEL_HANGUP in both
// the inbound and outbound paths. Merge-only: values already populated by the
// spandsp custom events are never unset (Success/Ecm only ever flip to true;
// negotiated identifiers are backfilled, not overwritten). For bridged calls
// no fax app ran, so no result variables exist and this is a no-op.
func (f *FaxResult) harvestResultVars(ev *eventsocket.Event) {
	found := false
	for _, name := range faxResultVarNames {
		v := ev.Get("Variable_" + name)
		if v == "" {
			continue
		}
		found = true
		if f.ResultVars == nil {
			f.ResultVars = make(map[string]string)
		}
		f.ResultVars[name] = v

		switch name {
		case "fax_success":
			if v == "1" {
				f.Success = true
			}
		case "fax_result_code":
			if n, err := strconv.Atoi(v); err == nil {
				f.ResultCode = n
			}
		case "fax_result_text":
			f.ResultText = v
		case "fax_document_transferred_pages":
			if n, err := strconv.Atoi(v); err == nil {
				f.TransferredPages = uint(n)
			}
		case "fax_document_total_pages":
			if n, err := strconv.Atoi(v); err == nil {
				f.TotalPages = uint(n)
			}
		case "fax_image_resolution":
			if r, err := parseResolution(v); err == nil {
				f.ImageResolution = r
			}
		case "fax_file_image_resolution":
			if r, err := parseResolution(v); err == nil {
				f.FileImageResolution = r
			}
		case "fax_image_pixel_size":
			if r, err := parseResolution(v); err == nil {
				f.ImagePixelSize = r
			}
		case "fax_file_image_pixel_size":
			if r, err := parseResolution(v); err == nil {
				f.FileImagePixelSize = r
			}
		case "fax_image_size":
			if n, err := strconv.Atoi(v); err == nil {
				f.ImageSize = uint(n)
			}
		case "fax_bad_rows":
			if n, err := strconv.Atoi(v); err == nil {
				f.BadRows = uint(n)
			}
		case "fax_longest_bad_row_run":
			if n, err := strconv.Atoi(v); err == nil {
				f.LongestBadRowRun = uint(n)
			}
		case "fax_encoding":
			if n, err := strconv.Atoi(v); err == nil {
				f.Encoding = n
			}
		case "fax_encoding_name":
			f.EncodingName = v
		case "fax_transfer_rate":
			if n, err := strconv.Atoi(v); err == nil {
				f.TransferRate = uint(n)
			}
		case "fax_ecm_used":
			if v == "on" {
				f.Ecm = true
			}
		case "fax_ecm_requested":
			// "1"/"0" normally; "true"/"false" in gateway mode
			if v == "1" || v == "true" {
				f.EcmRequested = true
			}
		case "fax_t38_status":
			if f.T38Status == "" {
				f.T38Status = v
			}
		case "fax_v17_disabled":
			if v == "1" {
				f.V17Disabled = true
			}
		case "fax_local_station_id":
			if f.LocalID == "" {
				f.LocalID = v
			}
		case "fax_remote_station_id":
			if f.RemoteID == "" {
				f.RemoteID = v
			}
		case "fax_remote_country":
			f.RemoteCountry = v
		case "fax_remote_vendor":
			f.RemoteVendor = v
		case "fax_remote_model":
			f.RemoteModel = v
		case "fax_trace_file":
			f.TraceFile = v
		}
	}

	if found {
		f.logManager.SendLog(f.logManager.BuildLog(
			"FaxResult",
			"Harvested fax result variables: success=%v bad_rows=%v encoding=%v pages=%v/%v",
			logrus.DebugLevel,
			map[string]interface{}{
				"uuid":        f.UUID.String(),
				"result_vars": f.ResultVars,
			},
			f.Success, f.BadRows, f.EncodingName, f.TransferredPages, f.TotalPages))
	}
}
