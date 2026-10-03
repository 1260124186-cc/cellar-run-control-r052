package domain

import "time"

// ObservationIntake is a derived view of how many observations a run has
// actually accepted and which sequence value the next accepted observation
// must use. It is recomputed from the run's persisted observations, so a
// rejected submission never reserves a sequence or inflates the count and a
// restart always resumes from the observations already saved.
type ObservationIntake struct {
	AcceptedCount    int64 `json:"accepted_count"`
	AcceptedSequence int64 `json:"accepted_sequence"`
	RejectedAttempts int64 `json:"rejected_attempts"`
}

func (i ObservationIntake) Clone() ObservationIntake {
	return i
}

// CommittedCount reports the number of observations durably accepted.
func (i ObservationIntake) CommittedCount() int64 {
	return i.AcceptedCount
}

// ExpectedSequence reports the sequence value the next contiguous submission
// must carry.
func (i ObservationIntake) ExpectedSequence() int64 {
	return i.AcceptedSequence + 1
}

// Resync derives the intake counters from the observations that were actually
// persisted. Only an accepted, appended observation advances the counters.
func (i *ObservationIntake) Resync(observations []GravityObservation) {
	i.AcceptedCount = int64(len(observations))
	i.AcceptedSequence = 0
	if len(observations) > 0 {
		i.AcceptedSequence = int64(observations[len(observations)-1].Sequence)
	}
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
