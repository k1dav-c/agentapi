package webpush

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The example of RFC 8291 section 5 and Appendix A.
var (
	rfcSubscription = Subscription{
		Endpoint: "https://push.example.net/push/JzLQ3raZJfFBR0aqvOMsLrt54w4rJUsV",
		Keys: Keys{
			P256dh: "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4",
			Auth:   "BTBZMqHH6r4Tts7J_aSIgg",
		},
	}
	rfcSenderPrivate = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	rfcSalt          = "DGv6ra1nlYgDCS1FRnbzlw"
	rfcPlaintext     = "When I grow up, I want to be a watermelon"
	rfcBody          = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27ml" +
		"mlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPT" +
		"pK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

func TestEncryptMatchesRFC8291(t *testing.T) {
	raw, err := decode(rfcSenderPrivate)
	require.NoError(t, err)
	key, err := ecdh.P256().NewPrivateKey(raw)
	require.NoError(t, err)
	salt, err := decode(rfcSalt)
	require.NoError(t, err)

	body, err := encrypt(rfcSubscription, []byte(rfcPlaintext), salt, key)
	require.NoError(t, err)
	assert.Equal(t, rfcBody, encode(body))
}

func TestEncryptRefusesTooLargePayload(t *testing.T) {
	_, err := Encrypt(rfcSubscription, make([]byte, MaxPayload+1))
	require.Error(t, err)
	body, err := Encrypt(rfcSubscription, make([]byte, MaxPayload))
	require.NoError(t, err)
	assert.Len(t, body, recordSize)
}

func TestValidate(t *testing.T) {
	require.NoError(t, rfcSubscription.Validate())
	for name, change := range map[string]func(*Subscription){
		"http endpoint":      func(s *Subscription) { s.Endpoint = "http://push.example.net/x" },
		"no host":            func(s *Subscription) { s.Endpoint = "https:///x" },
		"credentials":        func(s *Subscription) { s.Endpoint = "https://a:b@push.example.net/x" },
		"long endpoint":      func(s *Subscription) { s.Endpoint = "https://push.example.net/" + strings.Repeat("x", maxEndpoint) },
		"bad p256dh":         func(s *Subscription) { s.Keys.P256dh = "not base64!" },
		"short p256dh":       func(s *Subscription) { s.Keys.P256dh = encode([]byte{4, 1, 2}) },
		"bad auth":           func(s *Subscription) { s.Keys.Auth = "!!" },
		"auth not 16":        func(s *Subscription) { s.Keys.Auth = encode(make([]byte, 15)) },
		"empty p256dh":       func(s *Subscription) { s.Keys.P256dh = "" },
		"missing everything": func(s *Subscription) { *s = Subscription{} },
	} {
		t.Run(name, func(t *testing.T) {
			sub := rfcSubscription
			change(&sub)
			require.Error(t, sub.Validate())
		})
	}
	// Padded base64url, as some browsers send, is fine.
	padded := rfcSubscription
	padded.Keys.Auth += "=="
	require.NoError(t, padded.Validate())
}

func TestPrivateKeyRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	require.NoError(t, err)
	encoded, err := EncodePrivateKey(key)
	require.NoError(t, err)
	parsed, err := ParsePrivateKey(encoded)
	require.NoError(t, err)
	assert.True(t, key.Equal(parsed))
	public, err := decode(PublicKey(key))
	require.NoError(t, err)
	assert.Len(t, public, 65)
	assert.Equal(t, byte(4), public[0])

	_, err = ParsePrivateKey("!!")
	require.Error(t, err)
	_, err = ParsePrivateKey(encode(make([]byte, 32)))
	require.Error(t, err, "zero is no private key")
}

// pushService stands for a push service: it checks the request's VAPID
// token and keeps the body.
type pushService struct {
	t      *testing.T
	status int
	got    chan *http.Request
	body   chan []byte
}

func (p *pushService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	p.got <- r
	p.body <- body
	w.WriteHeader(p.status)
	_, _ = w.Write([]byte("why\n"))
}

