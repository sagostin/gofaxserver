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
	"github.com/google/uuid"
)

// FaxJob containing everything FreeSWITCH needs
type FaxJob struct {
	// FreeSWITCH Channel UUID (we generate this)
	UUID uuid.UUID `json:"uuid,omitempty"`
	// Destination number
	Number string `json:"number,omitempty"`
	// Caller ID number
	Cidnum string `json:"cidnum,omitempty"`
	// Caller ID name
	Cidname string `json:"cidname,omitempty"`
	// TIFF file to send
	Filename string `json:"filename,omitempty"`
	// Use ECM (default: true)
	UseECM bool `json:"use_ecm,omitempty"`
	// Disable V.17 and limit signal rate to 9600 (default: false)
	DisableV17 bool `json:"disable_v_17,omitempty"`
	// Faxing ident
	Ident string `json:"ident,omitempty"`
	// String for header (i.e. sender company name)
	// Page header with timestamp, header, ident, pageno will be added
	// if this Header is non empty
	Header string `json:"header,omitempty"`

	// Gateways to try for this job
	Gateways []string `json:"gateways,omitempty"`
}

// NewFaxJob initializes a new Faxing Job with a random UUID
func NewFaxJob() *FaxJob {
	jobUUID := uuid.New()

	return &FaxJob{
		UUID:   jobUUID,
		UseECM: true,
	}
}
