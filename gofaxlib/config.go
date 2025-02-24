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
	"encoding/json"
	"io/ioutil"
	"log"
	"os"
)

var (
	// Config is the global configuration struct
	Config config
)

type config struct {
	FreeSwitch struct {
		EventClientSocket         string `json:"event_client_socket"`          // used for sending commands to freeswitch
		EventClientSocketPassword string `json:"event_client_socket_password"` // password for the event client socket
		EventServerSocket         string `json:"event_server_socket"`          // used for receiving events from freeswitch
		/*Gateway                   []string `json:"gateway"`                      // default gateways for sending faxes / upstream trunk*/
		Ident             string `json:"ident"`
		Header            string `json:"header"`
		Verbose           bool   `json:"verbose"`
		SoftmodemFallback bool   `json:"softmodem_fallback"`
	} `json:"freeswitch"`
	Faxing struct {
		TempDir                      string          `json:"temp_dir"` // eg. /opt/gofaxip/tmp
		EnableT38                    bool            `json:"enable_t38"`
		RequestT38                   bool            `json:"request_t38"`
		RecipientFromDiversionHeader bool            `json:"recipient_from_diversion_header"`
		AnswerAfter                  uint64          `json:"answer_after"`
		WaitTime                     uint64          `json:"wait_time"`
		DisableV17AfterRetry         string          `json:"disable_v17_after_retry"`
		DisableECMAfterRetry         string          `json:"disable_ecm_after_retry"`
		FailedResponse               []string        `json:"failed_response"`
		FailedResponseMap            map[string]bool `json:"failed_response_map"`
	} `json:"faxing"`
	Database struct { // this is a postgresql database
		Host     string `json:"host"`
		Port     string `json:"port"`
		User     string `json:"user"`
		Password string `json:"password"`
		Database string `json:"database"`
	} `json:"database"`
	Loki struct {
		PushURL  string `json:"push_url"`
		Username string `json:"username"`
		Password string `json:"password"`
		Job      string `json:"job"`
	} `json:"loki"`
}

// LoadConfig loads the configuration from a JSON file.
func LoadConfig(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		log.Fatalf("Config: unable to open file: %v", err)
	}
	defer file.Close()

	data, err := ioutil.ReadAll(file)
	if err != nil {
		log.Fatalf("Config: unable to read file: %v", err)
	}

	err = json.Unmarshal(data, &Config)
	if err != nil {
		log.Fatalf("Config: unable to parse JSON: %v", err)
	}

	// Rebuild FailedResponseMap from FailedResponse list
	Config.Faxing.FailedResponseMap = make(map[string]bool)
	for _, val := range Config.Faxing.FailedResponse {
		Config.Faxing.FailedResponseMap[val] = true
	}
}

func FailedHangUpCause(hangUpCause string) bool {
	if Config.Faxing.FailedResponseMap[hangUpCause] {
		return true
	} else {
		return false
	}
}
