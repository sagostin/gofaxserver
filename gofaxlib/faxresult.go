// This file is part of the GOfax.IP project - https://github.com/gonicus/gofaxip
// Copyright (C) 2014 GONICUS GmbH, Germany - http://www.gonicus.de
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
	"github.com/sirupsen/logrus"
	"strconv"
	"strings"
	"time"

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
	RemoteID         string `json:"remote_id,omitempty"`
	ResultCode       int    `json:"result_code,omitempty"` // SpanDSP, not HylaFAX!
	ResultText       string `json:"result_text,omitempty"`
	Success          bool   `json:"success"`
	TransferRate     uint   `json:"transfer_rate,omitempty"`
	NegotiateCount   uint   `json:"negotiate_count,omitempty"`
	bridge           bool
	PageResults      []PageResult `json:"page_results,omitempty"`
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

	case "CUSTOM":
		// Faxing results have changed
		action := ""
		switch ev.Get("Event-Subclass") {
		case "spandsp::rxfaxnegociateresult",
			"spandsp::txfaxnegociateresult":
			f.NegotiateCount++
			if ecm := ev.Get("Fax-Ecm-Used"); ecm == "on" {
				f.Ecm = true
			}
			f.RemoteID = ev.Get("Fax-Remote-Station-Id")
			if rate, err := strconv.Atoi(ev.Get("Fax-Transfer-Rate")); err == nil {
				f.TransferRate = uint(rate)
			}
			f.logManager.SendLog(f.logManager.BuildLog(
				"FaxResult",
				"Remote ID: \"%v\", Transfer Rate: %v, ECM=%v",
				logrus.InfoLevel,
				map[string]interface{}{"uuid": f.UUID.String()}, f.RemoteID, f.TransferRate, f.Ecm))

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

		}
	}

}
