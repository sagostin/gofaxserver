// This file is part of gofaxserver - https://github.com/sagostin/gofaxserver
// Copyright (C) 2025-2026 Shaun Agostinho
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
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// FaxJobResult is a GORM model representing a stored fax job result.
// It flattens the primary FaxJob fields (call UUID, callee/caller info) and summary FaxResult info
// into dedicated columns, while nesting the full FaxJob and FaxResult JSON.
// FaxJobResult combines key fields from a FaxJob and its FaxResult.
type FaxJobResult struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	SrcTenantID    uint      `json:"src_tenant_id"`
	DstTenantID    uint      `json:"dst_tenant_id"`
	JobUUID        uuid.UUID `json:"job_uuid"`
	CallUUID       uuid.UUID `json:"call_uuid"`
	CalleeNumber   string    `json:"callee_number"`
	CallerIdNumber string    `json:"caller_id_number"`
	CallerIdName   string    `json:"caller_id_name"`
	FileName       string    `json:"file_name"`
	UseECM         bool      `json:"use_ecm"`
	DisableV17     bool      `json:"disable_v17"`
	Identifier     string    `json:"identifier"`
	Header         string    `json:"header"`
	Endpoints      string    `json:"endpoints"` // JSON-encoded endpoints slice
	SourceInfo     string    `json:"source_info"`

	// Result classification
	ResultType    string `json:"result_type"`    // "reception", "bridge", "transmission", "delivery", "submission"
	AttemptNumber int    `json:"attempt_number"` // retry attempt number (1 = first try)
	EndpointID    uint   `json:"endpoint_id"`    // ID of the endpoint used
	EndpointType  string `json:"endpoint_type"`  // Type: "gateway", "webhook", etc.
	Gateway       string `json:"gateway"`        // Actual FreeSWITCH gateway used (transmission: winning gateway; reception/bridge: arrival gateway)

	// FaxJob fields
	NPages     int           `json:"npages"`
	DataFormat string        `json:"data_format"`
	SignalRate int           `json:"signal_rate"`
	CSI        string        `json:"csi"`
	Status     string        `json:"status"`
	Returned   string        `json:"returned"`
	TotDials   int           `json:"tot_dials"`
	NDials     int           `json:"n_dials"`
	TotTries   int           `json:"tot_tries"`
	JobTime    time.Duration `json:"job_time"`
	ConnTime   time.Duration `json:"conn_time"`
	Ts         *time.Time    `json:"ts"` // pointer to allow NULL

	// FaxResult fields (from job.Result)
	StartTs          *time.Time `json:"start_ts"` // pointer to allow NULL
	EndTs            *time.Time `json:"end_ts"`   // pointer to allow NULL
	HangupCause      string     `json:"hangup_cause"`
	TotalPages       uint       `json:"total_pages"`
	TransferredPages uint       `json:"transferred_pages"`
	ECM              bool       `json:"ecm"`
	EcmRequested     bool       `json:"ecm_requested"` // was ECM requested
	RemoteID         string     `json:"remote_id"`
	LocalID          string     `json:"local_id"` // local station ID
	ResultCode       int        `json:"result_code"`
	ResultText       string     `json:"result_text"`
	Success          bool       `json:"success"`
	TransferRate     uint       `json:"transfer_rate"`
	NegotiateCount   uint       `json:"negotiate_count"`
	T38Status        string     `json:"t38_status"`   // "negotiated", "rejected", etc.
	V17Disabled      bool       `json:"v17_disabled"` // was V.17 disabled
	PageResults      string     `json:"page_results"`

	// NEW: bridge / transcoding metadata
	IsBridge        bool          `json:"is_bridge"`
	BridgeDirection string        `json:"bridge_direction"` // "pbx_to_upstream" / "upstream_to_pbx"
	BridgeGateway   string        `json:"bridge_gateway"`
	BridgeStartTs   *time.Time    `json:"bridge_start_ts"` // pointer to allow NULL
	BridgeEndTs     *time.Time    `json:"bridge_end_ts"`   // pointer to allow NULL
	BridgeDuration  time.Duration `json:"bridge_duration"`
	BridgeT38       bool          `json:"bridge_t38"` // was T.38 gateway actually enabled?
	SoftmodemSrc    bool          `json:"softmodem_src"`
	SoftmodemDst    bool          `json:"softmodem_dst"`

	// T.38 decision tracking (for all call types)
	UsedT38           bool `json:"used_t38"`           // was T.38 actually used for this call
	SoftmodemFallback bool `json:"softmodem_fallback"` // was softmodem fallback override active

	// AppliedPolicies is a JSON array of fax policy rule IDs that applied to
	// this call (empty when no rules matched).
	AppliedPolicies string `json:"applied_policies"`

	CreatedAt time.Time `json:"created_at"`
}

