package auth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

const (
	testRPID   = "localhost"
	testOrigin = "http://localhost:8080"
)

// recordingAuditor captures audit calls so tests can assert on them.
type recordingAuditor struct {
	mu      sync.Mutex
	actions []string
}

func (a *recordingAuditor) Append(_ context.Context, tx pgx.Tx, action, _ string, _ *uuid.UUID, _ map[string]any) error {
	if tx == nil {
		panic("auditor called without a transaction")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actions = append(a.actions, action)
	return nil
}

func (a *recordingAuditor) has(action string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, x := range a.actions {
		if x == action {
			return true
		}
	}
	return false
}

func newTestService(t *testing.T, mod ...func(*Config)) (*Service, *recordingAuditor) {
	t.Helper()
	pool := db.TestPool(t)
	aud := &recordingAuditor{}
	cfg := Config{
		RPID:           testRPID,
		RPDisplayName:  "AutoDev Bank",
		Origins:        []string{testOrigin},
		AllowTestLogin: true,
		Auditor:        aud,
	}
	for _, m := range mod {
		m(&cfg)
	}
	s, err := New(pool, cfg)
	testutil.NoError(t, err)
	return s, aud
}

func mustPrincipal(t *testing.T, s *Service, handle string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := db.WithTx(context.Background(), s.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, _, err = ensurePrincipal(ctx, tx, handle, strings.ToUpper(handle))
		return err
	})
	testutil.NoError(t, err)
	return id
}

// softAuthenticator is a minimal in-memory platform authenticator (ES256,
// "none" attestation, UP+UV) used to drive real ceremonies through the
// library's verification code.
type softAuthenticator struct {
	key        *ecdsa.PrivateKey
	credID     []byte
	userHandle []byte
	counter    uint32
	rpID       string
	origin     string
}

func newSoftAuthenticator(t *testing.T) *softAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	testutil.NoError(t, err)
	id := make([]byte, 32)
	_, _ = rand.Read(id)
	return &softAuthenticator{key: key, credID: id, rpID: testRPID, origin: testOrigin}
}

var b64 = base64.RawURLEncoding

type optionsEnvelope struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		User      struct {
			ID string `json:"id"`
		} `json:"user"`
	} `json:"publicKey"`
}

func (a *softAuthenticator) clientData(t *testing.T, typ, challenge string) []byte {
	t.Helper()
	cd, err := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": a.origin, "crossOrigin": false})
	testutil.NoError(t, err)
	return cd
}

// create answers navigator.credentials.create() and returns the
// PublicKeyCredential.toJSON() form.
func (a *softAuthenticator) create(t *testing.T, options json.RawMessage) json.RawMessage {
	t.Helper()
	var opt optionsEnvelope
	testutil.NoError(t, json.Unmarshal(options, &opt))
	uh, err := b64.DecodeString(opt.PublicKey.User.ID)
	testutil.NoError(t, err)
	a.userHandle = uh

	cd := a.clientData(t, "webauthn.create", opt.PublicKey.Challenge)
	pub := a.key.PublicKey
	x, y := make([]byte, 32), make([]byte, 32)
	pub.X.FillBytes(x)
	pub.Y.FillBytes(y)
	cose, err := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	testutil.NoError(t, err)

	rpHash := sha256.Sum256([]byte(a.rpID))
	var ad bytes.Buffer
	ad.Write(rpHash[:])
	ad.WriteByte(0x01 | 0x04 | 0x40) // UP | UV | AT
	_ = binary.Write(&ad, binary.BigEndian, a.counter)
	ad.Write(make([]byte, 16)) // AAGUID
	_ = binary.Write(&ad, binary.BigEndian, uint16(len(a.credID)))
	ad.Write(a.credID)
	ad.Write(cose)

	att, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": ad.Bytes()})
	testutil.NoError(t, err)

	out, err := json.Marshal(map[string]any{
		"id":    b64.EncodeToString(a.credID),
		"rawId": b64.EncodeToString(a.credID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(cd),
			"attestationObject": b64.EncodeToString(att),
			"transports":        []string{"internal"},
		},
		"clientExtensionResults":  map[string]any{},
		"authenticatorAttachment": "platform",
	})
	testutil.NoError(t, err)
	return out
}

