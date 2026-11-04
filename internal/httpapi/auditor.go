package httpapi

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/LupovichRan/AutoDev_Bank/internal/audit"
	"github.com/LupovichRan/AutoDev_Bank/internal/auth"
)

// auditorAdapter lets the auth service append to the audit chain.
type auditorAdapter struct{ rec *audit.Recorder }

var _ auth.Auditor = auditorAdapter{}

// NewAuditor adapts rec to auth.Auditor (for auth.Config.Auditor).
func NewAuditor(rec *audit.Recorder) auth.Auditor { return auditorAdapter{rec: rec} }

func (a auditorAdapter) Append(ctx context.Context, tx pgx.Tx, action, subject string, actorID *uuid.UUID, payload map[string]any) error {
	_, err := a.rec.Append(ctx, tx, audit.Event{ActorID: actorID, Action: action, Subject: subject, Payload: payload})
	return err
}