func TestSend(t *testing.T) {
	service := &pushService{t: t, status: http.StatusCreated, got: make(chan *http.Request, 1), body: make(chan []byte, 1)}
	server := httptest.NewTLSServer(service)
	defer server.Close()

	key, err := GenerateKey()
	require.NoError(t, err)
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	sender := &Sender{Key: key, Subject: "https://example.com", Client: server.Client(), Now: func() time.Time { return now }}
	sub := rfcSubscription
	sub.Endpoint = server.URL + "/push/abc"

	err = sender.Send(context.Background(), sub, Message{Payload: []byte(`{"title":"hi"}`), TTL: 90 * time.Second, Urgency: UrgencyHigh, Topic: "abc"})
	require.NoError(t, err)
	r := <-service.got
	body := <-service.body
	assert.Equal(t, "/push/abc", r.URL.Path)
	assert.Equal(t, "aes128gcm", r.Header.Get("Content-Encoding"))
	assert.Equal(t, "90", r.Header.Get("TTL"))
	assert.Equal(t, "high", r.Header.Get("Urgency"))
	assert.Equal(t, "abc", r.Header.Get("Topic"))
	// The header (86 bytes), the payload and its delimiter, the tag.
	assert.Len(t, body, 86+len(`{"title":"hi"}`)+1+16)

	// vapid t=<jwt>, k=<public key>
	auth := r.Header.Get("Authorization")
	require.True(t, strings.HasPrefix(auth, "vapid t="), auth)
	token, public, ok := strings.Cut(strings.TrimPrefix(auth, "vapid t="), ", k=")
	require.True(t, ok)
	assert.Equal(t, PublicKey(key), public)
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	header, err := decode(parts[0])
	require.NoError(t, err)
	assert.JSONEq(t, `{"typ":"JWT","alg":"ES256"}`, string(header))
	claimsJSON, err := decode(parts[1])
	require.NoError(t, err)
	var claims map[string]any
	require.NoError(t, json.Unmarshal(claimsJSON, &claims))
	assert.Equal(t, server.URL, claims["aud"])
	assert.Equal(t, "https://example.com", claims["sub"])
	assert.InDelta(t, float64(now.Add(12*time.Hour).Unix()), claims["exp"], 0)
	sig, err := decode(parts[2])
	require.NoError(t, err)
	require.Len(t, sig, 64)
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	assert.True(t, ecdsa.Verify(&key.PublicKey, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])))
}

func TestSendAnswers(t *testing.T) {
	for _, tt := range []struct {
		status int
		check  func(t *testing.T, err error)
	}{
		{http.StatusOK, func(t *testing.T, err error) { require.NoError(t, err) }},
		{http.StatusNotFound, func(t *testing.T, err error) { require.ErrorIs(t, err, ErrGone) }},
		{http.StatusGone, func(t *testing.T, err error) { require.ErrorIs(t, err, ErrGone) }},
		{http.StatusTooManyRequests, func(t *testing.T, err error) {
			var status *StatusError
			require.ErrorAs(t, err, &status)
			assert.Equal(t, http.StatusTooManyRequests, status.Status)
			assert.Equal(t, "the push service answered 429: why", err.Error())
		}},
	} {
		service := &pushService{t: t, status: tt.status, got: make(chan *http.Request, 1), body: make(chan []byte, 1)}
		server := httptest.NewTLSServer(service)
		key, err := GenerateKey()
		require.NoError(t, err)
		sub := rfcSubscription
		sub.Endpoint = server.URL + "/push/abc"
		err = (&Sender{Key: key, Subject: "mailto:a@example.com", Client: server.Client()}).Send(context.Background(), sub, Message{Payload: []byte("x")})
		tt.check(t, err)
		server.Close()
	}
}

func TestSendRefusesInvalidSubscription(t *testing.T) {
	key, err := GenerateKey()
	require.NoError(t, err)
	sub := rfcSubscription
	sub.Endpoint = "http://push.example.net/x"
	require.Error(t, (&Sender{Key: key}).Send(context.Background(), sub, Message{}))
}

func TestDecrypt(t *testing.T) {
	raw, err := decode("q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94")
	require.NoError(t, err)
	key, err := ecdh.P256().NewPrivateKey(raw)
	require.NoError(t, err)
	auth, err := decode(rfcSubscription.Keys.Auth)
	require.NoError(t, err)
	body, err := decode(rfcBody)
	require.NoError(t, err)
	plaintext, err := Decrypt(body, key, auth)
	require.NoError(t, err)
	assert.Equal(t, rfcPlaintext, string(plaintext))

	// And a body of Encrypt's own.
	body, err = Encrypt(rfcSubscription, []byte("hello"))
	require.NoError(t, err)
	plaintext, err = Decrypt(body, key, auth)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(plaintext))

	body[len(body)-1] ^= 1
	_, err = Decrypt(body, key, auth)
	require.Error(t, err)
	_, err = Decrypt(body[:10], key, auth)
	require.Error(t, err)
}
