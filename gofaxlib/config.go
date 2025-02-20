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

	_ "github.com/gonicus/gofaxip/gofaxlib/logger"
)

var (
	// Config is the global configuration struct
	Config config
)

type config struct {
	FreeSwitch struct {
		Socket            string   `json:"socket"`
		Password          string   `json:"password"`
		Gateway           []string `json:"gateway"`
		Ident             string   `json:"ident"`
		Header            string   `json:"header"`
		Verbose           bool     `json:"verbose"`
		SoftmodemFallback bool     `json:"softmodem_fallback"`
	} `json:"Freeswitch"`
	Fax struct {
		EnableT38                    bool            `json:"enable_t38"`
		RequestT38                   bool            `json:"request_t38"`
		RecipientFromDiversionHeader bool            `json:"recipient_from_diversion_header"`
		EventSocketListen            string          `json:"event_socket_listen"`
		AnswerAfter                  uint64          `json:"answer_after"`
		WaitTime                     uint64          `json:"wait_time"`
		DisableV17AfterRetry         string          `json:"disable_v17_after_retry"`
		DisableECMAfterRetry         string          `json:"disable_ecm_after_retry"`
		FailedResponse               []string        `json:"failed_response"`
		FailedResponseMap            map[string]bool `json:"failed_response_map"`
	} `json:"fax"`
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
	Config.Fax.FailedResponseMap = make(map[string]bool)
	for _, val := range Config.Fax.FailedResponse {
		Config.Fax.FailedResponseMap[val] = true
	}
}

func FailedHangUpCause(hangUpCause string) bool {
	if Config.Fax.FailedResponseMap[hangUpCause] {
		return true
	} else {
		return false
	}
}
