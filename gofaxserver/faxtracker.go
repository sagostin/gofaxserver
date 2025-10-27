// gofaxserver/fax_tracker.go
package gofaxserver

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"gofaxserver/gofaxlib"
)

type FaxPhase string

const (
	PhaseRouted  FaxPhase = "ROUTED"     // enqueued/routed into Queue
	PhaseAttempt FaxPhase = "ATTEMPTING" // an attempt is in-flight
	PhaseWaiting FaxPhase = "WAITING"    // backoff between attempts
	PhaseDone    FaxPhase = "DONE"       // finished (success/fail)
)

type FaxRunState struct {
	// identity
	JobUUID  uuid.UUID `json:"job_uuid"`
	CallUUID uuid.UUID `json:"call_uuid"` // latest active call UUID (changes per attempt)

	// who/where
	SrcTenantID uint   `json:"src_tenant_id"`
	DstTenantID uint   `json:"dst_tenant_id"`
	Caller      string `json:"caller"`
	Callee      string `json:"callee"`

	// progress
	Phase          FaxPhase            `json:"phase"`
	Attempt        int                 `json:"attempt"`
	MaxAttempts    int                 `json:"max_attempts"`
	CurrentPrio    uint                `json:"current_priority"`
	EndpointType   string              `json:"endpoint_type"` // gateway, webhook, ...
	EndpointLabel  string              `json:"endpoint_label"`
	EndpointValue  string              `json:"endpoint_value"`
	LastError      string              `json:"last_error"`
	LastResult     string              `json:"last_result"` // e.g. "OK", "RETRY", "FAILED"
	ResultSuccess  *bool               `json:"result_success,omitempty"`
	ResultSnapshot *gofaxlib.FaxResult `json:"result_snapshot,omitempty"`

	// timing
	EnqueuedAt  time.Time  `json:"enqueued_at"`
	StartedAt   time.Time  `json:"started_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// FaxTracker tracks all in-flight jobs.
type FaxTracker struct {
	mu      sync.RWMutex
	byJob   map[uuid.UUID]*FaxRunState
	byCall  map[uuid.UUID]uuid.UUID // CallUUID -> JobUUID (reverse index)
	running int                     // fast counter of active jobs
}

func NewFaxTracker() *FaxTracker {
	return &FaxTracker{
		byJob:  make(map[uuid.UUID]*FaxRunState),
		byCall: make(map[uuid.UUID]uuid.UUID),
	}
}

// Begin inserts/updates a job when it’s routed/enqueued.
func (t *FaxTracker) Begin(job *FaxJob) {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	st, ok := t.byJob[job.UUID]
	if !ok {
		st = &FaxRunState{
			JobUUID:     job.UUID,
			CallUUID:    job.CallUUID,
			SrcTenantID: job.SrcTenantID,
			DstTenantID: job.DstTenantID,
			Caller:      job.CallerIdNumber,
			Callee:      job.CalleeNumber,
			Phase:       PhaseRouted,
			EnqueuedAt:  now,
			UpdatedAt:   now,
		}
		t.byJob[job.UUID] = st
		t.running++
	} else {
		// refresh identity/caller/callee if changed
		st.CallUUID = job.CallUUID
		st.Caller = job.CallerIdNumber
		st.Callee = job.CalleeNumber
		st.Phase = PhaseRouted
		st.UpdatedAt = now
	}
	if job.CallUUID != uuid.Nil {
		t.byCall[job.CallUUID] = job.UUID
	}
}

// SetCall ties a new attempt CallUUID to the job.
func (t *FaxTracker) SetCall(jobID, callID uuid.UUID) {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	if st, ok := t.byJob[jobID]; ok {
		// drop old reverse index
		if st.CallUUID != uuid.Nil {
			delete(t.byCall, st.CallUUID)
		}
		st.CallUUID = callID
		st.UpdatedAt = now
		if callID != uuid.Nil {
			t.byCall[callID] = jobID
		}
	}
}

// MarkAttempt stamps the start of an attempt.
func (t *FaxTracker) MarkAttempt(jobID uuid.UUID, callID uuid.UUID, attempt int, maxAttempts int, prio uint, epType, epLabel, epValue string) {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	if st, ok := t.byJob[jobID]; ok {
		st.CallUUID = callID
		st.Attempt = attempt
		st.MaxAttempts = maxAttempts
		st.CurrentPrio = prio
		st.EndpointType = epType
		st.EndpointLabel = epLabel
		st.EndpointValue = epValue
		st.Phase = PhaseAttempt
		st.StartedAt = now
		st.UpdatedAt = now
		t.byCall[callID] = jobID
	}
}

// MarkResult captures the outcome of an attempt (success or not).
func (t *FaxTracker) MarkResult(jobID uuid.UUID, callID uuid.UUID, success bool, returned string, lastErr string, res *gofaxlib.FaxResult) {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	if st, ok := t.byJob[jobID]; ok {
		st.CallUUID = callID
		st.LastResult = returned
		st.LastError = lastErr
		st.ResultSuccess = &success
		if res != nil {
			// copy pointer reference (read only elsewhere)
			st.ResultSnapshot = res
		}
		st.UpdatedAt = now
		// phase stays Attempt/Waiting until Complete()
	}
}

// MarkWaiting sets backoff/idle phase between attempts.
func (t *FaxTracker) MarkWaiting(jobID uuid.UUID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if st, ok := t.byJob[jobID]; ok {
		st.Phase = PhaseWaiting
		st.UpdatedAt = time.Now()
	}
}

// Complete finalizes the job and removes reverse index; keeps a terminal snapshot.
// replace previous Complete()
func (t *FaxTracker) Complete(jobID uuid.UUID) {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	if st, ok := t.byJob[jobID]; ok && st.Phase != PhaseDone {
		// clear reverse index
		if st.CallUUID != uuid.Nil {
			delete(t.byCall, st.CallUUID)
		}
		// decrement running
		if t.running > 0 {
			t.running--
		}
		// if you want to keep a terminal timestamp before delete:
		st.Phase = PhaseDone
		st.CompletedAt = &now
		st.UpdatedAt = now

		// REMOVE from memory
		delete(t.byJob, jobID)
		return
	}
}

// ActiveCount returns number of non-DONE jobs.
func (t *FaxTracker) ActiveCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.running
}

// Snapshot returns a shallow copy of all states (for UI/API).
func (t *FaxTracker) Snapshot() []*FaxRunState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]*FaxRunState, 0, len(t.byJob))
	for _, st := range t.byJob {
		// copy the struct to avoid external mutation
		cp := *st
		out = append(out, &cp)
	}
	return out
}

// Lookup by JobUUID
func (t *FaxTracker) Get(jobID uuid.UUID) (*FaxRunState, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	st, ok := t.byJob[jobID]
	if !ok {
		return nil, false
	}
	cp := *st
	return &cp, true
}

// Lookup job by CallUUID (fast reverse index)
func (t *FaxTracker) FindJobByCall(callID uuid.UUID) (uuid.UUID, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	j, ok := t.byCall[callID]
	return j, ok
}
