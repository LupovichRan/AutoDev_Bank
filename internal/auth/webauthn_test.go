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
	mu     sync.Mutex
	events []auditEvent
}

type auditEvent struct {
	action  string
	payload map[string]any
}

func (a *recordingAuditor) Append(_ context.Context, tx pgx.Tx, action, _ string, _ *uuid.UUID, payload map[string]any) error {
	if tx == nil {
		panic("auditor called without a transaction")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, auditEvent{action, payload})
	return nil
}

func (a *recordingAuditor) has(action string) bool {
	return a.count(action) > 0
}

func (a *recordingAuditor) count(action string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, x := range a.events {
		if x.action == action {
			n++
		}
	}
	return n
}

func (a *recordingAuditor) last(action string) map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := len(a.events) - 1; i >= 0; i-- {
		if a.events[i].action == action {
			return a.events[i].payload
		}
	}
	return nil
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
	zeroCount  bool // synced passkeys report a constant 0 sign count
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
	if !a.zeroCount {
		a.counter++
	}
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

// register runs a full new-principal registration and returns the session
// it signs in plus the authenticator holding the passkey.
func register(t *testing.T, s *Service, handle string) (Session, *softAuthenticator) {
	t.Helper()
	authr := newSoftAuthenticator(t)
	return registerWith(t, s, handle, authr), authr
}

func registerWith(t *testing.T, s *Service, handle string, authr *softAuthenticator) Session {
	t.Helper()
	ctx := context.Background()
	options, cid, err := s.BeginRegistration(ctx, handle, strings.ToUpper(handle))
	testutil.NoError(t, err)
	sess, err := s.FinishRegistration(ctx, cid, credentialRequest(authr.create(t, options)))
	testutil.NoError(t, err)
	return sess
}

// login runs a discoverable login with authr.
func login(t *testing.T, s *Service, authr *softAuthenticator) (Session, error) {
	t.Helper()
	ctx := context.Background()
	options, cid, err := s.BeginLogin(ctx)
	testutil.NoError(t, err)
	return s.FinishLogin(ctx, cid, credentialRequest(authr.get(t, options)))
}

// stepUp runs a step-up ceremony for hash on sess with authr.
func stepUp(t *testing.T, s *Service, sess Session, authr *softAuthenticator, hash []byte) error {
	t.Helper()
	ctx := context.Background()
	options, cid, err := s.BeginStepUp(ctx, sess, hash)
	testutil.NoError(t, err)
	return s.FinishStepUp(ctx, sess, cid, credentialRequest(authr.get(t, options)))
}

func principalCount(t *testing.T, s *Service, handle string) int {
	t.Helper()
	var n int
	testutil.NoError(t, s.pool.QueryRow(context.Background(),
		`select count(*) from control.principals where handle=$1`, handle).Scan(&n))
	return n
}

func TestBeginRegistration_RequiresResidentKeyAndUV(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()

	options, challengeID, err := s.BeginRegistration(ctx, "alice", "Alice")
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
				ID          string `json:"id"`
				Name        string `json:"name"`
				DisplayName string `json:"displayName"`
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
	testutil.Equal(t, "Alice", opt.PublicKey.User.DisplayName)
	testutil.Equal(t, testRPID, opt.PublicKey.RP.ID)
	uh, err := b64.DecodeString(opt.PublicKey.User.ID)
	testutil.NoError(t, err)
	_, err = uuid.FromBytes(uh)
	testutil.NoError(t, err, "user handle is a principal uuid")

	var kind, handle, display string
	var principal *uuid.UUID
	var ttl time.Duration
	var challenge string
	err = s.pool.QueryRow(ctx, `select kind, principal_id, handle, display_name, expires_at - now(), session_data->>'challenge'
		from control.webauthn_challenges where id=$1`, challengeID).Scan(&kind, &principal, &handle, &display, &ttl, &challenge)
	testutil.NoError(t, err)
	testutil.Equal(t, "register", kind)
	testutil.True(t, principal == nil, "no principal yet")
	testutil.Equal(t, "alice", handle)
	testutil.Equal(t, "Alice", display)
	testutil.True(t, ttl > 4*time.Minute && ttl <= 5*time.Minute, "challenge expires in 5 minutes")
	testutil.Equal(t, opt.PublicKey.Challenge, challenge, "stored session data carries the challenge")
}

