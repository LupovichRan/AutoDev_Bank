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

// BeginStepUp starts an assertion for the session's own credentials that is
// bound to payloadHash (sha256 of the canonical payment payload). The
// WebAuthn challenge is 32 random bytes followed by the hash, so the
// authenticator's signature covers the payload (dynamic linking); the hash
// is also stored with the challenge row.
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
	id, err := s.saveChallenge(ctx, "stepup", &sess.PrincipalID, sd, payloadHash)
	if err != nil {
		return nil, "", err
	}
	opts, err := json.Marshal(assertion)
	return opts, id, err
}

// FinishStepUp verifies the assertion in r's body and marks sess as stepped
// up for the payload hash recorded at BeginStepUp.
func (s *Service) FinishStepUp(ctx context.Context, sess Session, challengeID string, r *http.Request) error {
	body, err := readCredential(r)
	if err != nil {
		return err
	}
	return s.finishStepUp(ctx, sess, challengeID, body)
}

func (s *Service) finishStepUp(ctx context.Context, sess Session, challengeID string, credential []byte) error {
	ch, err := s.consumeChallenge(ctx, challengeID, "stepup", &sess.PrincipalID)
	if err != nil {
		return err
	}
	parsed, err := parseAssertion(credential)
	if err != nil {
		return err
	}
	return db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		user, err := loadUser(ctx, tx, sess.PrincipalID)
		if err != nil {
			return err
		}
		cred, err := s.wa.ValidateLogin(user, ch.data, parsed)
		if err != nil {
			return verificationError(err)
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
// one assertion authorises one payment.
func (s *Service) RequireStepUp(ctx context.Context, sess Session, payloadHash []byte) error {
	if len(payloadHash) != sha256.Size {
		return ErrStepUpRequired
	}
	tag, err := s.pool.Exec(ctx, `update control.sessions set stepped_up_at=null, stepup_payload_hash=null
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