// get answers navigator.credentials.get().
func (a *softAuthenticator) get(t *testing.T, options json.RawMessage) json.RawMessage {
	t.Helper()
	var opt optionsEnvelope
	testutil.NoError(t, json.Unmarshal(options, &opt))
	a.counter++
	cd := a.clientData(t, "webauthn.get", opt.PublicKey.Challenge)
	rpHash := sha256.Sum256([]byte(a.rpID))
	var ad bytes.Buffer
	ad.Write(rpHash[:])
	ad.WriteByte(0x01 | 0x04) // UP | UV
	_ = binary.Write(&ad, binary.BigEndian, a.counter)
	cdHash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad.Bytes()...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	testutil.NoError(t, err)
	out, err := json.Marshal(map[string]any{
		"id":    b64.EncodeToString(a.credID),
		"rawId": b64.EncodeToString(a.credID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(cd),
			"authenticatorData": b64.EncodeToString(ad.Bytes()),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        b64.EncodeToString(a.userHandle),
		},
		"clientExtensionResults":  map[string]any{},
		"authenticatorAttachment": "platform",
	})
	testutil.NoError(t, err)
	return out
}

func credentialRequest(body json.RawMessage) *http.Request {
	r := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	r.Header.Set("User-Agent", "soft-authenticator/1.0")
	return r
}

