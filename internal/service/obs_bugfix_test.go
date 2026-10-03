package service

import (
	"testing"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/clock"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/storage"
)

func setupObservationRun(t *testing.T, dataDir string) (*Service, string) {
	t.Helper()
	store, err := storage.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	svc := New(store, clock.System{})

	formula, err := svc.CreateFormula(CreateFormulaInput{
		Name: "Bugfix Check", Style: "ale",
		TargetOriginalGravity: 1.050, TargetFinalGravity: 1.010,
		MaxFermentationTempC: 30, MinimumDays: 5, MaximumDays: 10,
	})
	if err != nil {
		t.Fatalf("create formula: %v", err)
	}
	_, err = svc.SetFormulaSteps(formula.ID, SetFormulaStepsInput{
		Steps: []domain.FormulaStep{
			{Sequence: 1, Label: "ferment", TemperatureC: 20, HoldMinutes: 60},
		},
	})
	if err != nil {
		t.Fatalf("set steps: %v", err)
	}
	if _, err := svc.ApproveFormula(formula.ID, VersionInput{}); err != nil {
		t.Fatalf("approve formula: %v", err)
	}
	vessel, err := svc.CreateVessel(CreateVesselInput{Code: "VSL-BUG", CapacityL: 500})
	if err != nil {
		t.Fatalf("create vessel: %v", err)
	}
	run, err := svc.CreateRun(CreateRunInput{
		Code: "RUN-BUG", FormulaID: formula.ID, PlannedVolumeL: 100,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, _, err := svc.ReserveRun(run.ID, ReserveRunInput{VesselID: vessel.ID}); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, _, err := svc.StartRun(run.ID, VersionInput{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	return svc, run.ID
}

func obs(seq int, gravity, temp float64, at time.Time) ObservationInput {
	return ObservationInput{Sequence: seq, Gravity: gravity, TemperatureC: temp, ObservedAt: at}
}

// Rejected submissions (out of range, wrong/gap/duplicate sequence) must not
// consume a sequence or count; the very next in-order submission succeeds.
func TestRejectedSubmissionDoesNotConsumeSequence(t *testing.T) {
	svc, runID := setupObservationRun(t, t.TempDir())
	base := time.Now().UTC().Add(time.Second)

	if _, err := svc.AppendObservation(runID, obs(1, 1.050, 20, base)); err != nil {
		t.Fatalf("obs1: %v", err)
	}
	// Out-of-range gravity rejected as invalid_input: must not consume seq 2.
	if _, err := svc.AppendObservation(runID, obs(2, 1.4, 20, base.Add(time.Second))); !domain.IsCode(err, domain.CodeInvalidInput) {
		t.Fatalf("expected invalid_input, got %v", err)
	}
	// Out-of-range temperature rejected: still must not consume seq 2.
	if _, err := svc.AppendObservation(runID, obs(2, 1.040, 99, base.Add(2*time.Second))); !domain.IsCode(err, domain.CodeInvalidInput) {
		t.Fatalf("expected invalid_input, got %v", err)
	}
	// Next in-order submission must succeed immediately.
	if _, err := svc.AppendObservation(runID, obs(2, 1.040, 20, base.Add(3*time.Second))); err != nil {
		t.Fatalf("obs2 after rejections should succeed, got %v", err)
	}
	// Gap sequence rejected.
	if _, err := svc.AppendObservation(runID, obs(4, 1.030, 20, base.Add(4*time.Second))); !domain.IsCode(err, domain.CodeObservationConflict) {
		t.Fatalf("expected observation_conflict for gap, got %v", err)
	}
	// Already-saved sequence resubmitted with another time: rejected as conflict,
	// must NOT be appended as a new observation.
	if _, err := svc.AppendObservation(runID, obs(1, 1.050, 20, base.Add(5*time.Second))); !domain.IsCode(err, domain.CodeObservationConflict) {
		t.Fatalf("expected observation_conflict for duplicate seq, got %v", err)
	}
	run, err := svc.GetRun(runID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(run.Observations) != 2 {
		t.Fatalf("expected 2 saved observations, got %d", len(run.Observations))
	}
	if run.Intake.CommittedCount() != 2 || run.Intake.ExpectedSequence() != 3 {
		t.Fatalf("intake not aligned: count=%d expectedSeq=%d", run.Intake.CommittedCount(), run.Intake.ExpectedSequence())
	}
	// And normal recording continues at seq 3.
	if _, err := svc.AppendObservation(runID, obs(3, 1.020, 20, base.Add(6*time.Second))); err != nil {
		t.Fatalf("obs3: %v", err)
	}
	// Lifecycle gates use effective count.
	if _, err := svc.BeginConditioning(runID, VersionInput{}); err != nil {
		t.Fatalf("conditioning: %v", err)
	}
}

// After reopening the persisted state (controller restart), recording must
// continue exactly after the last saved observation; counters must equal the
// detail's observation count; completion still works on the real count.
func TestSequenceContinuesAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	svc, runID := setupObservationRun(t, dir)
	base := time.Now().UTC().Add(time.Second)
	for i, g := range []float64{1.050, 1.030} {
		if _, err := svc.AppendObservation(runID, obs(i+1, g, 20, base.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatalf("obs%d: %v", i+1, err)
		}
	}
	// Rejected attempt right before restart: must leave no trace after reopen.
	if _, err := svc.AppendObservation(runID, obs(3, 1.9, 20, base.Add(2*time.Second))); !domain.IsCode(err, domain.CodeInvalidInput) {
		t.Fatalf("expected invalid_input, got %v", err)
	}

	// Reopen: simulates controller restart.
	store, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	restarted := New(store, clock.System{})
	run, err := restarted.GetRun(runID)
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if len(run.Observations) != 2 {
		t.Fatalf("expected 2 observations after restart, got %d", len(run.Observations))
	}
	if run.Intake.CommittedCount() != int64(len(run.Observations)) ||
		run.Intake.ExpectedSequence() != 3 {
		t.Fatalf("intake after restart misaligned: count=%d seq=%d",
			run.Intake.CommittedCount(), run.Intake.ExpectedSequence())
	}
	// Duplicate of a previously saved sequence rejected with existing semantics.
	if _, err := restarted.AppendObservation(runID, obs(2, 1.030, 20, base.Add(9*time.Second))); !domain.IsCode(err, domain.CodeObservationConflict) {
		t.Fatalf("duplicate seq after restart expected conflict, got %v", err)
	}
	// The genuine next submission continues the saved series.
	if _, err := restarted.AppendObservation(runID, obs(3, 1.014, 20, base.Add(3*time.Second))); err != nil {
		t.Fatalf("obs3 after restart: %v", err)
	}
	if _, err := restarted.BeginConditioning(runID, VersionInput{}); err != nil {
		t.Fatalf("conditioning after restart: %v", err)
	}
	completed, _, err := restarted.CompleteRun(runID, VersionInput{})
	if err != nil {
		t.Fatalf("complete after restart: %v", err)
	}
	if completed.Metrics == nil || completed.Metrics.ObservationCount != 3 {
		t.Fatalf("completion metrics count mismatch: %+v", completed.Metrics)
	}
}