// timePtr returns a pointer to the time, or nil if it's the zero value.
func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// placeholderHangupCause marks the synthetic result attached to a FaxJob at
// enqueue time (see web.go). It is not a real call outcome; it is persisted
// as a "submission" leg (attempt_number 0) so portal/API-originated jobs show
// their intake in the job chain, but consumers must keep ignoring it for
// terminal-state decisions (the portal poller skips rows with this hangup
// cause).
const placeholderHangupCause = "WEBHOOK"

// isPlaceholderResult reports whether the job carries the synthetic enqueue
// placeholder result rather than a real attempt/reception outcome.
func isPlaceholderResult(job *FaxJob) bool {
	return job != nil && job.Result != nil && job.Result.HangupCause == placeholderHangupCause
}

// classifyFaxResult infers the persisted leg classification (result type,
// endpoint metadata, attempt number, gateway attribution) from a job. Pure
// function so it is unit-testable without a database.
func classifyFaxResult(job *FaxJob) (resultType string, endpointID uint, endpointType string, attemptNumber int, gateway string) {
	// Infer result type and extract endpoint metadata
	resultType = "reception" // default: receiving a fax
	attemptNumber = 1

	if isPlaceholderResult(job) {
		// Synthetic enqueue placeholder (webhook/API submission): record the
		// job's intake as a "submission" leg. It is emitted exactly once per
		// job, by the router's progress tick, before endpoints are resolved.
		return "submission", 0, "", 0, ""
	}

	if job.IsBridge {
		resultType = "bridge"
	} else if len(job.Endpoints) > 0 {
		ep := job.Endpoints[0]
		endpointID = ep.ID
		endpointType = ep.EndpointType

		switch ep.EndpointType {
		case "gateway":
			// Outbound transmission to a gateway (e.g., sending to PBX or upstream)
			resultType = "transmission"
		case "webhook":
			// Delivery attempt (webhook notification)
			resultType = "delivery"
		default:
			resultType = "delivery"
		}

		// For delivery attempts, track attempt number via TotDials or TotTries
		if job.TotDials > 0 {
			attemptNumber = job.TotDials
		} else if job.TotTries > 0 {
			attemptNumber = job.TotTries
		}
	}

	// Gateway attribution: receptions/bridges record the arrival gateway
	// (SourceInfo.Source); transmissions record the gateway the call
	// actually went out on (captured from channel events, see
	// freeswitch_outbound.go), falling back to the attempted endpoint's
	// gateway name.
	gateway = job.Gateway
	if gateway == "" {
		switch resultType {
		case "reception", "bridge":
			gateway = job.SourceInfo.Source
		case "transmission":
			if len(job.Endpoints) > 0 {
				gateway = gatewayLabel(job.Endpoints[0])
			}
		}
	}
	return resultType, endpointID, endpointType, attemptNumber, gateway
}

