package domain

import "time"

// ObservationIntake accounts for how many observations a run has actually
// saved and which sequence value the next accepted observation must use. The
// counters move only when an observation is durably appended; a rejected
// submission must neither consume a sequence nor count toward a run's
// observations.
type ObservationIntake struct {
	AcceptedCount    int64 `json:"accepted_count"`
	AcceptedSequence int64 `json:"accepted_sequence"`
	RejectedAttempts int64 `json:"rejected_attempts"`
}

func (i ObservationIntake) Clone() ObservationIntake {
	return i
}

func (i ObservationIntake) CommittedCount() int64 {
	return i.AcceptedCount
}

// ExpectedSequence reports the sequence value the next submission must carry.
// With no reservations there is exactly one such value: one past the last
// saved observation.
func (i ObservationIntake) ExpectedSequence() int64 {
	return i.AcceptedSequence + 1
}

// CheckSequence rejects any submission that does not continue the saved
// observations exactly, including a sequence that was already saved.
func (i ObservationIntake) CheckSequence(sequence int64) error {
	if sequence < 1 {
		return NewError(CodeObservationConflict,
			"observation sequence must begin at 1")
	}
	if sequence != i.ExpectedSequence() {
		return NewError(CodeObservationConflict,
			"observation sequence must be contiguous and begin at 1")
	}
	return nil
}

// RecordAccepted advances the counters by exactly one saved observation.
func (i *ObservationIntake) RecordAccepted(sequence int64) {
	i.AcceptedSequence = sequence
	i.AcceptedCount++
}

// RecordRejected accounts for an unsuccessful submission. Rejections never
// move the sequence or the accepted count.
func (i *ObservationIntake) RecordRejected() {
	i.RejectedAttempts++
}

// Reconcile aligns the counters with the observations that are actually
// present after loading persisted state, so recording can always continue
// from the last saved observation.
func (i *ObservationIntake) Reconcile(count int64) {
	i.AcceptedCount = count
	i.AcceptedSequence = count
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
