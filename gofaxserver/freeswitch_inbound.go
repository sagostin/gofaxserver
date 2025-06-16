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
	fmt.Println("Incoming Event Socket connection from", c.RemoteAddr())

	connectev, err := c.Send("connect") // Returns a whole event
	if err != nil {
		c.Send("exit")
		fmt.Print(err)
		return
	}

	channelUUID, err := uuid.Parse(connectev.Get("Unique-Id"))
	if err != nil {
		c.Send("exit")
		fmt.Print(err)
		return
	}
	defer fmt.Println(channelUUID, "Handler ending")

	// Filter and subscribe to events
	c.Send("linger")
	c.Send(fmt.Sprintf("filter Unique-ID %v", channelUUID))
	c.Send("event plain CHANNEL_CALLSTATE CUSTOM spandsp::rxfaxnegociateresult spandsp::rxfaxpageresult spandsp::rxfaxresult")

	// Extract Caller/Callee
	var recipient string
	if gofaxlib.Config.Faxing.RecipientFromDiversionHeader {
		recipient, err = getNumberFromSIPURI(connectev.Get("Variable_sip_h_diversion"))
		if err != nil {
			fmt.Println(err)
			c.Execute("respond", "404", true)
			c.Send("exit")
			return
		}
	} else {
		recipient = connectev.Get("Variable_sip_to_user")
	}

	// gateway := connectev.Get("Variable_sip_gateway")
	// this shit doesn't work, so we need
	// another way of determining the endpoints...
	// we can store "allowed" IPs through the endpoints,
	// and handle it that way
	// for the "default" endpoints, we will need to store them in the DB with some random Tenant/Number ID?
	//

	cidname := connectev.Get("Channel-Caller-Id-Name")
	cidnum := connectev.Get("Channel-Caller-Id-Number")
	sourceip := connectev.Get("Variable_sip_network_ip")

	ep1, err := e.server.fsGatewayACL(sourceip)
	if err != nil {
		e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
			"EventServer",
			"invalid call (to: %s from: %s <%s>) failed to match ACLs - ip: %s",
			logrus.ErrorLevel,
			map[string]interface{}{"uuid": channelUUID.String()}, recipient, cidnum, cidname, sourceip,
		))
		c.Execute("respond", "401", true)
		c.Send("exit")
		return
	}

	var gateway = strings.Split(ep1, ":")[0]

	e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
		"EventServer",
		"Incoming call to %v from %v <%v> via gateway %v",
		logrus.InfoLevel,
		map[string]interface{}{"uuid": channelUUID.String()}, recipient, cidname, cidnum, gateway,
	))

	// todo match inbound gateway source and destination number, if its from a trunk
	// and the destination is a number with an endpoint with bridge mode,
	// allow transcode to audio to leg B (PBX) from Trunk
	// otherwise, if it's from PBX and going to external, transcode Leg B to Leg A if applicable
	// this doesn't need to be handled for outbound because inbound from trunk / pbx will always be "inbound"

	/*var device *Device
	if gofaxlib.Config.Faxing.AllocateInboundDevices {
		// Find free device
		device, err := devmanager.FindDevice(fmt.Sprintf("Receiving facsimile"))
		if err != nil {
			fmt.Println(err)
			c.Execute("respond", "404", true)
			c.Send("exit")
			return
		}
		defer device.SetReady()
	}*/

	/*var usedDevice string
	if device != nil {
		usedDevice = device.Name
	} else {
		usedDevice = defaultDevice
	}*/

	csi := gofaxlib.Config.FreeSwitch.Ident

	// todo pre router, we need to check if the number is in the database??!??!? or should we just block based on outbound

	// Query DynamicConfig
	/*if dcCmd := gofaxlib.Config.Faxing.DynamicConfig; dcCmd != "" {
		fmt.Println("Calling DynamicConfig script", dcCmd)
		dc, err := gofaxlib.DynamicConfig(dcCmd, usedDevice, cidnum, cidname, recipient, gateway)
		if err != nil {
			fmt.Println("Error calling DynamicConfig:", err)
		} else {
			// Check if call should be rejected
			if gofaxlib.DynamicConfigBool(dc.GetString("RejectCall")) {
				fmt.Println("DynamicConfig decided to reject this call")
				c.Execute("respond", "404", true)
				c.Send("exit")
				return
			}

			// Check if a custom identifier should be set
			if dynamicCsi := dc.GetString("LocalIdentifier"); dynamicCsi != "" {
				csi = dynamicCsi
			}

		}
	}*/

	/*logManager, err := gofaxlib.NewSessionLogger(0)
	if err != nil {
		c.Send("exit")
		fmt.Print(err)
		return
	}*/

	e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
		"EventServer",
		"Inbound channel UUID: %s",
		logrus.InfoLevel,
		map[string]interface{}{"uuid": channelUUID.String()}, channelUUID.String(),
	))

	// Check if T.38 should be enabled
	requestT38 := gofaxlib.Config.Faxing.RequestT38
	enableT38 := gofaxlib.Config.Faxing.EnableT38

	fallback, err := gofaxlib.GetSoftmodemFallback(nil, cidnum)
	if err != nil {
		e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
			"EventServer",
			err.Error(),
			logrus.ErrorLevel,
			map[string]interface{}{"uuid": channelUUID.String()},
		))
	}
	if fallback {
		e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
			"EventServer",
			"Softmodem fallback active for caller %s, disabling T.38",
			logrus.WarnLevel,
			map[string]interface{}{"uuid": channelUUID.String()}, cidnum,
		))
		enableT38 = false
		requestT38 = false
	}

	/*if gateway == "" {
		fmt.Println("invalid gateway, rejecting call")
		c.Execute("respond", "404", true)
		c.Send("exit")
		return
	}*/

	e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
		"FreeSwitch.EventServer",
		"Accepting call to %v from %v <%v> via gateway %v with commid %v",
		logrus.InfoLevel,
		map[string]interface{}{"uuid": channelUUID.String()}, recipient, cidname, cidnum, gateway, channelUUID.String(),
	))

	c.Execute("set", fmt.Sprintf("fax_enable_t38=%s", strconv.FormatBool(enableT38)), true)
	c.Execute("set", fmt.Sprintf("fax_enable_t38_request=%s", strconv.FormatBool(requestT38)), true)

	srcNum := e.server.DialplanManager.ApplyTransformationRules(cidnum)
	dstNum := e.server.DialplanManager.ApplyTransformationRules(recipient)

	// handle bridging
	bridgeGw, enableBridge := e.server.Router.detectAndRouteToBridge(dstNum, srcNum, gateway)

	if enableBridge {
		e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
			"FreeSwitch.EventServer.BRIDGE",
			"Enabling bridge mode for call to %v from %v <%v> via gateway %v with commid %v",
			logrus.InfoLevel,
			map[string]interface{}{"uuid": channelUUID.String()}, recipient, cidname, cidnum, gateway, channelUUID.String(),
		))

		c.Execute("set", "origination_caller_id_number="+srcNum, true)

		// we will assume that if the source is not an upstream gateway,
		// that we will enable transcoding from Leg A (g711) to Leg B (g711/t38)
		if bridgeGw == "upstream" {

			var dsGateways = endpointGatewayDialstring(e.server.UpstreamFsGateways, dstNum)
			e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
				"FREESWITCH.BRIDGE",
				"DialString %s",
				logrus.InfoLevel,
				map[string]interface{}{"uuid": channelUUID.String()}, dsGateways,
			))

			c.Execute("bridge", "{sip_execute_on_image=t38_gateway peer nocng}"+dsGateways, true) // nocng
			//c.Execute("bridge", fmt.Sprintf("sofia/gateway/%v/%v", "telcobridges2", dstNum), true)
		} else {
			c.Execute("set", "sip_execute_on_image=t38_gateway self nocng", true)
			c.Execute("bridge", "{refuse_t38=true}"+fmt.Sprintf("sofia/gateway/%v/%v", bridgeGw, dstNum), true)
			/*

				<extension name="t38_transcode">
				  <condition field="destination_number" expression="^(fax_transcode)$">
				    <action application="set" data="fax_enable_t38=true"/>
				    <action application="bridge" data="{sip_execute_on_image='t38_gateway self nocng'}sofia/internal/ext@host"/> <!-- "nocng" means don't detect the CNG tones. Just start transcoding -->
				  </condition>
				</extension>

			*/
		}
	}

	/*if device != nil {
		// Notify faxq
		// todo this is used for hylafax only, we will not use this going forward
		gofaxlib.Faxq.ModemStatus(device.Name, "I"+logManager.CommID())
		gofaxlib.Faxq.ReceiveStatus(device.Name, "B")
		gofaxlib.Faxq.ReceiveStatus(device.Name, "S")
		defer gofaxlib.Faxq.ReceiveStatus(device.Name, "E")
	}*/

	// Start interacting with the caller

	// Find filename in recvq to save received .tif
	/*seq, err := gofaxlib.GetSeqFor(recvqDir)
	if err != nil {
		c.Send("exit")
		logManager.Log(err)
		return
	}*/
	// todo can we require to a database instead or just in memory and pass to a channel?
	filename := filepath.Join(gofaxlib.Config.Faxing.TempDir, fmt.Sprintf(tempFileFormat, channelUUID.String()))

	/*
		todo for inbound calls from carrier if they send t.38, we will support, but for outbound,
		we should relay it in a sense. We will need to check the gateway and if it is a t.38 gateway?

			<extension name="t38_reinvite">
			  <condition>
			    <action application="set" data="fax_enable_t38=true"/> <!-- Enable t.38 for this call -->
			    <action application="set" data="fax_enable_t38_request=true"/> <!-- Enable t38_gateway to send a t.38 reinvite when a fax tone is detected. If using t38_gateway peer then you need to export this variable instead of set -->
			    <action application="set" data="execute_on_answer=t38_gateway self"/> <!--Execute t38_gateway on answer. self or peer. self: send a reinvite back to the a-leg. peer reinvite forward to the b-leg -->
			    <action application="bridge" data="sofia/external/1234@host"/>
			  </condition>
			</extension>
	*/

	// todo support bridging the call to another extension / gateway
	// check priority of the endpoints, and if the gateway is one, then attempt to transcode it

	// todo
	// we need to save the file temporarily
	// and then add it to the database or
	// something because freeswitch needs
	// to do it like that, hmm...
	if !enableBridge {
		e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
			"FreeSwitch.EventServer",
			"Rxfax to %s",
			logrus.DebugLevel,
			map[string]interface{}{"uuid": channelUUID.String()}, filename,
		))

		if gofaxlib.Config.Faxing.AnswerAfter != 0 {
			c.Execute("ring_ready", "", true)
			c.Execute("sleep", strconv.FormatUint(gofaxlib.Config.Faxing.AnswerAfter, 10), true)
		}

		c.Execute("answer", "", true)

		if gofaxlib.Config.Faxing.WaitTime != 0 {
			c.Execute("playback", "silence_stream://"+strconv.FormatUint(gofaxlib.Config.Faxing.WaitTime, 10), true)
		}

		// todo do i need the fax_ident?
		c.Execute("set", fmt.Sprintf("fax_ident=%s", csi), true)
		c.Execute("rxfax", filename, true)
		c.Execute("hangup", "", true)
	}

	result := gofaxlib.NewFaxResult(channelUUID, e.server.LogManager, enableBridge)
	es := gofaxlib.NewEventStream(c)

	pages := result.TransferredPages

	// todo how does this work???
