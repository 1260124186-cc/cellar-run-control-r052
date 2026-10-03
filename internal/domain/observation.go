package domain

import "time"

// ObservationIntake accounts for how many observations a run has accepted and
// which sequence value the next accepted observation must use. The committed
// counters travel with the run, while the staged counters describe the single
// submission that is currently being committed.
type ObservationIntake struct {
	AcceptedCount    int64 `json:"accepted_count"`
	AcceptedSequence int64 `json:"accepted_sequence"`
	RejectedAttempts int64 `json:"rejected_attempts"`

	staged         bool
	stagedSequence int64
	stagedCount    int64
}

func (i ObservationIntake) Clone() ObservationIntake {
	return i
}

func (i ObservationIntake) CommittedCount() int64 {
	return i.AcceptedCount
}

func (i ObservationIntake) Reserved() bool {
	return i.staged
}

// ExpectedSequence reports the sequence value the next contiguous submission
// must carry, including a reservation that has not been released yet.
func (i ObservationIntake) ExpectedSequence() int64 {
	base := i.AcceptedSequence
	if i.staged && i.stagedSequence > base {
		base = i.stagedSequence
	}
	return base + 1
}

// Reserve claims the next contiguous sequence for an in-flight submission.
func (i *ObservationIntake) Reserve(sequence int64) error {
	if sequence < 1 {
		i.RejectedAttempts++
		return NewError(CodeObservationConflict,
			"observation sequence must begin at 1")
	}
	if sequence != i.ExpectedSequence() {
		i.RejectedAttempts++
		return NewError(CodeObservationConflict,
			"observation sequence must be contiguous and begin at 1")
	}
	i.staged = true
	i.stagedSequence = sequence
	i.stagedCount = i.AcceptedCount + 1
	return nil
}

// Commit publishes a reserved submission as durable intake progress.
func (i *ObservationIntake) Commit() {
	if i.staged {
		i.AcceptedSequence = i.stagedSequence
		i.AcceptedCount = i.stagedCount
	}
	i.staged = false
	i.stagedSequence = i.AcceptedSequence
	i.stagedCount = i.AcceptedCount
}

// Release drops a reservation whose submission was not accepted.
func (i *ObservationIntake) Release() {
	if !i.staged {
		return
	}
	i.RejectedAttempts++
	i.staged = false
	i.stagedSequence = i.AcceptedSequence
	i.stagedCount = i.AcceptedCount
}

// ResumeAttempt restores the reservation that was still open when the previous
// process stopped.
func (i *ObservationIntake) ResumeAttempt() {
	i.AcceptedSequence = i.stagedSequence
	i.AcceptedCount = i.stagedCount
	i.staged = false
	i.stagedSequence = i.AcceptedSequence
	i.stagedCount = i.AcceptedCount
}

type GravityObservation struct {
	RunID        string    `json:"run_id"`
	Sequence     int       `json:"sequence"`
	Gravity      float64   `json:"gravity"`
	TemperatureC float64   `json:"temperature_c"`
	ObservedAt   time.Time `json:"observed_at"`
	Note         string    `json:"note,omitempty"`
	ReceivedAt   time.Time `json:"received_at"`
}

func ValidateObservation(
	runStartedAt time.Time,
	previous *GravityObservation,
	candidate GravityObservation,
	now time.Time,
) error {
	if candidate.Gravity < 0.980 || candidate.Gravity > 1.200 {
		return FieldError(CodeInvalidInput, "gravity",
			"must be between 0.980 and 1.200")
	}
	if candidate.TemperatureC < -5 || candidate.TemperatureC > 60 {
		return FieldError(CodeInvalidInput, "temperature_c",
			"must be between -5 and 60 Celsius")
	}
	if len([]rune(candidate.Note)) > 500 {
		return FieldError(CodeInvalidInput, "note",
			"must not exceed 500 characters")
	}
	observed := candidate.ObservedAt.UTC()
	if observed.Before(runStartedAt.UTC()) {
		return FieldError(CodeInvalidInput, "observed_at",
			"must not precede the run start")
	}
	if observed.After(now.UTC().Add(time.Minute)) {
		return FieldError(CodeInvalidInput, "observed_at",
			"must not be in the future")
	}
	if previous != nil {
		if !observed.After(previous.ObservedAt.UTC()) {
			return NewError(CodeObservationConflict,
				"observation time must increase strictly")
		}
		if candidate.Gravity-previous.Gravity > 0.010 {
			return NewError(CodeObservationConflict,
				"gravity must not rise more than 0.010 above the previous value")
		}
	}
	return nil
}
