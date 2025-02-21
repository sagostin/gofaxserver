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
	"fmt"
	"github.com/sirupsen/logrus"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gonicus/gofaxip/gofaxlib"

	"github.com/fiorix/go-eventsocket/eventsocket"
)

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

// SendFaxFS immediately tries to send the given qfile using FreeSWITCH
func SendFaxFS(faxjob FaxJob, deviceID string) (returned SendResult, err error) {
	returned = SendFailed

	/*var jobid uint
	if jobidstr := qf.GetString("jobid"); jobidstr != "" {
		if i, err := strconv.Atoi(jobidstr); err == nil {
			jobid = uint(i)
		}
	}

	if jobid == 0 {
		err = fmt.Errorf("Error parsing jobid")
		return
	}*/

	// Create FaxJob structure
	/*faxjob := NewFaxJob()*/
	faxjob.Number = fmt.Sprint(gofaxlib.Config.Gofaxsend.CallPrefix, qf.GetString("external"))
	faxjob.Cidnum = gofaxlib.Config.Gofaxsend.FaxNumber //qf.GetString("faxnumber")
	faxjob.Ident = gofaxlib.Config.Freeswitch.Ident
	faxjob.Header = gofaxlib.Config.Freeswitch.Header
	faxjob.Gateways = gofaxlib.Config.Freeswitch.Gateway

	if ecmMode, err := qf.GetInt("desiredec"); err == nil {
		faxjob.UseECM = ecmMode != 0
	}

	if brMode, err := qf.GetInt("desiredbr"); err == nil {
		if brMode < 5 { // < 14400bps
			faxjob.DisableV17 = true
		}
	}

	// Add TIFFs from queue file
	faxparts := qf.GetAll("fax")
	if len(faxparts) == 0 {
		err = fmt.Errorf("No fax file(s) found in qfile")
		return
	}
	faxfile := FaxFile{}
	for _, fileentry := range faxparts {
		err = faxfile.AddItem(fileentry)
		if err != nil {
			return
		}
	}

	// Merge TIFFs
	faxjob.Filename = filepath.Join(os.TempDir(), "gofaxsend_"+faxjob.UUID.String()+".tif")
	defer os.Remove(faxjob.Filename)
	if err = faxfile.WriteTo(faxjob.Filename); err != nil {
		return
	}

	// Start communication session and open logfile
	sessionlog, err := gofaxlib.NewSessionLogger(jobid)
	if err != nil {
		return
	}
	qf.Set("commid", sessionlog.CommID())
	sessionlog.Logf("Processing hylafax commid %s as freeswitch call %v", sessionlog.CommID(), faxjob.UUID)

	// Query DynamicConfig
	/*if dcCmd := gofaxlib.Config.Gofaxsend.DynamicConfig; dcCmd != "" {
		sessionlog.Log("Calling DynamicConfig script", dcCmd)
		dc, err := gofaxlib.DynamicConfig(dcCmd, deviceID, qf.GetString("owner"), qf.GetString("number"), fmt.Sprint(jobid))
		if err != nil {
			errmsg := fmt.Sprintln("Error calling DynamicConfig:", err)
			sessionlog.Log(errmsg)
			qf.Set("returned", strconv.Itoa(int(SendRetry)))
			qf.Set("status", errmsg)
			if err = qf.Write(); err != nil {
				sessionlog.Logf("Error updating qfile:", err)
			}
			// Retry, as this is an internal error executing the DynamicConfig script which could recover later
			return SendRetry, nil
		}

		// Check if call should be rejected
		if gofaxlib.DynamicConfigBool(dc.GetString("RejectCall")) {
			errmsg := "Transmission rejected by DynamicConfig"
			sessionlog.Log(errmsg)
			qf.Set("returned", strconv.Itoa(int(SendFailed)))
			qf.Set("status", errmsg)
			if err = qf.Write(); err != nil {
				sessionlog.Logf("Error updating qfile:", err)
			}
			return SendFailed, nil
		}

		// Check if a custom identifier should be set
		if dynamicTsi := dc.GetString("LocalIdentifier"); dynamicTsi != "" {
			faxjob.Ident = dynamicTsi
		}

		if tagline := dc.GetString("TagLine"); tagline != "" {
			faxjob.Header = tagline
		}

		if prefix := dc.GetString("CallPrefix"); prefix != "" {
			faxjob.Number = fmt.Sprint(prefix, qf.GetString("external"))
		}

		if faxnumber := dc.GetString("FAXNumber"); faxnumber != "" {
			faxjob.Cidnum = faxnumber
		}

		if gatewayString := dc.GetString("Gateway"); gatewayString != "" {
			faxjob.Gateways = strings.Split(gatewayString, ",")
		}

	}*/

	switch gofaxlib.Config.Gofaxsend.CidName {
	case "sender":
		faxjob.Cidname = qf.GetString("sender")
	case "number":
		faxjob.Cidname = qf.GetString("number")
	case "cidnum":
		faxjob.Cidname = faxjob.Cidnum
	default:
		faxjob.Cidname = gofaxlib.Config.FreeSwitch.CidName
	}

	// Total attempted calls
	totdials, _ := qf.GetInt("totdials")
	// Consecutive failed attempts to place a call
	ndials, _ := qf.GetInt("ndials")
	// Total answered calls
	tottries, _ := qf.GetInt("tottries")

	//Auto fallback to slow baudrate after to many tries
	v17retry, err := strconv.Atoi(gofaxlib.Config.Faxing.DisableV17AfterRetry)
	if err != nil {
		v17retry = 0
	}
	if v17retry > 0 && tottries >= v17retry {
		faxjob.DisableV17 = true
	}

	//Auto disable ECM after to many tries
	ecmretry, err := strconv.Atoi(gofaxlib.Config.Faxing.DisableECMAfterRetry)
	if err != nil {
		ecmretry = 0
	}
	if ecmretry > 0 && tottries >= ecmretry {
		faxjob.UseECM = false
	}

	// Update status
	qf.Set("status", "Dialing")
	totdials++
	qf.Set("totdials", strconv.Itoa(totdials))
	if err = qf.Write(); err != nil {
		sessionlog.Log("Error updating qfile:", err)
		return SendFailed, nil
	}
	// Default: Retry when eventClient fails
	returned = SendRetry

	// Start eventClient goroutine
	transmitTs := time.Now()
	t := newEventClient(faxjob, sessionlog)
	var result *gofaxlib.FaxResult
	var status string

	// Wait for events
StatusLoop:
	for {
		select {
		case page := <-t.PageSent():
			qf.Set("npages", strconv.Itoa(int(page.Page)))
			qf.Set("dataformat", page.EncodingName)
			if err = qf.Write(); err != nil {
				sessionlog.Log("Error updating qfile:", err)
			}

		case result = <-t.Result():
			qf.Set("signalrate", strconv.Itoa(int(result.TransferRate)))
			qf.Set("csi", result.RemoteID)

			// Break if call is hung up
			if result.Hangupcause != "" {
				// Faxing Finished
				status = result.ResultText
				if result.Success {
					returned = SendDone
				}
				break StatusLoop
			}

			// Negotiation finished
			negstatus := fmt.Sprint("Sending ", result.TransferRate)
			if result.Ecm {
				negstatus = negstatus + "/ECM"
			}
			status = negstatus
			tottries++
			ndials = 0
			qf.Set("status", status)
			qf.Set("tottries", strconv.Itoa(tottries))
			qf.Set("ndials", strconv.Itoa(ndials))
			if err = qf.Write(); err != nil {
				sessionlog.Log("Error updating qfile:", err)
			}

		case faxerr := <-t.Errors():
			ndials++
			qf.Set("ndials", strconv.Itoa(ndials))
			status = faxerr.Error()
			if faxerr.Retry() {
				returned = SendRetry
			} else {
				returned = SendFailed
			}
			break StatusLoop
		}
	}

	qf.Set("status", status)
	qf.Set("returned", strconv.Itoa(int(returned)))
	if err = qf.Write(); err != nil {
		sessionlog.Log("Error updating qfile:", err)
	}

	xfl := &gofaxlib.XFRecord{}
	xfl.Commid = sessionlog.CommID()
	xfl.Modem = deviceID
	xfl.Jobid = uint(jobid)
	xfl.Jobtag = qf.GetString("jobtag")
	xfl.Sender = qf.GetString("mailaddr")
	xfl.Destnum = qf.GetString("number")
	xfl.Owner = qf.GetString("owner")

	if result != nil {
		if result.Success {
			sessionlog.Logf("Faxing sent successfully. Hangup Cause: %v. Result: %v", result.Hangupcause, status)
		} else {
			sessionlog.Logf("Faxing failed. Retry: %v. Hangup Cause: %v. Result: %v", returned == SendRetry, result.Hangupcause, status)
		}
		xfl.SetResult(result)
	} else {
		sessionlog.Logf("Call failed. Retry: %v. Result: %v", returned == SendRetry, status)
		xfl.Reason = status
		xfl.Ts = transmitTs
		xfl.Jobtime = time.Now().Sub(transmitTs)
	}

	if err = xfl.SaveTransmissionReport(); err != nil {
		sessionlog.Log(err)
	}

	return returned, nil
}