func TestBeginRegistration_RequiresResidentKeyAndUV(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	pid := mustPrincipal(t, s, "alice")

	options, challengeID, err := s.BeginRegistration(ctx, pid, "Alice", "alice")
	testutil.NoError(t, err)
	testutil.True(t, challengeID != "", "challenge id")

	var opt struct {
		PublicKey struct {
			Challenge              string `json:"challenge"`
			AuthenticatorSelection struct {
				ResidentKey      string `json:"residentKey"`
				UserVerification string `json:"userVerification"`
			} `json:"authenticatorSelection"`
			User struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"user"`
			RP struct {
				ID string `json:"id"`
			} `json:"rp"`
		} `json:"publicKey"`
	}
	testutil.NoError(t, json.Unmarshal(options, &opt))
	testutil.Equal(t, "required", opt.PublicKey.AuthenticatorSelection.ResidentKey)
	testutil.Equal(t, "required", opt.PublicKey.AuthenticatorSelection.UserVerification)
	testutil.Equal(t, "alice", opt.PublicKey.User.Name)
	testutil.Equal(t, testRPID, opt.PublicKey.RP.ID)
	uh, err := b64.DecodeString(opt.PublicKey.User.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, pid[:], uh, "user handle is the principal uuid bytes")

	var kind string
	var principal uuid.UUID
	var ttl time.Duration
	var challenge string
	err = s.pool.QueryRow(ctx, `select kind, principal_id, expires_at - now(), session_data->>'challenge'
		from control.webauthn_challenges where id=$1`, challengeID).Scan(&kind, &principal, &ttl, &challenge)
	testutil.NoError(t, err)
	testutil.Equal(t, "register", kind)
	testutil.Equal(t, pid, principal)
	testutil.True(t, ttl > 4*time.Minute && ttl <= 5*time.Minute, "challenge expires in 5 minutes")
	testutil.Equal(t, opt.PublicKey.Challenge, challenge, "stored session data carries the challenge")
}

func TestCeremony_RegisterLoginStepUp(t *testing.T) {
	s, aud := newTestService(t)
	ctx := context.Background()
	pid := mustPrincipal(t, s, "bob")
	authr := newSoftAuthenticator(t)

	// Registration.
	options, cid, err := s.BeginRegistration(ctx, pid, "Bob", "bob")
	testutil.NoError(t, err)
	regSess, err := s.FinishRegistration(ctx, cid, credentialRequest(authr.create(t, options)))
	testutil.NoError(t, err)
	testutil.Equal(t, pid, regSess.PrincipalID)
	testutil.True(t, aud.has("auth.register"), "register audited")

	var n int
	testutil.NoError(t, s.pool.QueryRow(ctx, `select count(*) from control.credentials where principal_id=$1`, pid).Scan(&n))
	testutil.Equal(t, 1, n)

	// The registration challenge is single use.
	_, err = s.FinishRegistration(ctx, cid, credentialRequest(authr.create(t, options)))
	testutil.ErrorIs(t, err, ErrChallengeNotFound)

	// Discoverable login.
	options, cid, err = s.BeginLogin(ctx)
	testutil.NoError(t, err)
	sess, err := s.FinishLogin(ctx, cid, credentialRequest(authr.get(t, options)))
	testutil.NoError(t, err)
	testutil.Equal(t, pid, sess.PrincipalID)
	testutil.Equal(t, "soft-authenticator/1.0", sess.UserAgent)
	testutil.True(t, aud.has("auth.login"), "login audited")

	var signCount int64
	var lastUsed *time.Time
	testutil.NoError(t, s.pool.QueryRow(ctx, `select (authenticator->>'signCount')::bigint, last_used_at
		from control.credentials where principal_id=$1`, pid).Scan(&signCount, &lastUsed))
	testutil.Equal(t, int64(1), signCount, "sign count stored")
	testutil.True(t, lastUsed != nil, "last_used_at set")

	// Replaying the same login challenge fails.
	_, err = s.FinishLogin(ctx, cid, credentialRequest(authr.get(t, options)))
	testutil.ErrorIs(t, err, ErrChallengeNotFound)

	// Step-up bound to a payload hash.
	hash := sha256.Sum256([]byte(`{"amount":100}`))
	testutil.ErrorIs(t, s.RequireStepUp(ctx, sess, hash[:]), ErrStepUpRequired)
	options, cid, err = s.BeginStepUp(ctx, sess, hash[:])
	testutil.NoError(t, err)
	var opt optionsEnvelope
	testutil.NoError(t, json.Unmarshal(options, &opt))
	ch, err := b64.DecodeString(opt.PublicKey.Challenge)
	testutil.NoError(t, err)
	testutil.True(t, bytes.HasSuffix(ch, hash[:]), "assertion challenge commits to the payload hash")

	testutil.NoError(t, s.FinishStepUp(ctx, sess, cid, credentialRequest(authr.get(t, options))))
	testutil.True(t, aud.has("auth.stepup"), "step-up audited")
	testutil.NoError(t, s.RequireStepUp(ctx, sess, hash[:]))
	testutil.ErrorIs(t, s.RequireStepUp(ctx, sess, hash[:]), ErrStepUpRequired, "step-up consumed")
}

func TestStepUp_ChallengeBoundToPrincipal(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	alice := mustPrincipal(t, s, "alice")
	mallory := mustPrincipal(t, s, "mallory")
	authr := newSoftAuthenticator(t)
	options, cid, err := s.BeginRegistration(ctx, alice, "Alice", "alice")
	testutil.NoError(t, err)
	aliceSess, err := s.FinishRegistration(ctx, cid, credentialRequest(authr.create(t, options)))
	testutil.NoError(t, err)
	malSess, err := s.TestLogin(ctx, mallory)
	testutil.NoError(t, err)

	hash := sha256.Sum256([]byte("payload"))
	options, cid, err = s.BeginStepUp(ctx, aliceSess, hash[:])
	testutil.NoError(t, err)
	err = s.FinishStepUp(ctx, malSess, cid, credentialRequest(authr.get(t, options)))
	testutil.ErrorIs(t, err, ErrChallengeNotFound, "another principal cannot finish alice's step-up")
}

func TestFinishLogin_ExpiredChallenge(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	pid := mustPrincipal(t, s, "carol")
	authr := newSoftAuthenticator(t)
	options, cid, err := s.BeginRegistration(ctx, pid, "Carol", "carol")
	testutil.NoError(t, err)
	_, err = s.FinishRegistration(ctx, cid, credentialRequest(authr.create(t, options)))
	testutil.NoError(t, err)

	options, cid, err = s.BeginLogin(ctx)
	testutil.NoError(t, err)
	_, err = s.pool.Exec(ctx, `update control.webauthn_challenges set expires_at = now() - interval '1 second' where id=$1`, cid)
	testutil.NoError(t, err)
	_, err = s.FinishLogin(ctx, cid, credentialRequest(authr.get(t, options)))
	testutil.ErrorIs(t, err, ErrChallengeNotFound)
}

func TestFinishLogin_BadSignatureRejected(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	pid := mustPrincipal(t, s, "dave")
	authr := newSoftAuthenticator(t)
	options, cid, err := s.BeginRegistration(ctx, pid, "Dave", "dave")
	testutil.NoError(t, err)
	_, err = s.FinishRegistration(ctx, cid, credentialRequest(authr.create(t, options)))
	testutil.NoError(t, err)

	// A different key claiming the same credential id must not log in.
	imposter := newSoftAuthenticator(t)
	imposter.credID, imposter.userHandle = authr.credID, authr.userHandle
	options, cid, err = s.BeginLogin(ctx)
	testutil.NoError(t, err)
	_, err = s.FinishLogin(ctx, cid, credentialRequest(imposter.get(t, options)))
	testutil.ErrorIs(t, err, ErrVerification)
}