func (q *Queue) storeQueueFaxResult(qFR QueueFaxResult) error {
	job := qFR.Job
	if job == nil {
		return fmt.Errorf("fax job is nil")
	}

	// Marshal endpoints to JSON.
	var endpointsJSON string
	if len(job.Endpoints) > 0 {
		if data, err := json.Marshal(job.Endpoints); err == nil {
			endpointsJSON = string(data)
		}
	}

	resultType, endpointID, endpointType, attemptNumber, gateway := classifyFaxResult(job)

	// Marshal applied policy rule IDs.
	var appliedPoliciesJSON string
	if len(job.AppliedPolicyIDs) > 0 {
		if data, err := json.Marshal(job.AppliedPolicyIDs); err == nil {
			appliedPoliciesJSON = string(data)
		}
	}

	record := FaxJobResult{
		JobUUID:        job.UUID,
		SrcTenantID:    job.SrcTenantID,
		DstTenantID:    job.DstTenantID,
		CallUUID:       job.CallUUID,
		CalleeNumber:   job.CalleeNumber,
		CallerIdNumber: job.CallerIdNumber,
		CallerIdName:   job.CallerIdName,
		FileName:       job.FileName,
		UseECM:         job.UseECM,
		DisableV17:     job.DisableV17,
		Identifier:     job.Identifier,
		Header:         job.Header,
		Endpoints:      endpointsJSON,

		// Result classification
		ResultType:    resultType,
		AttemptNumber: attemptNumber,
		EndpointID:    endpointID,
		EndpointType:  endpointType,
		Gateway:       gateway,

		NPages:     job.NPages,
		DataFormat: job.DataFormat,
		SignalRate: job.SignalRate,
		CSI:        job.CSI,
		Status:     job.Status,
		Returned:   job.Returned,
		TotDials:   job.TotDials,
		NDials:     job.NDials,
		TotTries:   job.TotTries,
		JobTime:    job.JobTime,
		ConnTime:   job.ConnTime,
		Ts:         timePtr(job.Ts),

		// bridge metadata – will be non-zero for transcoded calls
		IsBridge:        job.IsBridge,
		BridgeDirection: job.BridgeDirection,
		BridgeGateway:   job.BridgeGateway,
		BridgeStartTs:   timePtr(job.BridgeStartTs),
		BridgeEndTs:     timePtr(job.BridgeEndTs),
		BridgeDuration:  job.BridgeEndTs.Sub(job.BridgeStartTs),
		BridgeT38:       job.BridgeT38,
		SoftmodemSrc:    job.SoftmodemSrc,
		SoftmodemDst:    job.SoftmodemDst,

		// T.38 decision tracking
		UsedT38:           job.UsedT38,
		SoftmodemFallback: job.SoftmodemFallback,
		AppliedPolicies:   appliedPoliciesJSON,

		CreatedAt: time.Now(),
	}

	// Inbound receptions/bridges correlate by session: the job UUID IS the
	// a-leg channel UUID, so fall back to it when no call ID was set. Only
	// per-attempt legs (transmissions, deliveries) carry their own UUIDs.
	if record.CallUUID == uuid.Nil {
		record.CallUUID = job.UUID
	}

	if resultType == "submission" {
		// Present the intake lifecycle state, not the internal placeholder
		// marker. Transitions to "processed" (success=true) when the queue
		// worker picks the job up — see markSubmissionProcessed.
		record.Status = "queued"

		// Race safety net: the submission row is persisted asynchronously
		// (router tick via QueueFaxResult), so a real leg may already have
		// landed by the time this insert runs. If the job is already in
		// flight, record the intake directly as processed.
		var legs int64
		if err := q.server.DB.Model(&FaxJobResult{}).
			Where("job_uuid = ? AND result_type <> ?", job.UUID, "submission").
			Count(&legs).Error; err == nil && legs > 0 {
			now := time.Now()
			record.Status = "processed"
			record.ResultText = "processed"
			record.Success = true
			record.EndTs = &now
		}
	}

	sourceRoutingInformation, err := json.Marshal(job.SourceInfo)
	if err != nil {
		return err
	}
	record.SourceInfo = string(sourceRoutingInformation)

	// If a FaxResult exists, fill in its fields. For bridged calls it may be nil.
	if job.Result != nil {
		record.StartTs = timePtr(job.Result.StartTs)
		record.EndTs = timePtr(job.Result.EndTs)
		record.HangupCause = job.Result.HangupCause
		record.TotalPages = job.Result.TotalPages
		record.TransferredPages = job.Result.TransferredPages
		record.ECM = job.Result.Ecm
		record.EcmRequested = job.Result.EcmRequested
		record.RemoteID = job.Result.RemoteID
		record.LocalID = job.Result.LocalID
		record.ResultCode = job.Result.ResultCode
		record.ResultText = job.Result.ResultText
		record.Success = job.Result.Success
		record.TransferRate = job.Result.TransferRate
		record.NegotiateCount = job.Result.NegotiateCount
		record.T38Status = job.Result.T38Status
		record.V17Disabled = job.Result.V17Disabled

		if pageR, err := json.Marshal(job.Result.PageResults); err == nil {
			record.PageResults = string(pageR)
		}
	}

	// For bridge calls, infer success from hangup cause if SpanDSP didn't report
	if job.IsBridge && !record.Success {
		hangup := record.HangupCause
		if hangup == "" && job.Result != nil {
			hangup = job.Result.HangupCause
		}
		// NORMAL_CLEARING indicates successful call completion
		if hangup == "NORMAL_CLEARING" {
			record.Success = true
			if record.ResultText == "" {
				record.ResultText = "Bridge completed"
			}
		}
	}

	if err := q.server.DB.Create(&record).Error; err != nil {
		return err
	}

	// The submission leg is persisted asynchronously (router tick), so the
	// pickup-time UPDATE in processFax can run before the row exists. The
	// first real leg lands strictly after intake, making this the reliable
	// transition point: once any real leg is stored, the job is processed.
	if resultType != "submission" {
		q.markSubmissionProcessed(job.UUID)
	}
	return nil
}

