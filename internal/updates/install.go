package updates

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrUpdateUnavailable = errors.New("update is not verified and installable")
	ErrUpdateBusy        = errors.New("active runs or workspaces block update installation")
)

type Progress struct {
	Phase   string `json:"phase"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// Orchestrator contains deployment-specific, deliberately explicit steps.
// No shell command or binary path is inferred: an application must wire every
// mutating operation before an update can run.
type Orchestrator struct {
	Busy   func(context.Context) bool
	Backup func(context.Context, Snapshot) error
	// DownloadAndVerify must fetch the exact GitHub artifact selected by the
	// trusted snapshot and compare its bytes with Release.Checksum.
	DownloadAndVerify func(context.Context, string, string) ([]byte, error)
	// VerifyArtifact validates the downloaded bytes against the configured
	// release signature/allowlist; a checksum alone never authorizes install.
	VerifyArtifact func(context.Context, Snapshot, []byte) error
	Verify         func(context.Context, Snapshot) error
	Migrate        func(context.Context, Snapshot) error
	// Switch receives the exact bytes returned by DownloadAndVerify. A
	// deployment adapter must install these bytes, never re-download by URL.
	Switch   func(context.Context, Snapshot, []byte) error
	Restart  func(context.Context, Snapshot) error
	Health   func(context.Context, Snapshot) error
	Rollback func(context.Context, Snapshot) error
}

func (o Orchestrator) Install(ctx context.Context, snapshot Snapshot, report func(Progress)) error {
	if !snapshot.Installable || snapshot.Status != "update_available" {
		return ErrUpdateUnavailable
	}
	if o.Busy != nil && o.Busy(ctx) {
		return ErrUpdateBusy
	}
	// Rollback is part of the safety contract, not an optional enhancement. A
	// missing recovery path must be detected before backup or any mutation.
	if o.Backup == nil || o.DownloadAndVerify == nil || o.VerifyArtifact == nil || o.Verify == nil || o.Migrate == nil || o.Switch == nil || o.Restart == nil || o.Health == nil || o.Rollback == nil {
		return errors.New("update installation is not fully configured for recovery")
	}
	var artifact []byte
	steps := []struct {
		phase string
		fn    func(context.Context, Snapshot) error
	}{
		{"backup", o.Backup}, {"verify", o.Verify}, {"migrate", o.Migrate},
		{"switch", nil}, {"restart", o.Restart}, {"healthcheck", o.Health},
	}
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		reportProgress(report, Progress{Phase: step.phase, Status: "running", Message: "Update-Schritt läuft."})
		if step.phase == "verify" {
			var err error
			artifact, err = o.DownloadAndVerify(ctx, snapshot.Release.ArtifactURL, snapshot.Release.Checksum)
			if err == nil {
				err = o.VerifyArtifact(ctx, snapshot, artifact)
			}
			if err != nil {
				reportProgress(report, Progress{Phase: step.phase, Status: "failed", Message: "Update-Schritt fehlgeschlagen."})
				reportProgress(report, Progress{Phase: "rollback", Status: "running", Message: "Wiederherstellung läuft."})
				if rollbackErr := o.Rollback(ctx, snapshot); rollbackErr != nil {
					return fmt.Errorf("verify: %w; rollback: %v", err, rollbackErr)
				}
				reportProgress(report, Progress{Phase: "rollback", Status: "succeeded", Message: "Wiederherstellung abgeschlossen."})
				return fmt.Errorf("verify: %w", err)
			}
		}
		var err error
		if step.phase == "switch" {
			err = o.Switch(ctx, snapshot, artifact)
		} else {
			err = step.fn(ctx, snapshot)
		}
		if err != nil {
			reportProgress(report, Progress{Phase: step.phase, Status: "failed", Message: "Update-Schritt fehlgeschlagen."})
			if step.phase != "backup" {
				reportProgress(report, Progress{Phase: "rollback", Status: "running", Message: "Wiederherstellung läuft."})
				if rollbackErr := o.Rollback(ctx, snapshot); rollbackErr != nil {
					return fmt.Errorf("%s: %w; rollback: %v", step.phase, err, rollbackErr)
				}
				reportProgress(report, Progress{Phase: "rollback", Status: "succeeded", Message: "Wiederherstellung abgeschlossen."})
			}
			return fmt.Errorf("%s: %w", step.phase, err)
		}
		reportProgress(report, Progress{Phase: step.phase, Status: "succeeded", Message: "Update-Schritt abgeschlossen."})
	}
	return nil
}

func reportProgress(report func(Progress), progress Progress) {
	if report != nil {
		report(progress)
	}
}
