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
	"fmt"
	"github.com/sirupsen/logrus"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gofaxserver/gofaxlib"

	"github.com/fiorix/go-eventsocket/eventsocket"
	"github.com/google/uuid"
)

const (
	tempFileFormat = "fax_%s.tiff"
	/*	recvqDir          = "recvq"
		defaultFaxrcvdCmd = "bin/faxrcvd"
		defaultDevice     = "freeswitch"*/
)

// EventSocketServer is a server for handling outgoing event socket connections from FreeSWITCH
type EventSocketServer struct {
	errorChan chan error
	killChan  chan struct{}
	server    *Server
}

// NewEventSocketServer initializes a EventSocketServer
func NewEventSocketServer(server *Server) *EventSocketServer {
	e := new(EventSocketServer)
	e.errorChan = make(chan error)
	e.killChan = make(chan struct{})
	e.server = server // this is used to point back to the main server struct to access the other methods and shit
	return e
}

func getNumberFromSIPURI(uri string) (string, error) {
	re := regexp.MustCompile(`sip:(\d+)@`)

	matches := re.FindStringSubmatch(uri)

	if len(matches) < 2 {
		return "", fmt.Errorf("number could not be extracted from SIP URI")
	}

	number := matches[1]
	return number, nil
}

// Start starts a goroutine to listen for ESL connections and handle incoming calls
func (e *EventSocketServer) Start() {
	go func() {
		err := eventsocket.ListenAndServe(gofaxlib.Config.FreeSwitch.EventServerSocket, e.handler)
		if err != nil {
			e.errorChan <- err
		}
	}()
}

// Errors returns a channel of fatal errors that make the server stop
func (e *EventSocketServer) Errors() <-chan error {
	return e.errorChan
}

// Kill aborts all running connections and kills the
// corresponding FreeSWITCH channels.
// TODO: Right now we have not way implemented to wait until
// all connections have closed and signal to the caller,
// so we have to wait a few seconds after calling Kill()
func (e *EventSocketServer) Kill() {
	close(e.killChan)
}