// markSubmissionProcessed transitions the job's submission (intake) leg from
// "queued" to "processed". Called when the queue worker picks the job up
// (processFax) and — the reliable ordering point, since the submission row
// is persisted asynchronously — when the job's first real leg is stored
// (storeQueueFaxResult). Idempotent: only rows still in the queued state are
// touched. No-op for jobs without a submission leg (inbound receptions,
// bridges).
func (q *Queue) markSubmissionProcessed(jobID uuid.UUID) {
	now := time.Now()
	res := q.server.DB.Model(&FaxJobResult{}).
		Where("job_uuid = ? AND result_type = ? AND result_text = ?", jobID, "submission", "queued").
		Updates(map[string]interface{}{
			"success":     true,
			"status":      "processed",
			"result_text": "processed",
			"end_ts":      now,
		})
	if res.Error != nil {
		q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
			"FaxJobResult",
			"error marking submission leg processed: %v",
			logrus.ErrorLevel,
			map[string]interface{}{"uuid": jobID.String(), "error": res.Error.Error()},
			res.Error,
		))
	}
}

// startQueueResults processes results from the QueueFaxResult channel asynchronously.
// For each result, it spawns a goroutine that logs the result and stores it in the database.
func (q *Queue) startQueueResults() {
	for result := range q.QueueFaxResult {
		go func(res QueueFaxResult) {
			if res.Job == nil {
				q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
					"FaxJobResult",
					"received nil job in QueueFaxResult",
					logrus.ErrorLevel,
					nil,
				))
				return
			}

			// Log receipt of result
			q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
				"FaxJobResult",
				"processing fax job result",
				logrus.DebugLevel,
				map[string]interface{}{
					"uuid":       res.Job.UUID.String(),
					"is_bridge":  res.Job.IsBridge,
					"bridge_dir": res.Job.BridgeDirection,
					"bridge_gw":  res.Job.BridgeGateway,
					"used_t38":   res.Job.UsedT38,
					"has_result": res.Job.Result != nil,
				},
			))

			if err := q.storeQueueFaxResult(res); err != nil {
				q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
					"FaxJobResult",
					"error storing fax job result: %v",
					logrus.ErrorLevel,
					map[string]interface{}{
						"uuid":      res.Job.UUID.String(),
						"is_bridge": res.Job.IsBridge,
						"error":     err.Error(),
					},
					err,
				))
			} else {
				q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
					"FaxJobResult",
					"fax job result stored successfully",
					logrus.InfoLevel,
					map[string]interface{}{
						"uuid":      res.Job.UUID.String(),
						"is_bridge": res.Job.IsBridge,
					},
				))
			}
		}(result)
	}
}