func TestRegistration_BeginOnlyNeverCreatesPrincipal(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	_, _, err := s.BeginRegistration(ctx, "olga", "Olga")
	testutil.NoError(t, err)
	testutil.Equal(t, 0, principalCount(t, s, "olga"), "begin must not create the principal")
	var n int
	testutil.NoError(t, s.pool.QueryRow(ctx, `select count(*) from control.principals`).Scan(&n))
	testutil.Equal(t, 0, n)
}

func TestRegistration_CancelledThenRetried(t *testing.T) {
	s, aud := newTestService(t)
	ctx := context.Background()

	// First attempt is abandoned (the user cancels the browser prompt).
	abandoned, abandonedID, err := s.BeginRegistration(ctx, "pia", "Pia")
	testutil.NoError(t, err)

	// A retry (or anyone) can begin again for the same handle.
	options, cid, err := s.BeginRegistration(ctx, "pia", "Pia")
	testutil.NoError(t, err)
	authr := newSoftAuthenticator(t)
	sess, err := s.FinishRegistration(ctx, cid, credentialRequest(authr.create(t, options)))
	testutil.NoError(t, err)
	testutil.Equal(t, 1, principalCount(t, s, "pia"))
	testutil.Equal(t, uuid.UUID(authr.userHandle), sess.PrincipalID, "principal id is the user handle from begin")
	testutil.True(t, aud.has("auth.register"), "register audited")

	// The abandoned ceremony can no longer claim the handle.
	late := newSoftAuthenticator(t)
	_, err = s.FinishRegistration(ctx, abandonedID, credentialRequest(late.create(t, abandoned)))
	testutil.ErrorIs(t, err, ErrHandleTaken)
	testutil.Equal(t, 1, principalCount(t, s, "pia"))
	var n int
	testutil.NoError(t, s.pool.QueryRow(ctx, `select count(*) from control.credentials`).Scan(&n))
	testutil.Equal(t, 1, n, "the losing credential is not stored")

	// Once taken, begin refuses the handle.
	_, _, err = s.BeginRegistration(ctx, "pia", "Pia")
	testutil.ErrorIs(t, err, ErrHandleTaken)
}

func TestAddCredential_RequiresFreshStepUp(t *testing.T) {
	s, aud := newTestService(t)
	ctx := context.Background()
	sess, authr := register(t, s, "pat")

	// A plain session is not enough.
	_, _, err := s.BeginAddCredential(ctx, sess)
	testutil.ErrorIs(t, err, ErrStepUpRequired)

	// A step-up for some other payload is not enough either.
	other := sha256.Sum256([]byte("transfer"))
	testutil.NoError(t, stepUp(t, s, sess, authr, other[:]))
	_, _, err = s.BeginAddCredential(ctx, sess)
	testutil.ErrorIs(t, err, ErrStepUpRequired)

	// Step-up bound to "add-credential:pat" unlocks it, once.
	hash := AddCredentialPayloadHash("pat")
	want := sha256.Sum256([]byte("add-credential:pat"))
	testutil.Equal(t, want[:], hash)
	testutil.NoError(t, stepUp(t, s, sess, authr, hash))
	options, cid, err := s.BeginAddCredential(ctx, sess)
	testutil.NoError(t, err)
	_, _, err = s.BeginAddCredential(ctx, sess)
	testutil.ErrorIs(t, err, ErrStepUpRequired, "step-up consumed")

	second := newSoftAuthenticator(t)
	r := credentialRequest(second.create(t, options))
	r = r.WithContext(WithSession(r.Context(), sess))
	newSess, err := s.FinishRegistration(ctx, cid, r)
	testutil.NoError(t, err)
	testutil.Equal(t, sess.PrincipalID, newSess.PrincipalID)
	testutil.Equal(t, uuid.UUID(second.userHandle), sess.PrincipalID)
	testutil.Equal(t, 1, aud.count("auth.register"), "adding is not a registration")
	testutil.True(t, aud.has("auth.credential_added"), "credential_added audited")
	testutil.Equal[any](t, sess.ID, aud.last("auth.credential_added")["session_id"])

	_, err = s.LoadSession(ctx, sess.ID)
	testutil.ErrorIs(t, err, ErrNoSession, "the acting session is replaced")

	// The second passkey signs in as pat.
	loggedIn, err := login(t, s, second)
	testutil.NoError(t, err)
	testutil.Equal(t, sess.PrincipalID, loggedIn.PrincipalID)
}