EventLoop:
	for {
		select {
		case ev := <-es.Events():
			if ev.Get("Content-Type") == "text/disconnect-notice" {
				e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
					"FreeSwitch.EventServer",
					"Received disconnect message",
					logrus.WarnLevel,
					map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge},
				))
				//c.Close()
				//break EventLoop
			} else {
				result.AddEvent(ev)
				if result.HangupCause != "" {
					c.Close()
					break EventLoop
				}

				if pages != result.TransferredPages {
					pages = result.TransferredPages
					/*if device != nil {
						gofaxlib.Faxq.ReceiveStatus(device.Name, "P")
					}*/
				}
			}
		case err := <-es.Errors():
			if err.Error() == "EOF" {
				e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
					"FreeSwitch.EventServer",
					"Event socket client disconnected",
					logrus.ErrorLevel,
					map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge},
				))
			} else {
				e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
					"FreeSwitch.EventServer",
					"Error: %v",
					logrus.ErrorLevel,
					map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge}, err,
				))
			}
			break EventLoop
		case _ = <-e.killChan:
			e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
				"FreeSwitch.EventServer",
				"Kill request received, destroying channel",
				logrus.ErrorLevel,
				map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge},
			))
			c.Send(fmt.Sprintf("api uuid_kill %v", channelUUID))
			c.Close()
			return
		}
	}

	/*if device != nil {
		gofaxlib.Faxq.ReceiveStatus(device.Name, "D")
	}*/
	/*if err = xfl.SaveReceptionReport(); err != nil {
		logManager.Log(err)
	}*/

	// If reception failed:
	// Check if softmodem fallback should be enabled on the next call
	if gofaxlib.Config.FreeSwitch.SoftmodemFallback && !result.Success {
		var activateFallback bool

		if result.NegotiateCount > 1 {
			// Activate fallback if negotiation was repeated
			e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
				"FreeSwitch.EventServer",
				"Faxing failed with %d negotiations, enabling softmodem fallback for calls from/to %s.",
				logrus.InfoLevel,
				map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge}, result.NegotiateCount, cidnum,
			))
			activateFallback = true
		} else {
			var badrows uint
			for _, p := range result.PageResults {
				badrows += p.BadRows
			}
			if badrows > 0 {
				// Activate fallback if any bad rows were present
				e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
					"FreeSwitch.EventServer",
					"Faxing failed with %d bad rows in %d pages, enabling softmodem fallback for calls from/to %s.",
					logrus.InfoLevel,
					map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge}, badrows, result.TransferredPages, cidnum,
				))
				activateFallback = true
			}
		}

		if activateFallback {
			err = gofaxlib.SetSoftmodemFallback(nil, cidnum, true)
			if err != nil {
				e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
					"FreeSwitch.EventServer",
					err.Error(),
					logrus.ErrorLevel,
					map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge},
				))
			}
		}

	}

	faxjob := &FaxJob{
		UUID:           channelUUID,
		CalleeNumber:   recipient,
		CallerIdNumber: cidnum,
		CallerIdName:   cidname,
		FileName:       filename,
		UseECM:         true, // set this by default
		DisableV17:     false,
		Result:         result,
		SourceInfo: FaxSourceInfo{
			Timestamp:  time.Now(),
			SourceType: "gateway", // gateway means freeswitch
			Source:     gateway,
			SourceID:   channelUUID.String(),
		},
	}

	if !result.Success {
		e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
			"FreeSwitch.EventServer",
			"Success: %v, Hangup Cause: %v, Result: %v",
			logrus.ErrorLevel,
			map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge}, result.Success, result.HangupCause, result.ResultText,
		))

		e.server.Queue.QueueFaxResult <- QueueFaxResult{
			Job: faxjob,
		}

		err := os.Remove(filename)
		if err != nil {
			e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
				"Queue",
				"failed to remove fax file",
				logrus.ErrorLevel,
				map[string]interface{}{"uuid": channelUUID, "bridge": enableBridge},
			))
			return
		}

		return
	}

	e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
		"FreeSwitch.EventServer",
		"Success: %v, Hangup Cause: %v, Result: %v",
		logrus.InfoLevel,
		map[string]interface{}{"uuid": channelUUID.String(), "bridge": enableBridge}, result.Success, result.HangupCause, result.ResultText,
	))

	/*if !result.Success {
		e.server.LogManager.SendLog(e.server.LogManager.BuildLog(
			"FreeSwitch.EventServer",
			result.ResultText,
			logrus.ErrorLevel,
			map[string]interface{}{"uuid": channelUUID.String()},
		))
		return
	}*/
	// todo pass the xfl to a channel for db saving and further routing

	e.server.FaxJobRouting <- faxjob

	// todo check if it was from our primary gateways, if not,
	// then we have to send it to the router for further processing

	// Process received file
	// todo send information to inbound channel for db saving, and further routing

	/*rcvdcmd := gofaxlib.Config.Gofaxd.FaxRcvdCmd
	if rcvdcmd == "" {
		rcvdcmd = defaultFaxrcvdCmd
	}
	errmsg := ""
	if !result.Success {
		errmsg = result.ResultText
	}

	cmd := exec.Command(rcvdcmd, filename, usedDevice, logManager.CommID(), errmsg, cidnum, cidname, recipient, gateway)
	extraEnv := []string{
		fmt.Sprintf("HANGUPCAUSE=%s", result.HangupCause),
		fmt.Sprintf("TRANSFER_RATE=%d", result.TransferRate),
	}
	cmd.Env = append(os.Environ(), extraEnv...)
	logManager.Log("Calling", cmd.Path, cmd.Args)
	if output, err := cmd.CombinedOutput(); err != nil {
		logManager.Log(cmd.Path, "ended with", err)
		logManager.Log(output)
	} else {
		logManager.Log(cmd.Path, "ended successfully")
	}*/

	return
}
