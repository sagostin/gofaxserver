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

package gofaxserver

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/fiorix/go-eventsocket/eventsocket"
	"github.com/sirupsen/logrus"
	"gofaxserver/gofaxlib"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

/*// SendQfileFromDisk reads the qfile from disk and then immediately tries to send the given qfile using FreeSWITCH
func SendQfileFromDisk(filename, deviceID string) (SendResult, error) {
	// Open qfile
	qf, err := OpenQfile(filename)
	if err != nil {
		return SendFailed, fmt.Errorf("cannot open qfile %v: %w", filename, err)
	}
	defer qf.Close()

	return SendFaxFS(qf, deviceID)
}*/

// SendFax immediately tries to send the given qfile using FreeSWITCH
func (e *EventSocketServer) SendFax(faxjob *FaxJob) (returned SendResult, err error) {
	returned = SendFailed

	// Initial job snapshot
	e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
		"FreeSwitch.SendFax",
		"Processing faxjob as FreeSWITCH call",
		logrus.InfoLevel,
		map[string]interface{}{
			"uuid":             faxjob.UUID.String(),
			"callee_number":    faxjob.CalleeNumber,
			"caller_id_number": faxjob.CallerIdNumber,
			"caller_id_name":   faxjob.CallerIdName,
			"file_name":        faxjob.FileName,
			"endpoints":        faxjob.Endpoints,
			"tot_tries":        faxjob.TotTries,
			"tot_dials":        faxjob.TotDials,
			"use_ecm":          faxjob.UseECM,
			"disable_v17":      faxjob.DisableV17,
		},
	))

	// Auto fallback to slow baudrate after too many tries
	v17retry, err := strconv.Atoi(gofaxlib.Config.Faxing.DisableV17AfterRetry)
	if err != nil {
		v17retry = 0
	}
	if v17retry > 0 && faxjob.TotTries >= v17retry && !faxjob.DisableV17 {
		e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
			"FreeSwitch.SendFax",
			"Disabling V.17 after %d tries (threshold=%d)",
			logrus.WarnLevel,
			map[string]interface{}{
				"uuid":      faxjob.UUID.String(),
				"tot_tries": faxjob.TotTries,
			},
			faxjob.TotTries, v17retry,
		))
		faxjob.DisableV17 = true
	}

	// Auto disable ECM after too many tries
	ecmretry, err := strconv.Atoi(gofaxlib.Config.Faxing.DisableECMAfterRetry)
	if err != nil {
		ecmretry = 0
	}
	if ecmretry > 0 && faxjob.TotTries >= ecmretry && faxjob.UseECM {
		e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
			"FreeSwitch.SendFax",
			"Disabling ECM after %d tries (threshold=%d)",
			logrus.WarnLevel,
			map[string]interface{}{
				"uuid":      faxjob.UUID.String(),
				"tot_tries": faxjob.TotTries,
			},
			faxjob.TotTries, ecmretry,
		))
		faxjob.UseECM = false
	}

	// Update status counters
	faxjob.TotDials++

	// Default: Retry when eventClient fails
	returned = SendRetry

	// Start eventClient goroutine
	transmitTs := time.Now()
	t := newEventClient(faxjob, e.server.LogManager)
	result := &gofaxlib.FaxResult{}
	var status string

	// Wait for events
