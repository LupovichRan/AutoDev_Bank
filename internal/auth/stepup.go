package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
)

var (
	// ErrStepUpRequired means the session has no fresh, unused step-up for
	// this exact payload; the client must run the step-up ceremony first.
	ErrStepUpRequired = errors.New("auth: step-up required")
	// ErrBadPayloadHash means the payload hash is not a SHA-256 digest.
	ErrBadPayloadHash = errors.New("auth: payload hash must be 32 bytes (sha256)")
)

func payloadHash(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// BeginStepUp starts an assertion for the session's own credentials that is
// bound to payloadHash (sha256 of the canonical payment payload). The
// WebAuthn challenge is 32 random bytes followed by the hash, so the
// authenticator's signature covers the payload (dynamic linking); the hash
// is also stored with the challenge row, which is tied to sess.
func (s *Service) BeginStepUp(ctx context.Context, sess Session, payloadHash []byte) (json.RawMessage, string, error) {
	if len(payloadHash) != sha256.Size {
		return nil, "", ErrBadPayloadHash
	}
	var user *principalUser
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		user, err = loadUser(ctx, tx, sess.PrincipalID)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	if len(user.creds) == 0 {
		return nil, "", fmt.Errorf("%w: principal has no passkey", ErrVerification)
	}
	challenge := make([]byte, 32, 32+sha256.Size)
	if _, err := rand.Read(challenge); err != nil {
		return nil, "", err
	}
	challenge = append(challenge, payloadHash...)
	assertion, sd, err := s.wa.BeginLogin(user, webauthn.WithChallenge(challenge))
	if err != nil {
		return nil, "", fmt.Errorf("auth: begin step-up: %w", err)
	}
	id, err := saveChallenge(ctx, s.pool, challengeRow{
		kind:        "stepup",
		principalID: &sess.PrincipalID,
		sessionID:   &sess.ID,
		payloadHash: payloadHash,
	}, sd)
	if err != nil {
		return nil, "", err
	}
	opts, err := json.Marshal(assertion)
	return opts, id, err
}

// FinishStepUp verifies the assertion in r's body and marks sess as stepped
// up for the payload hash recorded at BeginStepUp. The challenge must have
// been begun by this very session. An assertion whose signature counter
// does not advance (stored count > 0) is refused with ErrVerification and
// audited as auth.clone_warning.
func (s *Service) FinishStepUp(ctx context.Context, sess Session, challengeID string, r *http.Request) error {
	body, err := readCredential(r)
	if err != nil {
		return err
	}
	return s.finishStepUp(ctx, sess, challengeID, body)
}

func (s *Service) finishStepUp(ctx context.Context, sess Session, challengeID string, credential []byte) error {
	ch, err := s.consumeChallenge(ctx, challengeID, "stepup", &sess.PrincipalID, &sess.ID)
	if err != nil {
		return err
	}
	parsed, err := parseAssertion(credential)
	if err != nil {
		return err
	}
	var (
		cloned   map[string]any
		clonedID []byte
	)
	err = db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		cloned, clonedID = nil, nil
		user, err := loadUser(ctx, tx, sess.PrincipalID)
		if err != nil {
			return err
		}
		cred, err := s.wa.ValidateLogin(user, ch.data, parsed)
		if err != nil {
			return verificationError(err)
		}
		if cred.Authenticator.CloneWarning {
			cloned, clonedID = cloneWarningPayload("stepup", cred, parsed), cred.ID
			return errCloneDetected
		}
		if err := updateCredentialAfterUse(ctx, tx, user.id, cred); err != nil {
			return err
		}
		if err := markSteppedUpTx(ctx, tx, sess, ch.payloadHash); err != nil {
			return err
		}
		return s.audit(ctx, tx, "auth.stepup", "principal:"+user.id.String(), &user.id, map[string]any{
			"payload_hash": hex.EncodeToString(ch.payloadHash),
		})
	})
	if errors.Is(err, errCloneDetected) {
		return s.refuseClonedStepUp(ctx, sess, clonedID, ch.payloadHash, cloned)
	}
	return err
}

// refuseClonedStepUp records the counter regression in its own committed
// transaction (the step-up's was rolled back) and fails the ceremony.
func (s *Service) refuseClonedStepUp(ctx context.Context, sess Session, credID, hash []byte, payload map[string]any) error {
	payload["payload_hash"] = hex.EncodeToString(hash)
	err := db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `update control.credentials
			set authenticator = authenticator || '{"cloneWarning": true}'::jsonb
			where id=$1 and principal_id=$2`, credID, sess.PrincipalID); err != nil {
			return err
		}
		return s.audit(ctx, tx, "auth.clone_warning", "principal:"+sess.PrincipalID.String(), &sess.PrincipalID, payload)
	})
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: signature counter did not increase (possible cloned authenticator)", ErrVerification)
}

// markSteppedUp records a verified step-up for payloadHash on sess.
func (s *Service) markSteppedUp(ctx context.Context, sess Session, payloadHash []byte) error {
	return db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		return markSteppedUpTx(ctx, tx, sess, payloadHash)
	})
}

func markSteppedUpTx(ctx context.Context, tx pgx.Tx, sess Session, payloadHash []byte) error {
	tag, err := tx.Exec(ctx, `update control.sessions set stepped_up_at=now(), stepup_payload_hash=$3
		where id=$1 and principal_id=$2 and expires_at > now()`, sess.ID, sess.PrincipalID, payloadHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNoSession
	}
	return nil
}

// RequireStepUp succeeds only if sess completed a step-up for exactly
// payloadHash within StepUpTTL, and consumes it in the same statement so
// one assertion authorises one payment. Concurrent callers race on the
// row: exactly one wins.
func (s *Service) RequireStepUp(ctx context.Context, sess Session, payloadHash []byte) error {
	return s.requireStepUp(ctx, s.pool, sess, payloadHash)
}

// RequireStepUpTx is RequireStepUp inside the caller's transaction, so the
// step-up is consumed only if that transaction (e.g. the transfer) commits.
func (s *Service) RequireStepUpTx(ctx context.Context, tx pgx.Tx, sess Session, payloadHash []byte) error {
	return s.requireStepUp(ctx, tx, sess, payloadHash)
}

func (s *Service) requireStepUp(ctx context.Context, q execer, sess Session, payloadHash []byte) error {
	if len(payloadHash) != sha256.Size {
		return ErrStepUpRequired
	}
	tag, err := q.Exec(ctx, `update control.sessions set stepped_up_at=null, stepup_payload_hash=null
		where id=$1 and principal_id=$2 and expires_at > now()
		  and stepup_payload_hash=$3
		  and stepped_up_at > now() - make_interval(secs => $4)`,
		sess.ID, sess.PrincipalID, payloadHash, s.cfg.StepUpTTL.Seconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStepUpRequired
	}
	return nil
}
