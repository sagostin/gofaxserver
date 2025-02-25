package gofaxserver

import (
	"encoding/json"
	"fmt"
	"time"
)

// FaxJobResultRecord is a GORM model representing a stored fax job result.
// It flattens the primary FaxJob fields (call UUID, callee/caller info) and summary FaxResult info
// into dedicated columns, while nesting the full FaxJob and FaxResult JSON.
type FaxJobResultRecord struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	JobUUID        string `json:"job_uuid"`         // FaxJob.UUID
	CallUUID       string `json:"call_uuid"`        // FaxJob.CallUUID
	CalleeNumber   string `json:"callee_number"`    // FaxJob.CalleeNumber
	CallerIdNumber string `json:"caller_id_number"` // FaxJob.CallerIdNumber
	CallerIdName   string `json:"caller_id_name"`   // FaxJob.CallerIdName

	FaxSuccess  bool   `json:"fax_success"`  // FaxResult.Success
	HangupCause string `json:"hangup_cause"` // FaxResult.HangupCause
	Status      string `json:"status"`

	FaxJobJSON    string `json:"fax_job_json"`    // Nested JSON for the full FaxJob
	FaxResultJSON string `json:"fax_result_json"` // Nested JSON for the full FaxResult

	CreatedAt time.Time `json:"created_at"` // When the record was created.
	ErrorMsg  string    `json:"error_msg"`  // Any error message encountered.
}

// storeQueueFaxResult converts a QueueFaxResult into a FaxJobResultRecord.
// It extracts key fields from the FaxJob and FaxResult for flat columns, and nests the complete JSON.
func (q *Queue) storeQueueFaxResult(result QueueFaxResult) error {
	job := result.Job

	// Marshal the full FaxJob.
	jobData, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to marshal fax job: %w", err)
	}
	jobJSON := string(jobData)

	// Marshal the FaxResult, if available.
	var resultJSON string
	if job.Result != nil {
		data, err := json.Marshal(job.Result)
		if err != nil {
			return fmt.Errorf("failed to marshal fax result: %w", err)
		}
		resultJSON = string(data)
	}

	record := FaxJobResultRecord{
		JobUUID:        job.UUID.String(),
		CallUUID:       job.CallUUID.String(),
		CalleeNumber:   job.CalleeNumber,
		CallerIdNumber: job.CallerIdNumber,
		CallerIdName:   job.CallerIdName,

		FaxSuccess:  job.Result != nil && job.Result.Success,
		Status:      job.Status,
		HangupCause: "",

		FaxJobJSON:    jobJSON,
		FaxResultJSON: resultJSON,
		CreatedAt:     time.Now(),
	}

	if job.Result != nil {
		record.HangupCause = job.Result.HangupCause
	}
	if result.Err != nil {
		record.ErrorMsg = result.Err.Error()
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
