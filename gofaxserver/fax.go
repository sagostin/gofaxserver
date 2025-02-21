package gofaxserver

import (
	"github.com/gonicus/gofaxip/gofaxlib"
	"github.com/google/uuid"
)

// FaxJob contains everything FreeSWITCH needs to send a fax.
type FaxJob struct {
	// FreeSWITCH Channel UUID (generated when the job is created, or when we receive it)
	UUID uuid.UUID `json:"uuid,omitempty"`

	/*File string	`json:"file,omitempty"` // PDF or TIFF to send - will need to be converted to TIFF if PDF*/
	// FreeSwitch-specific information
	CalleeNumber   string `json:"number,omitempty"`       // Destination number
	CallerIdNumber string `json:"cidnum,omitempty"`       // Caller ID number
	CallerIdName   string `json:"cidname,omitempty"`      // Caller ID name
	FileName       string `json:"filename,omitempty"`     // TIFF file to send
	UseECM         bool   `json:"use_ecm,omitempty"`      // Use ECM (Error Correction Mode)
	DisableV17     bool   `json:"disable_v_17,omitempty"` // Disable V.17 (for lower baud rate)
	Identifier     string `json:"ident,omitempty"`        // Faxing ident
	Header         string `json:"header,omitempty"`       // Header (e.g., sender company name)

	Endpoints                []*Endpoint              `json:"gateways,omitempty"` // List of endpoints and such
	Result                   *gofaxlib.FaxResult      `json:"result,omitempty"`
	SourceRoutingInformation SourceRoutingInformation `json:"source_routing_information,omitempty"` // Routing information for the fax

	// These fields may be updated later in the process:
	NPages     int    `json:"npages,omitempty"`     // number of pages sent
	DataFormat string `json:"dataformat,omitempty"` // encoding or data format for the fax
	SignalRate int    `json:"signalrate,omitempty"` // signal rate (transfer rate)
	CSI        string `json:"csi,omitempty"`        // remote Caller Station Identification
	Status     string `json:"status,omitempty"`     // current status (e.g., "Dialing", "Completed", etc.)
	Returned   string `json:"returned,omitempty"`   // returned result code (e.g., SendDone, SendRetry, SendFailed)
	TotDials   int    `json:"totdials"`             // total attempted calls (as an int)
	NDials     int    `json:"ndials"`               // consecutive failed call attempts
	TotTries   int    `json:"tottries"`             // total answered or attempted calls
}

// NewFaxJob initializes a new FaxJob with a random UUID and default FreeSwitch settings.
func NewFaxJob() *FaxJob {
	jobUUID := uuid.New()

	return &FaxJob{
		UUID:   jobUUID,
		UseECM: true, // Default: use ECM
	}
}