// Handle incoming call
func (e *EventSocketServer) handler(c *eventsocket.Connection) {
	const logNS = "FreeSwitch.EventServer"

	logf := func(level logrus.Level, msg string, fields map[string]interface{}, args ...interface{}) {
		if fields == nil {
			fields = map[string]interface{}{}
		}
		e.server.LogManager.SendLog(e.server.LogManager.BuildLog(logNS, msg, level, fields, args...))
	}

	send := func(cmd string) bool {
		if _, err := c.Send(cmd); err != nil {
			logf(logrus.ErrorLevel, "Send failed: %s", map[string]interface{}{"cmd": cmd}, err)
			return false
		}
		return true
	}

	exec := func(app, args string, sync bool) bool {
		if _, err := c.Execute(app, args, sync); err != nil {
			logf(logrus.ErrorLevel, "Execute failed: %s %s", map[string]interface{}{"app": app, "args": args}, err)
			return false
		}
		return true
	}

	// --- Connection / 'connect' handshake -----------------------------------
	logf(logrus.InfoLevel, "Incoming Event Socket connection from %s", map[string]interface{}{}, c.RemoteAddr())

	connectev, err := c.Send("connect")
	if err != nil {
		logf(logrus.ErrorLevel, "connect failed: %v", nil, err)
		_, err = c.Send("exit")
		return
	}

	channelUUID, err := uuid.Parse(connectev.Get("Unique-Id"))
	if err != nil {
		logf(logrus.ErrorLevel, "invalid Unique-Id: %v", nil, err)
		_, err = c.Send("exit")
		return
	}
	defer logf(logrus.InfoLevel, "Handler ending for %s", map[string]interface{}{"uuid": channelUUID.String()}, channelUUID.String())

	// --- Subscribe / filter events ------------------------------------------
	send("linger")
	send(fmt.Sprintf("filter Unique-ID %s", channelUUID.String()))
	send("event plain CHANNEL_CALLSTATE CUSTOM spandsp::rxfaxnegociateresult spandsp::rxfaxpageresult spandsp::rxfaxresult")

	// --- Extract caller/callee and context -----------------------------------
	var (
		recipient string
		cidName   = connectev.Get("Channel-Caller-Id-Name")
		cidNum    = connectev.Get("Channel-Caller-Id-Number")
		sourceIP  = connectev.Get("Variable_sip_network_ip")
	)

	if gofaxlib.Config.Faxing.RecipientFromDiversionHeader {
		recipient, err = getNumberFromSIPURI(connectev.Get("Variable_sip_h_diversion"))
		if err != nil || recipient == "" {
			logf(logrus.ErrorLevel, "failed to parse Diversion header: %v", map[string]interface{}{"uuid": channelUUID.String()}, err)
			exec("respond", "404", true)
			_, err = c.Send("exit")
			return
		}
	} else {
		recipient = connectev.Get("Variable_sip_to_user")
	}

	// Validate against FS gateway ACLs
	ep1, err := e.server.fsGatewayACL(sourceIP)
	if err != nil {
		logf(
			logrus.ErrorLevel,
			"invalid call (to: %s from: %s <%s>) failed ACLs - ip: %s",
			map[string]interface{}{"uuid": channelUUID.String()},
			recipient, cidNum, cidName, sourceIP,
		)
		exec("respond", "401", true)
		_, err = c.Send("exit")
		return
	}
	gateway := strings.Split(ep1, ":")[0]

	logf(
		logrus.InfoLevel,
		"Incoming call to %s from %s <%s> via gateway %s",
		map[string]interface{}{"uuid": channelUUID.String(), "ip": sourceIP},
		recipient, cidName, cidNum, gateway,
	)

	// Optional: Log initial channel UUID right away
	logf(logrus.DebugLevel, "Inbound channel UUID: %s", map[string]interface{}{"uuid": channelUUID.String()}, channelUUID.String())

	// --- T.38 intent / softmodem fallback screening --------------------------
	requestT38 := gofaxlib.Config.Faxing.RequestT38
	enableT38 := gofaxlib.Config.Faxing.EnableT38

	fallback, fbErr := gofaxlib.GetSoftmodemFallback(nil, cidNum)
	if fbErr != nil {
		logf(logrus.ErrorLevel, "fallback check error: %v", map[string]interface{}{"uuid": channelUUID.String()}, fbErr)
	}
	if fallback {
		logf(logrus.WarnLevel, "Softmodem fallback active for caller %s; disabling T.38", map[string]interface{}{"uuid": channelUUID.String()}, cidNum)
		enableT38 = false
		requestT38 = false
	}

	// --- Routing transforms ---------------------------------------------------
	srcNum := e.server.DialplanManager.ApplyTransformationRules(cidNum)
	dstNum := e.server.DialplanManager.ApplyTransformationRules(recipient)

	// Bridge decision
	bridgeGw, enableBridge := e.server.Router.detectAndRouteToBridge(dstNum, srcNum, gateway)
	logf(logrus.InfoLevel, "Bridge decision: enable=%t target=%s", map[string]interface{}{
		"uuid":       channelUUID.String(),
		"src_num":    srcNum,
		"dst_num":    dstNum,
		"gateway":    gateway,
		"requestT38": requestT38,
		"enableT38":  enableT38,
	}, enableBridge, bridgeGw)

	// --- Bridge mode path -----------------------------------------------------
	if enableBridge {
		logf(logrus.InfoLevel, "Bridge enabled for %s → %s via %s", map[string]interface{}{"uuid": channelUUID.String()}, srcNum, dstNum, gateway)
		exec("set", "origination_caller_id_number="+srcNum, true)

		if bridgeGw == "upstream" {
			// Leg B (upstream) gets T.38 gateway on answer; Leg A assumed G.711
			exportStr := fmt.Sprintf("{%s,%s,%s,%s}",
				fmt.Sprintf("fax_enable_t38=%t", enableT38),
				fmt.Sprintf("fax_enable_t38_request=%t", requestT38),
				"execute_on_answer=t38_gateway self",
				"absolute_codec_string=PCMU",
			)

			dsGateways := endpointGatewayDialstring(e.server.UpstreamFsGateways, dstNum)
			logf(logrus.InfoLevel, "FS_INBOUND → OUTBOUND BRIDGE %s", map[string]interface{}{"uuid": channelUUID.String()}, exportStr+dsGateways)
			exec("bridge", exportStr+dsGateways, true)

			/*	// A-leg exports – doc-style
				exec("export", fmt.Sprintf("nolocal:fax_enable_t38=%t", enableT38), true)
				exec("export", fmt.Sprintf("nolocal:fax_enable_t38_request=%t", requestT38), true)
				exec("export", "nolocal:execute_on_answer=t38_gateway self", true)
				exec("export", "nolocal:absolute_codec_string=PCMU", true)

				// B-leg plain dialstring (no {})
				dsGateways := endpointGatewayDialstring(e.server.UpstreamFsGateways, dstNum)
				logf(logrus.InfoLevel, "FS_INBOUND → OUTBOUND BRIDGE %s", map[string]interface{}{"uuid": channelUUID.String()}, dsGateways)
				exec("bridge", dsGateways, true)
			*/
		} else {
			// PBX side bridge
			logf(logrus.InfoLevel, "FS_INBOUND → INBOUND BRIDGE gateway=%s", map[string]interface{}{"uuid": channelUUID.String()}, bridgeGw)
			exec("set", "absolute_codec_string=PCMU", true)
			exec("set", fmt.Sprintf("fax_enable_t38=%t", enableT38), true)
			exec("set", fmt.Sprintf("fax_enable_t38_request=%t", requestT38), true)
			exec("answer", "", true)
			exec("t38_gateway", "self", true)
			exec("bridge", fmt.Sprintf("sofia/gateway/%s/%s", bridgeGw, dstNum), true)
		}
	}

	// --- Non-bridge (receive) path setup -------------------------------------
	filename := filepath.Join(gofaxlib.Config.Faxing.TempDir, fmt.Sprintf(tempFileFormat, channelUUID.String()))

	if !enableBridge {
		exec("set", fmt.Sprintf("fax_enable_t38=%t", enableT38), true)
		exec("set", fmt.Sprintf("fax_enable_t38_request=%t", requestT38), true)
		exec("set", "fax_disable_v17=true", true)

		logf(logrus.DebugLevel, "rxfax target file: %s", map[string]interface{}{"uuid": channelUUID.String()}, filename)

		if gofaxlib.Config.Faxing.AnswerAfter != 0 {
			exec("ring_ready", "", true)
			exec("sleep", strconv.FormatUint(gofaxlib.Config.Faxing.AnswerAfter, 10), true)
		}

		exec("answer", "", true)

		if gofaxlib.Config.Faxing.WaitTime != 0 {
			exec("playback", "silence_stream://"+strconv.FormatUint(gofaxlib.Config.Faxing.WaitTime, 10), true)
		}

		csi := gofaxlib.Config.FreeSwitch.Ident
		exec("set", fmt.Sprintf("fax_ident=%s", csi), true)
		exec("rxfax", filename, true)
		exec("hangup", "", true)
	}

	// --- Queue job routing / cleanup -----------------------------------------
	faxjob := &FaxJob{
		UUID:           channelUUID,
		CalleeNumber:   recipient,
		CallerIdNumber: cidNum,
		CallerIdName:   cidName,
		FileName:       filename,
		UseECM:         false, // default
		DisableV17:     false,
		SourceInfo: FaxSourceInfo{
			Timestamp:  time.Now(),
			SourceType: "gateway", // indicates FreeSWITCH source
			Source:     gateway,
			SourceID:   channelUUID.String(),
		},
	}
	e.server.FaxTracker.Begin(faxjob)

	// --- Result tracking & event loop ----------------------------------------
	result := gofaxlib.NewFaxResult(channelUUID, e.server.LogManager, enableBridge)
	es := gofaxlib.NewEventStream(c)
	pages := result.TransferredPages

EventLoop:
	for {
		select {
		case ev := <-es.Events():
			if ev.Get("Content-Type") == "text/disconnect-notice" {
				logf(logrus.WarnLevel, "Received disconnect message", map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge})
				// keep loop; result handler may still need to finish/stamp
			} else {
				result.AddEvent(ev)

				if result.HangupCause != "" {
					logf(logrus.DebugLevel, "Hangup cause observed: %s", map[string]interface{}{"uuid": channelUUID.String()}, result.HangupCause)
					c.Close()
					break EventLoop
				}

				// Page progress logging (throttled to change)
				if pages != result.TransferredPages {
					pages = result.TransferredPages
					logf(logrus.DebugLevel, "Transferred pages: %d", map[string]interface{}{"uuid": channelUUID.String()}, pages)
				}
			}

		case err := <-es.Errors():
			if err.Error() == "EOF" {
				logf(logrus.ErrorLevel, "Event socket client disconnected (EOF)", map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge})
			} else {
				logf(logrus.ErrorLevel, "Event stream error: %v", map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge}, err)
			}
			break EventLoop

		case <-e.killChan:
			logf(logrus.ErrorLevel, "Kill request received, destroying channel", map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge})
			_, err = c.Send(fmt.Sprintf("api uuid_kill %v", channelUUID))
			c.Close()
			return
		}
	}

	// --- Post-receive fallback heuristics ------------------------------------
	if gofaxlib.Config.FreeSwitch.SoftmodemFallback && !result.Success {
		var activateFallback bool

		if result.NegotiateCount > 1 {
			logf(logrus.InfoLevel, "Fax failed with %d negotiations; enabling softmodem fallback for %s.", map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge}, result.NegotiateCount, cidNum)
			activateFallback = true
		} else {
			var badrows uint
			for _, p := range result.PageResults {
				badrows += p.BadRows
			}
			if badrows > 0 {
				logf(logrus.InfoLevel, "Fax failed with %d bad rows across %d pages; enabling softmodem fallback for %s.", map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge}, badrows, result.TransferredPages, cidNum)
				activateFallback = true
			}
		}

		if activateFallback {
			if err := gofaxlib.SetSoftmodemFallback(nil, cidNum, true); err != nil {
				logf(logrus.ErrorLevel, "failed to set softmodem fallback: %v", map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge}, err)
			}
		}
	}

	faxjob.Result = result

	if enableBridge {
		logf(logrus.InfoLevel, "Ended bridge", map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge})

		e.server.FaxTracker.Complete(faxjob.UUID)
		return
	}

	// Non-bridge: deliver result + remove temp file
	level := logrus.InfoLevel
	if !result.Success {
		level = logrus.ErrorLevel
	}
	logf(level, "Success: %v, Hangup Cause: %v, Result: %v", map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge}, result.Success, result.HangupCause, result.ResultText)

	if !result.Success {
		e.server.Queue.QueueFaxResult <- QueueFaxResult{Job: faxjob}
		if err := os.Remove(filename); err != nil {
			logf(logrus.ErrorLevel, "failed to remove fax file", map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge, "file": filename})
			return
		}
	} else {
		e.server.FaxJobRouting <- faxjob
	}

	return
}
