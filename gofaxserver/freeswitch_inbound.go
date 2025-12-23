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
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

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

	// Bridge / transcoding tracking
	isBridge := false
	bridgeDirection := ""
	bridgeGateway := ""
	var bridgeStart time.Time
	var bridgeEnd time.Time

	// Track whether softmodem fallback matched either side
	var softmodemSrc bool
	var softmodemDst bool
	fallbackHit := false

	// --- Connection / 'connect' handshake -----------------------------------
	logf(logrus.InfoLevel, "Incoming Event Socket connection from %s", map[string]interface{}{}, c.RemoteAddr())

	connectev, err := c.Send("connect")
	if err != nil {
		logf(logrus.ErrorLevel, "connect failed: %v", nil, err)
		_, _ = c.Send("exit")
		return
	}

	channelUUID, err := uuid.Parse(connectev.Get("Unique-Id"))
	if err != nil {
		logf(logrus.ErrorLevel, "invalid Unique-Id: %v", nil, err)
		_, _ = c.Send("exit")
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
			_, _ = c.Send("exit")
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
		_, _ = c.Send("exit")
		return
	}
	gateway := strings.Split(ep1, ":")[0]

	logf(
		logrus.InfoLevel,
		"Incoming call to %s from %s <%s> via gateway %s",
		map[string]interface{}{"uuid": channelUUID.String(), "ip": sourceIP},
		recipient, cidName, cidNum, gateway,
	)

	// --- Routing transforms ---------------------------------------------------
	srcNum := e.server.DialplanManager.ApplyTransformationRules(cidNum)
	dstNum := e.server.DialplanManager.ApplyTransformationRules(recipient)

	// Optional: Log initial channel UUID right away
	logf(logrus.DebugLevel, "Inbound channel UUID: %s", map[string]interface{}{"uuid": channelUUID.String()}, channelUUID.String())

	// --- T.38 intent / per-pair policy baseline ------------------------------
	requestT38 := gofaxlib.Config.Faxing.RequestT38
	enableT38 := gofaxlib.Config.Faxing.EnableT38

	pairDecisionTime := time.Now()
	pairAllowT38 := true

	// --- Bridge decision ------------------------------------------------------
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
		pairAllowT38 = e.server.ShouldAllowT38ForPair(srcNum, dstNum, pairDecisionTime)
		if !pairAllowT38 {
			logf(logrus.InfoLevel,
				"Per-pair policy: disabling T.38 for %s → %s (flip-flop within TTL)",
				map[string]interface{}{
					"uuid":       channelUUID.String(),
					"src_num":    srcNum,
					"dst_num":    dstNum,
					"pair_ttl_s": T38PairTTL.Seconds(),
				},
				srcNum, dstNum,
			)
			enableT38 = false
			requestT38 = false
		} else {
			logf(logrus.InfoLevel,
				"Per-pair policy: allowing T.38 for %s → %s (first or flipped)",
				map[string]interface{}{
					"uuid":       channelUUID.String(),
					"src_num":    srcNum,
					"dst_num":    dstNum,
					"pair_ttl_s": T38PairTTL.Seconds(),
				},
				srcNum, dstNum,
			)
		}

		isBridge = true
		bridgeGateway = bridgeGw

		logf(logrus.InfoLevel, "Bridge enabled for %s → %s via %s", map[string]interface{}{"uuid": channelUUID.String()}, srcNum, dstNum, gateway)
		exec("set", "origination_caller_id_number="+srcNum, true)

		if bridgeGw == "upstream" {
			bridgeDirection = "upstream"
			// Internal / PBX -> Upstream (t.38)

			fallbackDst, fbErr := gofaxlib.GetSoftmodemFallback(nil, dstNum)
			if fbErr != nil {
				logf(logrus.ErrorLevel, "fallbackDst check error: %v", map[string]interface{}{"uuid": channelUUID.String()}, fbErr)
			}
			if fallbackDst {
				softmodemDst = true
				fallbackHit = true
				logf(logrus.WarnLevel, "Softmodem fallbackDst active for caller %s; disabling T.38", map[string]interface{}{"uuid": channelUUID.String()}, cidNum)
				enableT38 = false
				requestT38 = false
			}

			exportStr := fmt.Sprintf("{%s,%s,%s}",
				fmt.Sprintf("fax_enable_t38=%t", enableT38),
				fmt.Sprintf("fax_enable_t38_request=%t", requestT38),
				fmt.Sprintf("sip_execute_on_image='t38_gateway %s'", "self nocng"),
			)

			dsGateways := endpointGatewayDialstring(e.server.UpstreamFsGateways, dstNum)
			logf(logrus.InfoLevel, "FS_INBOUND → OUTBOUND BRIDGE %s", map[string]interface{}{"uuid": channelUUID.String()}, dsGateways)

			bridgeStart = time.Now()
			exec("bridge", exportStr+dsGateways, true)

		} else {
			bridgeDirection = "downstream"

			fallbackSrc, fbErr := gofaxlib.GetSoftmodemFallback(nil, srcNum)
			if fbErr != nil {
				logf(logrus.ErrorLevel, "fallbackSrc check error: %v", map[string]interface{}{"uuid": channelUUID.String()}, fbErr)
			}
			if fallbackSrc {
				softmodemSrc = true
				fallbackHit = true
				logf(logrus.WarnLevel, "Softmodem fallbackSrc active for caller %s; disabling T.38", map[string]interface{}{"uuid": channelUUID.String()}, cidNum)
				enableT38 = false
				requestT38 = false
			}

			// External -> PBX / Internal (t.38)
			logf(logrus.InfoLevel, "FS_INBOUND → INBOUND BRIDGE gateway=%s", map[string]interface{}{"uuid": channelUUID.String()}, bridgeGw)
			exec("set", fmt.Sprintf("fax_enable_t38=%t", enableT38), true)
			exec("set", fmt.Sprintf("fax_enable_t38_request=%t", requestT38), true)
			exec("set", fmt.Sprintf("sip_execute_on_image=t38_gateway %s", "self nocng"), true)
			bridgeStart = time.Now()
			exec("bridge", fmt.Sprintf("sofia/gateway/%s/%s", bridgeGw, dstNum), true)
		}
	}

	// Apply per-pair flip-flop policy for non-bridged calls as well.
	if !enableBridge {
		pairAllowT38 = e.server.ShouldAllowT38ForPair(srcNum, dstNum, pairDecisionTime)
		if !pairAllowT38 {
			logf(logrus.InfoLevel,
				"Per-pair policy (non-bridge): disabling T.38 for %s → %s (flip-flop within TTL)",
				map[string]interface{}{
					"uuid":       channelUUID.String(),
					"src_num":    srcNum,
					"dst_num":    dstNum,
					"pair_ttl_s": T38PairTTL.Seconds(),
				},
				srcNum, dstNum,
			)
			enableT38 = false
			requestT38 = false
		} else {
			logf(logrus.InfoLevel,
				"Per-pair policy (non-bridge): allowing T.38 for %s → %s (first or flipped)",
				map[string]interface{}{
					"uuid":       channelUUID.String(),
					"src_num":    srcNum,
					"dst_num":    dstNum,
					"pair_ttl_s": T38PairTTL.Seconds(),
				},
				srcNum, dstNum,
			)
		}
	}

	// --- Non-bridge (receive) path setup -------------------------------------
	filename := filepath.Join(gofaxlib.Config.Faxing.TempDir, fmt.Sprintf(tempFileFormat, channelUUID.String()))

	if !enableBridge {
		// calculate the fallback for both source / destination, use whichever matches
		fallbackDst, fbErr := gofaxlib.GetSoftmodemFallback(nil, dstNum)
		if fbErr != nil {
			logf(logrus.ErrorLevel, "fallbackDst check error: %v", map[string]interface{}{"uuid": channelUUID.String()}, fbErr)
		}

		fallbackSrc, fbErr := gofaxlib.GetSoftmodemFallback(nil, srcNum)
		if fbErr != nil {
			logf(logrus.ErrorLevel, "fallbackSrc check error: %v", map[string]interface{}{"uuid": channelUUID.String()}, fbErr)
		}

		var matchedField string
		var matchedNumber string

		switch {
		case fallbackSrc:
			matchedField = "caller"
			matchedNumber = srcNum
		case fallbackDst:
			matchedField = "called"
			matchedNumber = dstNum
		}

		// Apply fallback if either side matched
		if fallbackDst || fallbackSrc {
			fallbackHit = true
			logf(
				logrus.WarnLevel,
				"Softmodem fallback active; disabling T.38",
				map[string]interface{}{
					"uuid":           channelUUID.String(),
					"matched_field":  matchedField,
					"matched_number": matchedNumber,
				},
			)
			enableT38 = false
			requestT38 = false
		}

		exec("set", fmt.Sprintf("fax_enable_t38=%t", enableT38), true)
		exec("set", fmt.Sprintf("fax_enable_t38_request=%t", requestT38), true)
		// exec("set", "fax_disable_v17=true", true)

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
		// T.38 decision tracking
		UsedT38:           enableT38,
		SoftmodemFallback: fallbackHit,
	}

	if srcTenant, _ := e.server.getTenantByNumber(srcNum); srcTenant != nil {
		faxjob.SrcTenantID = srcTenant.ID
	}
	if dstTenant, _ := e.server.getTenantByNumber(dstNum); dstTenant != nil {
		faxjob.DstTenantID = dstTenant.ID
	}

	e.server.FaxTracker.Begin(faxjob)

	// Mark appropriate phase based on call type
	if enableBridge {
		e.server.FaxTracker.MarkBridging(faxjob.UUID, bridgeDirection, bridgeGateway)
	} else {
		e.server.FaxTracker.MarkReceiving(faxjob.UUID)
	}

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
			_, _ = c.Send(fmt.Sprintf("api uuid_kill %v", channelUUID))
			c.Close()
			return
		}
	}

	bridgeEnd = time.Now()

	// --- Optional post-receive fallback heuristics (currently disabled) ------
	/*
		if gofaxlib.Config.FreeSwitch.SoftmodemFallback && !result.Success {
			var activateFallback bool

			if result.NegotiateCount > 1 {
				logf(logrus.InfoLevel, "Fax failed with %d negotiations; enabling softmodem fallbackSrc for %s.",
					map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge},
					result.NegotiateCount, cidNum)
				activateFallback = true
			} else {
				var badrows uint
				for _, p := range result.PageResults {
					badrows += p.BadRows
				}
				if badrows > 0 {
					logf(logrus.InfoLevel, "Fax failed with %d bad rows across %d pages; enabling softmodem fallbackSrc for %s.",
						map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge},
						badrows, result.TransferredPages, cidNum)
					activateFallback = true
				}
			}

			if activateFallback {
				if err := gofaxlib.SetSoftmodemFallback(nil, cidNum, true); err != nil {
					logf(logrus.ErrorLevel, "failed to set softmodem fallbackSrc: %v",
						map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge}, err)
				}
			}
		}
	*/

	// --- Attach bridge metadata ----------------------------------------------
	if enableBridge && !bridgeStart.IsZero() && !bridgeEnd.IsZero() {
		faxjob.ConnTime = bridgeEnd.Sub(bridgeStart)
	}

	if enableBridge {
		faxjob.IsBridge = isBridge
		faxjob.BridgeDirection = bridgeDirection
		faxjob.BridgeGateway = bridgeGateway
		faxjob.BridgeStartTs = bridgeStart
		faxjob.BridgeEndTs = bridgeEnd
		faxjob.BridgeT38 = enableT38
		faxjob.SoftmodemSrc = softmodemSrc
		faxjob.SoftmodemDst = softmodemDst
	}

	faxjob.Result = result

	// --- Update per-pair T.38 flip-flop state --------------------------------
	if !enableBridge {
		now := time.Now()
		if !fallbackHit {
			e.server.UpdateT38PairState(srcNum, dstNum, enableT38, now)
		} else {
			logf(
				logrus.DebugLevel,
				"Skipping T.38 pair state update for non-bridge due to softmodem fallback",
				map[string]interface{}{
					"uuid":    channelUUID.String(),
					"src_num": srcNum,
					"dst_num": dstNum,
				},
			)
		}
	}

	if enableBridge {
		now := time.Now()
		if !bridgeEnd.IsZero() {
			now = bridgeEnd
		}

		if !fallbackHit {
			// Only let non-fallback bridged calls influence flip-flop state.
			e.server.UpdateT38PairState(srcNum, dstNum, faxjob.BridgeT38, now)
		} else {
			logf(
				logrus.DebugLevel,
				"Skipping T.38 pair state update due to softmodem fallback",
				map[string]interface{}{
					"uuid":    channelUUID.String(),
					"src_num": srcNum,
					"dst_num": dstNum,
				},
			)
		}

		logf(logrus.InfoLevel, "Ended bridge", map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge})

		e.server.Queue.QueueFaxResult <- QueueFaxResult{Job: faxjob}
		e.server.FaxTracker.Complete(faxjob.UUID)
		return
	}

	// --- Non-bridge: deliver result + remove temp file -----------------------
	level := logrus.InfoLevel

	if !result.Success {
		e.server.FaxTracker.Complete(faxjob.UUID)
		level = logrus.ErrorLevel

		e.server.Queue.QueueFaxResult <- QueueFaxResult{Job: faxjob}
		if err := os.Remove(filename); err != nil {
			logf(logrus.ErrorLevel, "failed to remove fax file", map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge, "file": filename})
		}
	} else {
		e.server.FaxJobRouting <- faxjob
	}

	logf(level, "Success: %v, Hangup Cause: %v, Result: %v",
		map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge},
		result.Success, result.HangupCause, result.ResultText)

	return
}
