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

package gofaxserver

import (
	"bytes"
	"errors"
	"fmt"
	"gofaxserver/gofaxlib"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fiorix/go-eventsocket/eventsocket"
	"github.com/sirupsen/logrus"
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
	t := newEventClient(faxjob, e.server.LogManager, e.server)
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
	server *Server

	pageChan   chan *gofaxlib.PageResult
	errorChan  chan FaxError
	resultChan chan *gofaxlib.FaxResult

	logManager *gofaxlib.LogManager
}

func newEventClient(faxjob *FaxJob, logManager *gofaxlib.LogManager, server *Server) *eventClient {
	t := &eventClient{
		faxjob:     faxjob,
		server:     server,
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

	// Resolve the fax policy for this call (Postgres-backed rules; replaces
	// the old mod_db softmodem fallback and decides T.38/ECM/V.17).
	srcNum := t.faxjob.CallerIdNumber
	dstNum := t.faxjob.CalleeNumber

	policy := DefaultFaxPolicy()
	if t.server != nil {
		policy = t.server.ResolveFaxPolicy(srcNum, dstNum, CallTypeSoftmodem)
	}
	enableT38 := policy.EnableT38
	requestT38 := policy.RequestT38
	fallbackHit := policy.T38ForcedOff

	if len(policy.AppliedRuleIDs) > 0 {
		t.logManager.SendLog(t.logManager.BuildLog(
			"EventClient",
			"Fax policy applied: rules=%v enable_t38=%t request_t38=%t softmodem_only=%t",
			logrus.InfoLevel,
			map[string]interface{}{
				"uuid":           t.faxjob.UUID.String(),
				"src_num":        srcNum,
				"dst_num":        dstNum,
				"rules":          policy.AppliedRuleIDs,
				"enable_t38":     enableT38,
				"request_t38":    requestT38,
				"softmodem_only": policy.SoftmodemOnly,
			},
			policy.AppliedRuleIDs, enableT38, requestT38, policy.SoftmodemOnly,
		))
	}

	// Retry-chain escalation: a previous attempt of this same job showed
	// T.38 negotiation trouble, so force T.38 off for this attempt.
	if t.faxjob.ForceT38Off && enableT38 {
		t.logManager.SendLog(t.logManager.BuildLog(
			"EventClient",
			"Retry-chain escalation: forcing T.38 off after previous negotiation failure",
			logrus.WarnLevel,
			map[string]interface{}{
				"uuid":    t.faxjob.UUID.String(),
				"src_num": srcNum,
				"dst_num": dstNum,
			},
		))
		enableT38 = false
		requestT38 = false
	}

	// Check if this is an upstream gateway call - T.38 is only supported for upstreams
	isUpstreamCall := false
	if t.server != nil && len(t.faxjob.Endpoints) > 0 {
		for _, ep := range t.faxjob.Endpoints {
			// Extract gateway name from endpoint (format: "gateway:ip" or just "gateway")
			gwName := strings.Split(ep.Endpoint, ":")[0]
			for _, upstream := range t.server.UpstreamFsGateways {
				if strings.EqualFold(gwName, upstream) {
					isUpstreamCall = true
					break
				}
			}
			if isUpstreamCall {
				break
			}
		}
	}

	// Hard constraint: non-upstream (tenant/local) gateways are G.711 only.
	if !isUpstreamCall {
		t.logManager.SendLog(t.logManager.BuildLog(
			"EventClient",
			"T.38 disabled for non-upstream endpoint (tenant/local gateway)",
			logrus.InfoLevel,
			map[string]interface{}{
				"uuid":    t.faxjob.UUID.String(),
				"src_num": srcNum,
				"dst_num": dstNum,
			},
		))
		enableT38 = false
		requestT38 = false
	} else if !policy.T38Decided && !t.faxjob.ForceT38Off && t.server != nil {
		// No rule decided T.38: flip-flop probing for this pair.
		pairAllowT38 := t.server.ShouldAllowT38ForPair(srcNum, dstNum, CallTypeSoftmodem, time.Now())
		if !pairAllowT38 {
			t.logManager.SendLog(t.logManager.BuildLog(
				"EventClient",
				"Per-pair policy: disabling T.38 for %s → %s (flip-flop within TTL)",
				logrus.InfoLevel,
				map[string]interface{}{
					"uuid":       t.faxjob.UUID.String(),
					"src_num":    srcNum,
					"dst_num":    dstNum,
					"pair_ttl_s": PairStateTTL().Seconds(),
				},
				srcNum, dstNum,
			))
			enableT38 = false
			requestT38 = false
		} else {
			t.logManager.SendLog(t.logManager.BuildLog(
				"EventClient",
				"Per-pair policy: allowing T.38 for %s → %s (first or flipped)",
				logrus.InfoLevel,
				map[string]interface{}{
					"uuid":       t.faxjob.UUID.String(),
					"src_num":    srcNum,
					"dst_num":    dstNum,
					"pair_ttl_s": PairStateTTL().Seconds(),
				},
				srcNum, dstNum,
			))
		}
	}

	// Apply ECM / V.17 policy decisions (nil = keep the job's settings).
	if policy.UseECM != nil {
		t.faxjob.UseECM = *policy.UseECM
	}
	if policy.DisableV17 != nil && *policy.DisableV17 {
		t.faxjob.DisableV17 = true
	}

	// Track T.38 decision on the faxjob for database persistence
	t.faxjob.UsedT38 = enableT38
	t.faxjob.SoftmodemFallback = fallbackHit
	t.faxjob.AppliedPolicyIDs = policy.AppliedRuleIDs

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

	// Apply channel-variable overrides from fax policy rules (replaces the
	// old mod_db "override-<number>" realm).
	for varName, varValue := range policy.VarOverrides {
		t.logManager.SendLog(t.logManager.BuildLog(
			"EventClient",
			"Overriding dialstring variable %s=%s (fax policy)",
			logrus.InfoLevel,
			map[string]interface{}{
				"uuid":      t.faxjob.UUID.String(),
				"var_name":  varName,
				"var_value": varValue,
			},
			varName, varValue,
		))
		dsVariablesMap[varName] = varValue
	}

	// Assemble dialstring
	var dsVariables bytes.Buffer
	var gateways []string
	for _, i := range t.faxjob.Endpoints {
		gateways = append(gateways, strings.Split(i.Endpoint, ":")[0])
	}

	var dsGateways = endpointGatewayDialstringOutboundTagged(gateways, t.faxjob.CalleeNumber)

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
			// Gateway attribution: the winning leg of a fan-out/failover
			// dialstring carries variable_gofax_gw (tagged per leg, see
			// endpointGatewayDialstringOutboundTagged). Record the first
			// non-empty value seen on the job.
			if t.faxjob.Gateway == "" {
				if gw := ev.Get("Variable_gofax_gw"); gw != "" {
					t.faxjob.Gateway = gw
					t.logManager.SendLog(t.logManager.BuildLog(
						"EventClient",
						"Outbound call routed via gateway %s",
						logrus.DebugLevel,
						map[string]interface{}{
							"uuid":    t.faxjob.UUID.String(),
							"gateway": gw,
						},
						gw,
					))
				}
			}
			if result.HangupCause != "" {

				// Feed the outcome into the fax policy engine (softmodem path
				// has full telemetry, so the auto-escalation ladder applies).
				if t.server != nil {
					if result.Success {
						t.server.LearnFaxPolicySuccess(dstNum)
					} else if qualifiesForLearning(result) {
						var badrows uint
						for _, p := range result.PageResults {
							badrows += p.BadRows
						}
						t.logManager.SendLog(t.logManager.BuildLog(
							"EventClient",
							"Faxing failed with qualifying signature (negotiations=%d bad_rows=%d t38_status=%s), learning policy for %s",
							logrus.WarnLevel,
							map[string]interface{}{
								"uuid":              t.faxjob.UUID.String(),
								"negotiate_count":   result.NegotiateCount,
								"bad_rows":          badrows,
								"t38_status":        result.T38Status,
								"callee_number":     t.faxjob.CalleeNumber,
								"hangup_cause":      result.HangupCause,
								"transferred_pages": result.TransferredPages,
							},
							result.NegotiateCount, badrows, result.T38Status, t.faxjob.CalleeNumber,
						))
						t.server.LearnFaxPolicyFailure(dstNum, result)
					}
				}

				// Update T.38 pair state for flip-flop (skip if a policy rule
				// forced T.38 or the call was non-upstream)
				if t.server != nil && !fallbackHit && isUpstreamCall {
					t.server.UpdateT38PairState(srcNum, dstNum, CallTypeSoftmodem, enableT38, time.Now())
					t.logManager.SendLog(t.logManager.BuildLog(
						"EventClient",
						"Updated T.38 pair state for %s → %s (used T.38: %t)",
						logrus.DebugLevel,
						map[string]interface{}{
							"uuid":     t.faxjob.UUID.String(),
							"src_num":  srcNum,
							"dst_num":  dstNum,
							"used_t38": enableT38,
						},
						srcNum, dstNum, enableT38,
					))
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
