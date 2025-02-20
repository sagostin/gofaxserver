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
	Freeswitch struct {
		Socket            string   `json:"Socket"`
		Password          string   `json:"Password"`
		Gateway           []string `json:"Gateway"`
		Ident             string   `json:"Ident"`
		Header            string   `json:"Header"`
		Verbose           bool     `json:"Verbose"`
		SoftmodemFallback bool     `json:"SoftmodemFallback"`
	} `json:"Freeswitch"`
	Hylafax struct {
		Spooldir   string `json:"Spooldir"`
		Modems     uint   `json:"Modems"`
		Xferfaxlog string `json:"Xferfaxlog"`
	} `json:"Hylafax"`
	Gofaxd struct {
		EnableT38                    bool   `json:"EnableT38"`
		RequestT38                   bool   `json:"RequestT38"`
		RecipientFromDiversionHeader bool   `json:"RecipientFromDiversionHeader"`
		Socket                       string `json:"Socket"`
		Answerafter                  uint64 `json:"Answerafter"`
		Waittime                     uint64 `json:"Waittime"`
		FaxRcvdCmd                   string `json:"FaxRcvdCmd"`
		DynamicConfig                string `json:"DynamicConfig"`
		AllocateInboundDevices       bool   `json:"AllocateInboundDevices"`
	} `json:"Gofaxd"`
	Gofaxsend struct {
		EnableT38            bool            `json:"EnableT38"`
		RequestT38           bool            `json:"RequestT38"`
		FaxNumber            string          `json:"FaxNumber"`
		CallPrefix           string          `json:"CallPrefix"`
		DynamicConfig        string          `json:"DynamicConfig"`
		DisableV17AfterRetry string          `json:"DisableV17AfterRetry"`
		DisableECMAfterRetry string          `json:"DisableECMAfterRetry"`
		CidName              string          `json:"CidName"`
		FailedResponse       []string        `json:"FailedResponse"`
		FailedResponseMap    map[string]bool `json:"FailedResponseMap"`
	} `json:"Gofaxsend"`
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
	Config.Gofaxsend.FailedResponseMap = make(map[string]bool)
	for _, val := range Config.Gofaxsend.FailedResponse {
		Config.Gofaxsend.FailedResponseMap[val] = true
	}
}

func FailedHangupcause(hangupcause string) bool {
	if Config.Gofaxsend.FailedResponseMap[hangupcause] {
		return true
	} else {
		return false
	}
}