func TestAddCredential_FinishRequiresActingSession(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	sess, authr := register(t, s, "quinn")
	testutil.NoError(t, stepUp(t, s, sess, authr, AddCredentialPayloadHash("quinn")))
	options, cid, err := s.BeginAddCredential(ctx, sess)
	testutil.NoError(t, err)
	// Finishing without the session that started it fails.
	_, err = s.FinishRegistration(ctx, cid, credentialRequest(newSoftAuthenticator(t).create(t, options)))
	testutil.ErrorIs(t, err, ErrChallengeNotFound)
}

func TestCeremony_RegisterLoginStepUp(t *testing.T) {
	s, aud := newTestService(t)
	ctx := context.Background()
	authr := newSoftAuthenticator(t)

	// Registration.
	options, cid, err := s.BeginRegistration(ctx, "bob", "Bob")
	testutil.NoError(t, err)
	regSess, err := s.FinishRegistration(ctx, cid, credentialRequest(authr.create(t, options)))
	testutil.NoError(t, err)
	pid := regSess.PrincipalID
	testutil.True(t, aud.has("auth.register"), "register audited")
	testutil.Equal(t, 1, principalCount(t, s, "bob"))

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
	testutil.False(t, aud.has("auth.clone_warning"), "no clone warning for increasing counters")
	testutil.NoError(t, s.RequireStepUp(ctx, sess, hash[:]))
	testutil.ErrorIs(t, s.RequireStepUp(ctx, sess, hash[:]), ErrStepUpRequired, "step-up consumed")
}

func TestStepUp_ChallengeBoundToPrincipal(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	aliceSess, authr := register(t, s, "alice")
	malSess, err := s.TestLogin(ctx, mustPrincipal(t, s, "mallory"))
	testutil.NoError(t, err)

	hash := sha256.Sum256([]byte("payload"))
	options, cid, err := s.BeginStepUp(ctx, aliceSess, hash[:])
	testutil.NoError(t, err)
	err = s.FinishStepUp(ctx, malSess, cid, credentialRequest(authr.get(t, options)))
	testutil.ErrorIs(t, err, ErrChallengeNotFound, "another principal cannot finish alice's step-up")
}

func TestStepUp_ChallengeBoundToSession(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	first, authr := register(t, s, "sam")
	second, err := login(t, s, authr)
	testutil.NoError(t, err)

	hash := sha256.Sum256([]byte("payload"))
	options, cid, err := s.BeginStepUp(ctx, first, hash[:])
	testutil.NoError(t, err)
	err = s.FinishStepUp(ctx, second, cid, credentialRequest(authr.get(t, options)))
	testutil.ErrorIs(t, err, ErrChallengeNotFound, "another session of the same principal cannot finish it")
	testutil.ErrorIs(t, s.RequireStepUp(ctx, second, hash[:]), ErrStepUpRequired)
	testutil.ErrorIs(t, s.RequireStepUp(ctx, first, hash[:]), ErrStepUpRequired)
}

func TestStepUp_DynamicLinkingNegative(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	sess, authr := register(t, s, "tara")
	hashA := sha256.Sum256([]byte("pay 10 to A"))
	hashB := sha256.Sum256([]byte("pay 9999 to B"))
	optsA, _, err := s.BeginStepUp(ctx, sess, hashA[:])
	testutil.NoError(t, err)
	_, cidB, err := s.BeginStepUp(ctx, sess, hashB[:])
	testutil.NoError(t, err)

	// The user approved A; an attacker submits that assertion against B.
	err = s.FinishStepUp(ctx, sess, cidB, credentialRequest(authr.get(t, optsA)))
	testutil.ErrorIs(t, err, ErrVerification)
	testutil.ErrorIs(t, s.RequireStepUp(ctx, sess, hashB[:]), ErrStepUpRequired)
	testutil.ErrorIs(t, s.RequireStepUp(ctx, sess, hashA[:]), ErrStepUpRequired)
}

func TestFinishLogin_WrongOriginRejected(t *testing.T) {
	s, _ := newTestService(t)
	_, authr := register(t, s, "uma")
	authr.origin = "http://evil.example:8080"
	_, err := login(t, s, authr)
	testutil.ErrorIs(t, err, ErrVerification)
}

func TestRegistration_WrongOriginRejected(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	authr := newSoftAuthenticator(t)
	authr.origin = "https://phish.example"
	options, cid, err := s.BeginRegistration(ctx, "vera", "Vera")
	testutil.NoError(t, err)
	_, err = s.FinishRegistration(ctx, cid, credentialRequest(authr.create(t, options)))
	testutil.ErrorIs(t, err, ErrVerification)
	testutil.Equal(t, 0, principalCount(t, s, "vera"))
}