StatusLoop:
	for {
		select {
		case page := <-t.PageSent():
			faxjob.NPages = int(page.Page)
			e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
				"FreeSwitch.SendFax",
				"Page sent",
				logrus.DebugLevel,
				map[string]interface{}{
					"uuid":               faxjob.UUID.String(),
					"page_number":        page.Page,
					"page_encoding":      page.EncodingName,
					"bad_rows":           page.BadRows,
					"total_page_results": faxjob.NPages,
				},
			))

		case result = <-t.Result():
			faxjob.SignalRate = int(result.TransferRate)
			faxjob.CSI = result.RemoteID

			if result.HangupCause != "" {
				// Final result
				status = result.ResultText
				faxjob.Status = status

				e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
					"FreeSwitch.SendFax",
					"Final fax result received",
					logrus.InfoLevel,
					map[string]interface{}{
						"uuid":              faxjob.UUID.String(),
						"hangup_cause":      result.HangupCause,
						"success":           result.Success,
						"result_text":       result.ResultText,
						"transfer_rate":     result.TransferRate,
						"ecm":               result.Ecm,
						"negotiations":      result.NegotiateCount,
						"transferred_pages": result.TransferredPages,
					},
				))

				if result.Success {
					faxjob.Result = result
				}
				break StatusLoop
			}

			// Negotiation finished, but call still up
			negstatus := fmt.Sprintf("Sending %d", result.TransferRate)
			if result.Ecm {
				negstatus += "/ECM"
			}
			status = negstatus
			faxjob.TotTries++
			faxjob.NDials = 0
			faxjob.Status = status

			e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
				"FreeSwitch.SendFax",
				"Negotiation result",
				logrus.DebugLevel,
				map[string]interface{}{
					"uuid":              faxjob.UUID.String(),
					"status":            status,
					"transfer_rate":     result.TransferRate,
					"ecm":               result.Ecm,
					"tot_tries":         faxjob.TotTries,
					"negotiations":      result.NegotiateCount,
					"transferred_pages": result.TransferredPages,
				},
			))

		case faxerr := <-t.Errors():
			faxjob.NDials++
			status = faxerr.Error()
			if faxerr.Retry() {
				returned = SendRetry
			} else {
				returned = SendFailed
			}

			e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
				"FreeSwitch.SendFax",
				"Event client error",
				logrus.ErrorLevel,
				map[string]interface{}{
					"uuid":          faxjob.UUID.String(),
					"error":         faxerr.Error(),
					"retry":         faxerr.Retry(),
					"ndials":        faxjob.NDials,
					"send_result":   returned.String(),
					"current_state": faxjob.Status,
				},
			))

			break StatusLoop
		}
	}

	faxjob.Status = status
	faxjob.Returned = strconv.Itoa(int(returned))
	faxjob.Ts = transmitTs
	faxjob.JobTime = time.Since(transmitTs)

	if result != nil {
		if result.Success {
			returned = SendDone
			faxjob.Result = result
			err = nil

			e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
				"FreeSwitch.SendFax",
				"Faxing sent successfully",
				logrus.InfoLevel,
				map[string]interface{}{
					"uuid":             faxjob.UUID.String(),
					"hangup_cause":     result.HangupCause,
					"result_text":      status,
					"send_result":      returned.String(),
					"duration_ms":      faxjob.JobTime.Milliseconds(),
					"pages":            result.TransferredPages,
					"signal_rate":      result.TransferRate,
					"ecm":              result.Ecm,
					"callee_number":    faxjob.CalleeNumber,
					"caller_id_number": faxjob.CallerIdNumber,
					"caller_id_name":   faxjob.CallerIdName,
					"use_ecm":          faxjob.UseECM,
					"disable_v17":      faxjob.DisableV17,
					"tot_tries":        faxjob.TotTries,
					"tot_dials":        faxjob.TotDials,
				},
			))
		} else {
			faxjob.Result = result
			err = errors.New("faxing failed")

			e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
				"FreeSwitch.SendFax",
				"Faxing failed",
				logrus.ErrorLevel,
				map[string]interface{}{
					"uuid":             faxjob.UUID.String(),
					"retry":            returned == SendRetry,
					"hangup_cause":     result.HangupCause,
					"result_text":      status,
					"send_result":      returned.String(),
					"duration_ms":      faxjob.JobTime.Milliseconds(),
					"pages":            result.TransferredPages,
					"signal_rate":      result.TransferRate,
					"ecm":              result.Ecm,
					"callee_number":    faxjob.CalleeNumber,
					"caller_id_number": faxjob.CallerIdNumber,
					"caller_id_name":   faxjob.CallerIdName,
					"use_ecm":          faxjob.UseECM,
					"disable_v17":      faxjob.DisableV17,
					"tot_tries":        faxjob.TotTries,
					"tot_dials":        faxjob.TotDials,
				},
			))
		}
	} else {
		// No result object – treat as call failure and retry
		returned = SendRetry
		err = errors.New("call failed")

		e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
			"FreeSwitch.SendFax",
			"Call failed without fax result",
			logrus.ErrorLevel,
			map[string]interface{}{
				"uuid":             faxjob.UUID.String(),
				"retry":            returned == SendRetry,
				"result_text":      status,
				"send_result":      returned.String(),
				"duration_ms":      faxjob.JobTime.Milliseconds(),
				"callee_number":    faxjob.CalleeNumber,
				"caller_id_number": faxjob.CallerIdNumber,
				"caller_id_name":   faxjob.CallerIdName,
			},
		))
	}

	return returned, err
}