type eventClient struct {
	faxjob FaxJob
	conn   *eventsocket.Connection

	pageChan   chan *gofaxlib.PageResult
	errorChan  chan FaxError
	resultChan chan *gofaxlib.FaxResult

	logManager *gofaxlib.LogManager
}

func newEventClient(faxjob FaxJob, logManager *gofaxlib.LogManager) *eventClient {
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
func (t *eventClient) start() {

	if t.faxjob.Number == "" {
		t.errorChan <- NewFaxError("Number to dial is empty", false)
		return
	}

	if len(t.faxjob.Gateways) == 0 {
		t.errorChan <- NewFaxError("Gateway not set", false)
		return
	}

	if _, err := os.Stat(t.faxjob.Filename); err != nil {
		t.errorChan <- NewFaxError(err.Error(), false)
		return
	}

	var err error
	t.conn, err = eventsocket.Dial(gofaxlib.Config.FreeSwitch.EventClientSocket, gofaxlib.Config.FreeSwitch.EventClientSocketPassword)
	if err != nil {
		t.errorChan <- NewFaxError(err.Error(), true)
		return
	}
	defer t.conn.Close()

	// Enable event filter and events
	_, err = t.conn.Send(fmt.Sprintf("filter Unique-ID %v", t.faxjob.UUID))
	if err != nil {
		t.errorChan <- NewFaxError(err.Error(), true)
		return
	}
	_, err = t.conn.Send("event plain CHANNEL_CALLSTATE CUSTOM spandsp::txfaxnegociateresult spandsp::txfaxpageresult spandsp::txfaxresult")
	if err != nil {
		t.errorChan <- NewFaxError(err.Error(), true)
		return
	}

	// Check if T.38 should be enabled
	requestT38 := gofaxlib.Config.Faxing.RequestT38
	enableT38 := gofaxlib.Config.Faxing.EnableT38

	fallback, err := gofaxlib.GetSoftmodemFallback(t.conn, t.faxjob.Number)
	if err != nil {
		t.logManager.SendLog(t.logManager.BuildLog(
			"EventClient",
			err.Error(),
			logrus.ErrorLevel,
			map[string]interface{}{"uuid": t.faxjob.UUID.String()},
		))
	}
	if fallback {
		t.logManager.SendLog(t.logManager.BuildLog(
			"EventClient",
			"Softmodem fallback active for destination %s, disabling T.38",
			logrus.ErrorLevel,
			map[string]interface{}{"uuid": t.faxjob.UUID.String()}, t.faxjob.Number,
		))
		enableT38 = false
		requestT38 = false
	}

	// Collect dialstring variables
	dsVariablesMap := map[string]string{
		"ignore_early_media":           "true",
		"origination_uuid":             t.faxjob.UUID.String(),
		"origination_caller_id_number": t.faxjob.Cidnum,
		"origination_caller_id_name":   t.faxjob.Cidname,
		"fax_ident":                    t.faxjob.Ident,
		"fax_header":                   t.faxjob.Header,
		"fax_use_ecm":                  strconv.FormatBool(t.faxjob.UseECM),
		"fax_disable_v17":              strconv.FormatBool(t.faxjob.DisableV17),
		"fax_enable_t38":               strconv.FormatBool(enableT38),
		"fax_enable_t38_request":       strconv.FormatBool(requestT38),
		"fax_verbose":                  strconv.FormatBool(gofaxlib.Config.FreeSwitch.Verbose),
	}

	// Look up variable overrides for given number
	overrideRealm := fmt.Sprintf("override-%s", t.faxjob.Number)
	overrides, err := gofaxlib.FreeSwitchDBList(t.conn, overrideRealm)
	if err != nil {
		if strings.TrimSpace(err.Error()) != "no reply" {
			t.logManager.SendLog(t.logManager.BuildLog(
				"EventClient",
				err.Error(),
				logrus.ErrorLevel,
				map[string]interface{}{"uuid": t.faxjob.UUID.String()},
			))
		}
	} else {
		for _, varName := range overrides {
			varValue, err := gofaxlib.FreeSwitchDBSelect(t.conn, overrideRealm, varName)
			if err != nil {
				if strings.TrimSpace(err.Error()) != "no reply" {
					t.logManager.SendLog(t.logManager.BuildLog(
						"EventClient",
						err.Error(),
						logrus.ErrorLevel,
						map[string]interface{}{"uuid": t.faxjob.UUID.String()},
					))
				}
			} else {
				t.logManager.SendLog(t.logManager.BuildLog(
					"EventClient",
					"Overriding dialstring variable %s=%s",
					logrus.ErrorLevel,
					map[string]interface{}{"uuid": t.faxjob.UUID.String()}, varName, varValue,
				))
				dsVariablesMap[varName] = varValue
			}
		}
	}

	// Assemble dialstring
	var dsVariables bytes.Buffer
	var dsGateways bytes.Buffer

	for k, v := range dsVariablesMap {
		if dsVariables.Len() > 0 {
			dsVariables.WriteByte(',')
		}
		dsVariables.WriteString(fmt.Sprintf("%v='%v'", k, v))
	}

	// Try gateways in configured order
	for _, gw := range t.faxjob.Gateways {
		if dsGateways.Len() > 0 {
			dsGateways.WriteByte('|')
		}
		dsGateways.WriteString(fmt.Sprintf("sofia/gateway/%v/%v", gw, t.faxjob.Number))
	}

	dialstring := fmt.Sprintf("{%v}%v", dsVariables.String(), dsGateways.String())
	t.logManager.SendLog(t.logManager.BuildLog(
		"EventClient",
		"Dialstring: %s",
		logrus.ErrorLevel,
		map[string]interface{}{"uuid": t.faxjob.UUID.String()}, dialstring,
	))

	// Originate call
	t.logManager.SendLog(t.logManager.BuildLog(
		"EventClient",
		"Originating channel to"+t.faxjob.Number+"using gateway"+strings.Join(t.faxjob.Gateways, ","),
		logrus.ErrorLevel,
		map[string]interface{}{"uuid": t.faxjob.UUID.String()},
	))
	_, err = t.conn.Send(fmt.Sprintf("api originate %v, &txfax(%v)", dialstring, t.faxjob.Filename))
	if err != nil {
		t.conn.Send(fmt.Sprintf("uuid_dump %v", t.faxjob.UUID))
		hangupcause := strings.TrimSpace(err.Error())
		t.logManager.SendLog(t.logManager.BuildLog(
			"EventClient",
			"Originate failed with hangup cause"+hangupcause,
			logrus.ErrorLevel,
			map[string]interface{}{"uuid": t.faxjob.UUID.String()},
		))
		if gofaxlib.FailedHangUpCause(hangupcause) {
			t.errorChan <- NewFaxError(hangupcause+" (retry disabled)", false)
		} else {
			t.errorChan <- NewFaxError(hangupcause, true)
		}
		return
	}
	t.logManager.SendLog(t.logManager.BuildLog(
		"EventClient",
		"Originate successful",
		logrus.ErrorLevel,
		map[string]interface{}{"uuid": t.faxjob.UUID.String()},
	))

	result := gofaxlib.NewFaxResult(t.faxjob.UUID, t.logManager)

	es := gofaxlib.NewEventStream(t.conn)
	var pages uint

	// Listen for system signals to be able to kill the channel
	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, syscall.SIGTERM, syscall.SIGINT)

	for {
		select {
		case ev := <-es.Events():
			result.AddEvent(ev)
			if result.Hangupcause != "" {

				// If eventClient failed:
				// Check if softmodem fallback should be enabled on the next call
				if gofaxlib.Config.FreeSwitch.SoftmodemFallback && !result.Success {
					var activateFallback bool

					if result.NegotiateCount > 1 {
						// Activate fallback if negotiation was repeated
						t.logManager.SendLog(t.logManager.BuildLog(
							"EventClient",
							"Faxing failed with %d negotiations, enabling softmodem fallback for calls from/to %s.",
							logrus.ErrorLevel,
							map[string]interface{}{"uuid": t.faxjob.UUID.String()}, result.NegotiateCount, t.faxjob.Number,
						))
						activateFallback = true
					} else {
						var badrows uint
						for _, p := range result.PageResults {
							badrows += p.BadRows
						}
						if badrows > 0 {
							// Activate fallback if any bad rows were present
							t.logManager.SendLog(t.logManager.BuildLog(
								"EventClient",
								"Faxing failed with %d bad rows in %d pages, enabling softmodem fallback for calls from/to %s.",
								logrus.ErrorLevel,
								map[string]interface{}{"uuid": t.faxjob.UUID.String()}, badrows, result.TransferredPages, t.faxjob.Number,
							))
							activateFallback = true
						}
					}

					if activateFallback {
						err = gofaxlib.SetSoftmodemFallback(t.conn, t.faxjob.Number, true)
						if err != nil {
							t.logManager.SendLog(t.logManager.BuildLog(
								"EventClient",
								err.Error(),
								logrus.ErrorLevel,
								map[string]interface{}{"uuid": t.faxjob.UUID.String()},
							))
						}
					}

				}

				t.resultChan <- result
				return
			}
			if ev.Get("Event-Subclass") == "spandsp::txfaxnegociateresult" {
				t.resultChan <- result
			} else if result.TransferredPages != pages {
				pages = result.TransferredPages
				t.pageChan <- &result.PageResults[pages-1]
			}
		case err := <-es.Errors():
			t.errorChan <- NewFaxError(err.Error(), true)
			return
		case kill := <-sigchan:
			t.logManager.SendLog(t.logManager.BuildLog(
				"EventClient",
				"event_client received signal %v, destroying freeswitch channel %v",
				logrus.ErrorLevel,
				map[string]interface{}{"uuid": t.faxjob.UUID.String()}, kill, t.faxjob.UUID,
			))
			t.conn.Send(fmt.Sprintf("api uuid_kill %v", t.faxjob.UUID))
			t.errorChan <- NewFaxError(fmt.Sprintf("Killed by signal %v", kill), false)
		}
	}

}
