package gofaxserver

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"time"
)

// FaxJobResultRecord is a GORM model representing a stored fax job result.
// It flattens the primary FaxJob fields (call UUID, callee/caller info) and summary FaxResult info
// into dedicated columns, while nesting the full FaxJob and FaxResult JSON.
// FaxJobResultRecord combines key fields from a FaxJob and its FaxResult.
type FaxJobResultRecord struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	JobUUID           uuid.UUID `json:"job_uuid"`
	CallUUID          uuid.UUID `json:"call_uuid"`
	CalleeNumber      string    `json:"callee_number"`
	CallerIdNumber    string    `json:"caller_id_number"`
	CallerIdName      string    `json:"caller_id_name"`
	FileName          string    `json:"file_name"`
	UseECM            bool      `json:"use_ecm"`
	DisableV17        bool      `json:"disable_v17"`
	Identifier        string    `json:"identifier"`
	Header            string    `json:"header"`
	Endpoints         string    `json:"endpoints"` // JSON-encoded endpoints slice
	SourceRoutingInfo string    `json:"source_routing_info"`

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
	Ts         time.Time     `json:"ts"`

	// FaxResult fields (from job.Result)
	StartTs          time.Time `json:"start_ts"`
	EndTs            time.Time `json:"end_ts"`
	HangupCause      string    `json:"hangup_cause"`
	TotalPages       uint      `json:"total_pages"`
	TransferredPages uint      `json:"transferred_pages"`
	ECM              bool      `json:"ecm"`
	RemoteID         string    `json:"remote_id"`
	ResultCode       int       `json:"result_code"`
	ResultText       string    `json:"result_text"`
	Success          bool      `json:"success"`
	TransferRate     uint      `json:"transfer_rate"`
	NegotiateCount   uint      `json:"negotiate_count"`

	CreatedAt time.Time `json:"created_at"`
}

// storeQueueFaxResult combines the FaxJob and FaxResult into a FaxJobResultRecord
// and saves it using GORM. It marshals the Endpoints slice into JSON.
func (q *Queue) storeQueueFaxResult(qFR QueueFaxResult) error {
	job := qFR.Job
	if job == nil {
		return fmt.Errorf("fax job is nil")
	}

	// Marshal endpoints to JSON.
	var endpointsJSON string
	if len(job.Endpoints) > 0 {
		if data, err := json.Marshal(job.Endpoints); err != nil {
			endpointsJSON = ""
		} else {
			endpointsJSON = string(data)
		}
	}

	record := FaxJobResultRecord{
		JobUUID:        job.UUID,
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
		Ts:         job.Ts,

		CreatedAt: time.Now(),
	}

	sourceRoutingInformation, err := json.Marshal(job.SourceRoutingInformation)
	if err != nil {
		return err
	}

	record.SourceRoutingInfo = string(sourceRoutingInformation)

	// If a FaxResult exists, fill in its fields.
	if job.Result != nil {
		record.StartTs = job.Result.StartTs
		record.EndTs = job.Result.EndTs
		record.HangupCause = job.Result.HangupCause
		record.TotalPages = job.Result.TotalPages
		record.TransferredPages = job.Result.TransferredPages
		record.ECM = job.Result.Ecm
		record.RemoteID = job.Result.RemoteID
		record.ResultCode = job.Result.ResultCode
		record.ResultText = job.Result.ResultText
		record.Success = job.Result.Success
		record.TransferRate = job.Result.TransferRate
		record.NegotiateCount = job.Result.NegotiateCount
	}

	// q.server.DB is assumed to be an initialized *gorm.DB instance.
	return q.server.DB.Create(&record).Error
}

// startQueueResults processes results from the QueueFaxResult channel asynchronously.
// For each result, it spawns a goroutine that logs the result and stores it in the database.
func (q *Queue) startQueueResults() {
	for result := range q.QueueFaxResult {
		go func(res QueueFaxResult) {
			fmt.Printf("Processing result for job %v: %+v\n", res.Job.UUID, res)
			if err := q.storeQueueFaxResult(res); err != nil {
				fmt.Printf("Error storing fax job result: %v\n", err)
			}
		}(result)
	}
}