func TestCounterRegression_StepUpRejectedLoginAudited(t *testing.T) {
	s, aud := newTestService(t)
	ctx := context.Background()
	sess, authr := register(t, s, "walt")
	_, err := login(t, s, authr) // stored sign count is now 1
	testutil.NoError(t, err)

	// A clone replays a counter that is not greater than the stored one.
	authr.counter = 0 // next assertion carries 1 again
	hash := sha256.Sum256([]byte("transfer"))
	err = stepUp(t, s, sess, authr, hash[:])
	testutil.ErrorIs(t, err, ErrVerification, "step-up refuses a counter regression")
	testutil.Equal(t, 1, aud.count("auth.clone_warning"), "clone warning audited despite the failure")
	testutil.False(t, aud.has("auth.stepup"), "no step-up recorded")
	testutil.ErrorIs(t, s.RequireStepUp(ctx, sess, hash[:]), ErrStepUpRequired)

	// Plain login still succeeds, but is audited.
	authr.counter = 0
	_, err = login(t, s, authr)
	testutil.NoError(t, err)
	testutil.Equal(t, 2, aud.count("auth.clone_warning"))
	testutil.Equal[any](t, true, aud.last("auth.login")["clone_warning"])
}

func TestCounterZero_SyncedPasskeyAllowed(t *testing.T) {
	s, aud := newTestService(t)
	authr := newSoftAuthenticator(t)
	authr.zeroCount = true
	sess := registerWith(t, s, "xena", authr)
	_, err := login(t, s, authr)
	testutil.NoError(t, err)
	hash := sha256.Sum256([]byte("transfer"))
	testutil.NoError(t, stepUp(t, s, sess, authr, hash[:]))
	testutil.NoError(t, stepUp(t, s, sess, authr, hash[:]))
	testutil.False(t, aud.has("auth.clone_warning"), "counter 0 is not a regression")
}

func TestFinishLogin_ExpiredChallenge(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	_, authr := register(t, s, "carol")

	options, cid, err := s.BeginLogin(ctx)
	testutil.NoError(t, err)
	_, err = s.pool.Exec(ctx, `update control.webauthn_challenges set expires_at = now() - interval '1 second' where id=$1`, cid)
	testutil.NoError(t, err)
	_, err = s.FinishLogin(ctx, cid, credentialRequest(authr.get(t, options)))
	testutil.ErrorIs(t, err, ErrChallengeNotFound)
}

func TestFinishLogin_BadSignatureRejected(t *testing.T) {
	s, _ := newTestService(t)
	_, authr := register(t, s, "dave")

	// A different key claiming the same credential id must not log in.
	imposter := newSoftAuthenticator(t)
	imposter.credID, imposter.userHandle = authr.credID, authr.userHandle
	_, err := login(t, s, imposter)
	testutil.ErrorIs(t, err, ErrVerification)
}

func TestNew_OriginPolicy(t *testing.T) {
	pool := db.TestPool(t)
	base := func() Config {
		return Config{RPID: "localhost", RPDisplayName: "Bank", Origins: []string{"https://localhost:8443"}}
	}
	cfg := base()
	cfg.Origins = []string{"https://localhost:8443", "http://localhost:8080"}
	_, err := New(pool, cfg)
	testutil.Error(t, err, "mixed http/https origins are refused")

	cfg.AllowMixedOrigins = true
	s, err := New(pool, cfg)
	testutil.NoError(t, err)
	testutil.True(t, s.secureCookie(), "Secure when any origin is https")

	cfg = base()
	cfg.Origins = []string{"http://localhost:8080", "https://localhost:8443"}
	cfg.AllowMixedOrigins = true
	s, err = New(pool, cfg)
	testutil.NoError(t, err)
	testutil.True(t, s.secureCookie(), "Secure regardless of origin order")

	cfg = Config{RPID: "bank.example", Origins: []string{"https://bank.example"}, AllowTestLogin: true}
	_, err = New(pool, cfg)
	testutil.Error(t, err, "test login refused for a public https origin")

	cfg = base()
	cfg.AllowTestLogin = true
	_, err = New(pool, cfg)
	testutil.NoError(t, err, "test login allowed on https://localhost")
}