func (r SendResult) String() string {
	switch r {
	case SendRetry:
		return "SendRetry"
	case SendFailed:
		return "SendFailed"
	case SendDone:
		return "SendDone"
	case SendReformat:
		return "SendReformat"
	case SendV34fail:
		return "SendV34fail"
	case SendV17fail:
		return "SendV17fail"
	case SendBatchfail:
		return "SendBatchfail"
	case SendNobatch:
		return "SendNobatch"
	default:
		return fmt.Sprintf("UnknownSendResult(%d)", int(r))
	}
}

const (
	// Return codes for Hylafax.
	SendRetry SendResult = iota
	SendFailed
	SendDone
	SendReformat
	SendV34fail
	SendV17fail
	SendBatchfail
	SendNobatch
)

type SendResult int

type eventClient struct {
	faxjob *FaxJob
	conn   *eventsocket.Connection

	pageChan   chan *gofaxlib.PageResult
	errorChan  chan FaxError
	resultChan chan *gofaxlib.FaxResult

	logManager *gofaxlib.LogManager
}

func newEventClient(faxjob *FaxJob, logManager *gofaxlib.LogManager) *eventClient {
	t := &eventClient{
		faxjob:     faxjob,
		pageChan:   make(chan *gofaxlib.PageResult),
		errorChan:  make(chan FaxError),
		resultChan: make(chan *gofaxlib.FaxResult),
		logManager: logManager,
	}
	go t.start()
	return t
}

func (t *eventClient) PageSent() <-chan *gofaxlib.PageResult {
	return t.pageChan
}

func (t *eventClient) Errors() <-chan FaxError {
	return t.errorChan
}

func (t *eventClient) Result() <-chan *gofaxlib.FaxResult {
	return t.resultChan
}

// Connect to FreeSWITCH and originate a txfax
// Connect to FreeSWITCH and originate a txfax
func (t *eventClient) start() {

	// Basic validation with logging
	if t.faxjob.CalleeNumber == "" {
		t.logManager.SendLog(t.logManager.BuildLog(
			"EventClient",
			"Number to dial is empty",
			logrus.ErrorLevel,
			map[string]interface{}{"uuid": t.faxjob.UUID.String()},
		))
		t.errorChan <- NewFaxError("Number to dial is empty", false)
		return
	}

	if len(t.faxjob.Endpoints) == 0 {
		t.logManager.SendLog(t.logManager.BuildLog(
			"EventClient",
			"Gateway/endpoints not set",
			logrus.ErrorLevel,
			map[string]interface{}{"uuid": t.faxjob.UUID.String()},
		))
		t.errorChan <- NewFaxError("Gateway not set", false)
		return
	}

	if _, err := os.Stat(t.faxjob.FileName); err != nil {
		t.logManager.SendLog(t.logManager.BuildLog(
			"EventClient",
			"Fax file not accessible: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"uuid":      t.faxjob.UUID.String(),
				"file_name": t.faxjob.FileName,
				"error":     err.Error(),
			},
			err,
		))
		t.errorChan <- NewFaxError(err.Error(), false)
		return
	}

	var err error
	t.conn, err = eventsocket.Dial(
		gofaxlib.Config.FreeSwitch.EventClientSocket,
		gofaxlib.Config.FreeSwitch.EventClientSocketPassword,
	)
	if err != nil {
		t.logManager.SendLog(t.logManager.BuildLog(
			"EventClient",
			"Failed to connect to FreeSWITCH event socket: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"uuid":      t.faxjob.UUID.String(),
				"socket":    gofaxlib.Config.FreeSwitch.EventClientSocket,
				"error":     err.Error(),
				"callee":    t.faxjob.CalleeNumber,
				"caller_id": t.faxjob.CallerIdNumber,
			},
			err,
		))
		t.errorChan <- NewFaxError(err.Error(), true)
		return
	}
	defer t.conn.Close()

	t.logManager.SendLog(t.logManager.BuildLog(
		"EventClient",
		"Connected to FreeSWITCH event socket",
		logrus.InfoLevel,
		map[string]interface{}{
			"uuid":   t.faxjob.UUID.String(),
			"socket": gofaxlib.Config.FreeSwitch.EventClientSocket,
		},
	))

	// Enable event filter and events
	if _, err = t.conn.Send(fmt.Sprintf("filter Unique-ID %v", t.faxjob.UUID)); err != nil {
		t.logManager.SendLog(t.logManager.BuildLog(
			"EventClient",
			"Failed to apply UUID filter: %v",
			logrus.ErrorLevel,
			map[string]interface{}{"uuid": t.faxjob.UUID.String(), "error": err.Error()},
			err,
		))
		t.errorChan <- NewFaxError(err.Error(), true)
		return
	}
	if _, err = t.conn.Send("event plain CHANNEL_CALLSTATE CUSTOM spandsp::txfaxnegociateresult spandsp::txfaxpageresult spandsp::txfaxresult"); err != nil {
		t.logManager.SendLog(t.logManager.BuildLog(
			"EventClient",
			"Failed to subscribe to events: %v",
			logrus.ErrorLevel,
			map[string]interface{}{"uuid": t.faxjob.UUID.String(), "error": err.Error()},
			err,
		))
		t.errorChan <- NewFaxError(err.Error(), true)
		return
	}

	// Check if T.38 should be enabled
	requestT38 := gofaxlib.Config.Faxing.RequestT38
	enableT38 := gofaxlib.Config.Faxing.EnableT38

	fallback, err := gofaxlib.GetSoftmodemFallback(t.conn, t.faxjob.CallerIdNumber)
	if err != nil {
		t.logManager.SendLog(t.logManager.BuildLog(
			"EventClient",
			"GetSoftmodemFallback error: %v",
			logrus.ErrorLevel,
			map[string]interface{}{
				"uuid":      t.faxjob.UUID.String(),
				"caller_id": t.faxjob.CallerIdNumber,
				"error":     err.Error(),
			},
			err,
		))
	}
	if fallback {
		t.logManager.SendLog(t.logManager.BuildLog(
			"EventClient",
			"Softmodem fallback already active for destination %s, disabling T.38",
			logrus.WarnLevel,
			map[string]interface{}{
				"uuid":          t.faxjob.UUID.String(),
				"callee_number": t.faxjob.CalleeNumber,
				"caller_id":     t.faxjob.CallerIdNumber,
			},
			t.faxjob.CalleeNumber,
		))
		enableT38 = false
		requestT38 = false
	}

	// Collect dialstring variables
	dsVariablesMap := map[string]string{
		"ignore_early_media":           "true",
		"origination_uuid":             t.faxjob.UUID.String(),
		"origination_caller_id_number": t.faxjob.CallerIdNumber,
		"origination_caller_id_name":   t.faxjob.CallerIdName,
		"fax_ident":                    t.faxjob.Identifier,
		"fax_header":                   t.faxjob.Header,
		"fax_use_ecm":                  strconv.FormatBool(t.faxjob.UseECM),
		"fax_disable_v17":              strconv.FormatBool(t.faxjob.DisableV17),
		"fax_enable_t38":               strconv.FormatBool(enableT38),
		"fax_enable_t38_request":       strconv.FormatBool(requestT38),
		"fax_verbose":                  strconv.FormatBool(gofaxlib.Config.FreeSwitch.Verbose),
	}

	// Look up variable overrides for given number
	overrideRealm := fmt.Sprintf("override-%s", t.faxjob.CalleeNumber)
	overrides, err := gofaxlib.FreeSwitchDBList(t.conn, overrideRealm)
	if err != nil {
		if strings.TrimSpace(err.Error()) != "no reply" {
			t.logManager.SendLog(t.logManager.BuildLog(
				"EventClient",
				"FreeSwitchDBList error: %v",
				logrus.ErrorLevel,
				map[string]interface{}{
					"uuid":          t.faxjob.UUID.String(),
					"overrideRealm": overrideRealm,
					"error":         err.Error(),
				},
				err,
			))
		}
	} else {
		for _, varName := range overrides {
			varValue, err := gofaxlib.FreeSwitchDBSelect(t.conn, overrideRealm, varName)
			if err != nil {
				if strings.TrimSpace(err.Error()) != "no reply" {
					t.logManager.SendLog(t.logManager.BuildLog(
						"EventClient",
						"FreeSwitchDBSelect error for %s: %v",
						logrus.ErrorLevel,
						map[string]interface{}{
							"uuid":          t.faxjob.UUID.String(),
							"overrideRealm": overrideRealm,
							"var_name":      varName,
							"error":         err.Error(),
						},
						varName, err,
					))
				}
			} else {
				t.logManager.SendLog(t.logManager.BuildLog(
					"EventClient",
					"Overriding dialstring variable %s=%s",
					logrus.InfoLevel,
					map[string]interface{}{
						"uuid":          t.faxjob.UUID.String(),
						"overrideRealm": overrideRealm,
						"var_name":      varName,
						"var_value":     varValue,
					},
					varName, varValue,
				))
				dsVariablesMap[varName] = varValue
			}
		}
	}

	// Assemble dialstring
	var dsVariables bytes.Buffer
	var gateways []string
	for _, i := range t.faxjob.Endpoints {
		gateways = append(gateways, strings.Split(i.Endpoint, ":")[0])
	}

	var dsGateways = endpointGatewayDialstring(gateways, t.faxjob.CalleeNumber)

	for k, v := range dsVariablesMap {
		if dsVariables.Len() > 0 {
			dsVariables.WriteByte(',')
		}
		dsVariables.WriteString(fmt.Sprintf("%v='%v'", k, v))
	}

	dialstring := fmt.Sprintf("{%v}%v", dsVariables.String(), dsGateways)

	t.logManager.SendLog(t.logManager.BuildLog(
		"EventClient",
		"FS_OUTBOUND - %s",
		logrus.InfoLevel,
		map[string]interface{}{
			"uuid":             t.faxjob.UUID.String(),
			"dialstring":       dialstring,
			"gateways":         gateways,
			"callee_number":    t.faxjob.CalleeNumber,
			"caller_id_number": t.faxjob.CallerIdNumber,
			"caller_id_name":   t.faxjob.CallerIdName,
			"use_ecm":          t.faxjob.UseECM,
			"disable_v17":      t.faxjob.DisableV17,
			"enable_t38":       enableT38,
			"request_t38":      requestT38,
		},
		dialstring,
	))

	// Originate call
	t.logManager.SendLog(t.logManager.BuildLog(
		"EventClient",
		"Originating channel to %s",
		logrus.InfoLevel,
		map[string]interface{}{
			"uuid":          t.faxjob.UUID.String(),
			"callee_number": t.faxjob.CalleeNumber,
		},
		t.faxjob.CalleeNumber,
	))
	_, err = t.conn.Send(fmt.Sprintf("api originate %v &txfax(%v)", dialstring, t.faxjob.FileName))
	if err != nil {
		t.conn.Send(fmt.Sprintf("uuid_dump %v", t.faxjob.UUID))
		hangupcause := strings.TrimSpace(err.Error())
		t.logManager.SendLog(t.logManager.BuildLog(
			"EventClient",
			"Originate failed with hangup cause %s",
			logrus.ErrorLevel,
			map[string]interface{}{
				"uuid":          t.faxjob.UUID.String(),
				"hangup_cause":  hangupcause,
				"callee_number": t.faxjob.CalleeNumber,
				"file_name":     t.faxjob.FileName,
			},
			hangupcause,
		))
		if gofaxlib.FailedHangUpCause(hangupcause) {
			t.errorChan <- NewFaxError(hangupcause, false)
		} else {
			t.errorChan <- NewFaxError(hangupcause, true)
		}
		return
	}
	t.logManager.SendLog(t.logManager.BuildLog(
		"EventClient",
		"Originate successful",
		logrus.InfoLevel,
		map[string]interface{}{
			"uuid":          t.faxjob.UUID.String(),
			"callee_number": t.faxjob.CalleeNumber,
		},
	))

	result := gofaxlib.NewFaxResult(t.faxjob.UUID, t.logManager, false)

	es := gofaxlib.NewEventStream(t.conn)
	var pages uint

	// Listen for system signals to be able to kill the channel
	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, syscall.SIGTERM, syscall.SIGINT)

	for {
		select {
		case ev := <-es.Events():
			result.AddEvent(ev)
			if result.HangupCause != "" {

				// If eventClient failed:
				// Check if softmodem fallback should be enabled on the next call
				if gofaxlib.Config.FreeSwitch.SoftmodemFallback && !result.Success {
					var activateFallback bool

					if result.NegotiateCount > 1 {
						t.logManager.SendLog(t.logManager.BuildLog(
							"EventClient",
							"Faxing failed with %d negotiations, enabling softmodem fallback for calls to %s",
							logrus.ErrorLevel,
							map[string]interface{}{
								"uuid":              t.faxjob.UUID.String(),
								"negotiate_count":   result.NegotiateCount,
								"callee_number":     t.faxjob.CalleeNumber,
								"hangup_cause":      result.HangupCause,
								"transferred_pages": result.TransferredPages,
								"success":           result.Success,
							},
							result.NegotiateCount, t.faxjob.CalleeNumber,
						))
						activateFallback = true
					} else {
						var badrows uint
						for _, p := range result.PageResults {
							badrows += p.BadRows
						}
						if badrows > 0 {
							t.logManager.SendLog(t.logManager.BuildLog(
								"EventClient",
								"Faxing failed with %d bad rows in %d pages, enabling softmodem fallback for calls to %s",
								logrus.ErrorLevel,
								map[string]interface{}{
									"uuid":          t.faxjob.UUID.String(),
									"bad_rows":      badrows,
									"pages":         result.TransferredPages,
									"callee_number": t.faxjob.CalleeNumber,
									"hangup_cause":  result.HangupCause,
									"success":       result.Success,
								},
								badrows, result.TransferredPages, t.faxjob.CalleeNumber,
							))
							activateFallback = true
						}
					}

					if activateFallback {
						err = gofaxlib.SetSoftmodemFallback(t.conn, t.faxjob.CalleeNumber, true)
						if err != nil {
							t.logManager.SendLog(t.logManager.BuildLog(
								"EventClient",
								"SetSoftmodemFallback error: %v",
								logrus.ErrorLevel,
								map[string]interface{}{
									"uuid":          t.faxjob.UUID.String(),
									"callee_number": t.faxjob.CalleeNumber,
									"error":         err.Error(),
								},
								err,
							))
						}
					}
				}

				t.resultChan <- result
				return
			}

			if ev.Get("Event-Subclass") == "spandsp::txfaxnegociateresult" {
				// Intermediate negotiation result
				t.resultChan <- result
			} else if result.TransferredPages != pages {
				pages = result.TransferredPages
				t.pageChan <- &result.PageResults[pages-1]
			}

		case err := <-es.Errors():
			t.logManager.SendLog(t.logManager.BuildLog(
				"EventClient",
				"Event stream error: %v",
				logrus.ErrorLevel,
				map[string]interface{}{
					"uuid":  t.faxjob.UUID.String(),
					"error": err.Error(),
				},
				err,
			))
			t.errorChan <- NewFaxError(err.Error(), true)
			return

		case kill := <-sigchan:
			t.logManager.SendLog(t.logManager.BuildLog(
				"EventClient",
				"event_client received signal %v, destroying FreeSWITCH channel %v",
				logrus.ErrorLevel,
				map[string]interface{}{
					"uuid":          t.faxjob.UUID.String(),
					"signal":        kill.String(),
					"channel_uuid":  t.faxjob.UUID.String(),
					"callee_number": t.faxjob.CalleeNumber,
				},
				kill, t.faxjob.UUID,
			))
			t.conn.Send(fmt.Sprintf("api uuid_kill %v", t.faxjob.UUID))
			t.errorChan <- NewFaxError(fmt.Sprintf("Killed by signal %v", kill), false)
			return
		}
	}
}
