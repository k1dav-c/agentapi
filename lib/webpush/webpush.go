// Package webpush sends Web Push messages (RFC 8030) to a browser's push
// subscription: the payload encrypted to the browser (RFC 8291,
// aes128gcm), and the sender identified by a VAPID key (RFC 8292). The
// push service relays the message without reading it.
package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Subscription is a browser's push subscription, as PushSubscription's
// toJSON() has it: where to send, and the keys to encrypt to.
type Subscription struct {
	Endpoint string `json:"endpoint" doc:"The push service's URL for this browser (https)."`
	Keys     Keys   `json:"keys"`
}

// Keys are a subscription's keys, base64url.
type Keys struct {
	P256dh string `json:"p256dh" doc:"The browser's P-256 public key, uncompressed, base64url."`
	Auth   string `json:"auth" doc:"The browser's 16-byte authentication secret, base64url."`
}

// maxEndpoint is the longest endpoint URL a subscription may have.
const maxEndpoint = 2048

// Validate says whether the subscription can be sent to: an https
// endpoint, a P-256 public key and a 16-byte secret.
func (s Subscription) Validate() error {
	if len(s.Endpoint) > maxEndpoint {
		return errors.New("the endpoint is too long")
	}
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return errors.New("the endpoint must be an https URL")
	}
	if _, err := s.publicKey(); err != nil {
		return err
	}
	if _, err := s.authSecret(); err != nil {
		return err
	}
	return nil
}

func (s Subscription) publicKey() (*ecdh.PublicKey, error) {
	raw, err := decode(s.Keys.P256dh)
	if err != nil {
		return nil, errors.New("keys.p256dh isn't base64url")
	}
	key, err := ecdh.P256().NewPublicKey(raw)
	if err != nil || len(raw) != 65 {
		return nil, errors.New("keys.p256dh isn't an uncompressed P-256 public key")
	}
	return key, nil
}

func (s Subscription) authSecret() ([]byte, error) {
	raw, err := decode(s.Keys.Auth)
	if err != nil || len(raw) != 16 {
		return nil, errors.New("keys.auth isn't a 16-byte base64url secret")
	}
	return raw, nil
}

// decode reads base64url, with or without padding.
func decode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

func encode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// GenerateKey makes a VAPID key.
func GenerateKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// EncodePrivateKey is key's private scalar, base64url, to keep.
func EncodePrivateKey(key *ecdsa.PrivateKey) (string, error) {
	raw, err := key.Bytes()
	if err != nil {
		return "", err
	}
	return encode(raw), nil
}

// ParsePrivateKey reads a key EncodePrivateKey wrote.
func ParsePrivateKey(s string) (*ecdsa.PrivateKey, error) {
	raw, err := decode(s)
	if err != nil {
		return nil, fmt.Errorf("the VAPID key isn't base64url: %w", err)
	}
	return ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
}

// PublicKey is key's public key, uncompressed and base64url: the
// applicationServerKey a browser subscribes with.
func PublicKey(key *ecdsa.PrivateKey) string {
	raw, err := key.PublicKey.Bytes()
	if err != nil {
		// A key GenerateKey or ParsePrivateKey made is on the curve.
		panic(err)
	}
	return encode(raw)
}

// recordSize is the record size the header names. A push message is one
// record, of at most 4096 bytes.
const recordSize = 4096

// MaxPayload is the most a message may hold: a 4096-byte record less the
// header (86 bytes), the tag (16) and the delimiter (1).
const MaxPayload = recordSize - 86 - 16 - 1

// Encrypt encrypts plaintext to the subscription (RFC 8291), with a new
// key and salt: the body of the push request.
func Encrypt(sub Subscription, plaintext []byte) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return encrypt(sub, plaintext, salt, key)
}

func encrypt(sub Subscription, plaintext, salt []byte, key *ecdh.PrivateKey) ([]byte, error) {
	if len(plaintext) > MaxPayload {
		return nil, fmt.Errorf("the payload has %d bytes, more than %d", len(plaintext), MaxPayload)
	}
	uaPublic, err := sub.publicKey()
	if err != nil {
		return nil, err
	}
	auth, err := sub.authSecret()
	if err != nil {
		return nil, err
	}
	shared, err := key.ECDH(uaPublic)
	if err != nil {
		return nil, err
	}
	asPublic := key.PublicKey().Bytes()
	// The shared secret and the authentication secret make the input
	// keying material (RFC 8291 section 3.3).
	prkKey, err := hkdf.Extract(sha256.New, shared, auth)
	if err != nil {
		return nil, err
	}
	keyInfo := "WebPush: info\x00" + string(uaPublic.Bytes()) + string(asPublic)
	ikm, err := hkdf.Expand(sha256.New, prkKey, keyInfo, 32)
	if err != nil {
		return nil, err
	}
	// aes128gcm (RFC 8188 section 2.2).
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	var header bytes.Buffer
	header.Write(salt)
	_ = binary.Write(&header, binary.BigEndian, uint32(recordSize))
	header.WriteByte(byte(len(asPublic)))
	header.Write(asPublic)
	// The last record ends in 0x02, without padding.
	record := append(append([]byte{}, plaintext...), 0x02)
	return gcm.Seal(header.Bytes(), nonce, record, nil), nil
}

// Urgency is how soon the push service should deliver a message (RFC
// 8030 section 5.3), which a phone saving its battery weighs.
type Urgency string

const (
	UrgencyNormal Urgency = "normal"
	UrgencyHigh   Urgency = "high"
)

// Message is what Send sends.
type Message struct {
	Payload []byte
	// TTL is how long the push service keeps the message for a browser
	// that is offline; under a second is 0, delivered now or never.
	TTL time.Duration
	// Urgency is empty for the push service's default (normal).
	Urgency Urgency
	// Topic, when set, replaces a message of the same topic the push
	// service still holds: up to 32 characters of base64url's alphabet.
	Topic string
}

// Sender sends messages as the holder of a VAPID key.
type Sender struct {
	Key *ecdsa.PrivateKey
	// Subject is how a push service may reach the sender: a mailto: or
	// https: URL.
	Subject string
	Client  *http.Client
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	// tokens are the VAPID tokens made, by push service, until an hour
	// before they expire.
	mu     sync.Mutex
	tokens map[string]vapidToken
}

type vapidToken struct {
	token   string
	expires time.Time
}

// ErrGone is the push service's answer for a subscription that ended (404
// or 410): stop sending to it.
var ErrGone = errors.New("the push subscription is gone")

// StatusError is a push service's answer other than success or ErrGone.
type StatusError struct {
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("the push service answered %d", e.Status)
	}
	return fmt.Sprintf("the push service answered %d: %s", e.Status, e.Body)
}

// Send encrypts the message to sub and gives it to sub's push service.
func (s *Sender) Send(ctx context.Context, sub Subscription, msg Message) error {
	if err := sub.Validate(); err != nil {
		return err
	}
	body, err := Encrypt(sub, msg.Payload)
	if err != nil {
		return err
	}
	endpoint, _ := url.Parse(sub.Endpoint)
	token, err := s.token(endpoint.Scheme + "://" + endpoint.Host)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", strconv.Itoa(int(msg.TTL/time.Second)))
	req.Header.Set("Authorization", "vapid t="+token+", k="+PublicKey(s.Key))
	if msg.Urgency != "" {
		req.Header.Set("Urgency", string(msg.Urgency))
	}
	if msg.Topic != "" {
		req.Header.Set("Topic", msg.Topic)
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	text, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	default:
		return &StatusError{Status: resp.StatusCode, Body: strings.TrimSpace(string(text))}
	}
}

// tokenLife is how long a VAPID token is good for; push services take at
// most 24 hours.
const tokenLife = 12 * time.Hour

// token is the VAPID JWT (ES256) for the push service at audience (RFC
// 8292 section 2), made again an hour before it expires.
func (s *Sender) token(audience string) (string, error) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.tokens[audience]; ok && now().Before(cached.expires.Add(-time.Hour)) {
		return cached.token, nil
	}
	expires := now().Add(tokenLife)
	header := encode([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{
		"aud": audience,
		"exp": expires.Unix(),
		"sub": s.Subject,
	})
	if err != nil {
		return "", err
	}
	signed := header + "." + encode(claims)
	digest := sha256.Sum256([]byte(signed))
	r, sig, err := ecdsa.Sign(rand.Reader, s.Key, digest[:])
	if err != nil {
		return "", err
	}
	raw := make([]byte, 64)
	r.FillBytes(raw[:32])
	sig.FillBytes(raw[32:])
	token := signed + "." + encode(raw)
	if s.tokens == nil {
		s.tokens = map[string]vapidToken{}
	}
	s.tokens[audience] = vapidToken{token: token, expires: expires}
	return token, nil
}

// Decrypt reads a body Encrypt wrote, as the browser that holds the
// subscription's private key and secret does: for tests, and for checking
// what a push carries.
func Decrypt(body []byte, key *ecdh.PrivateKey, authSecret []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, errors.New("the body is shorter than its header")
	}
	salt := body[:16]
	idlen := int(body[20])
	if len(body) < 21+idlen {
		return nil, errors.New("the body is shorter than its header")
	}
	asPublic, err := ecdh.P256().NewPublicKey(body[21 : 21+idlen])
	if err != nil {
		return nil, err
	}
	shared, err := key.ECDH(asPublic)
	if err != nil {
		return nil, err
	}
	prkKey, err := hkdf.Extract(sha256.New, shared, authSecret)
	if err != nil {
		return nil, err
	}
	ikm, err := hkdf.Expand(sha256.New, prkKey, "WebPush: info\x00"+string(key.PublicKey().Bytes())+string(asPublic.Bytes()), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	record, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		return nil, err
	}
	end := bytes.LastIndexByte(record, 0x02)
	if end < 0 || len(bytes.Trim(record[end+1:], "\x00")) != 0 {
		return nil, errors.New("the record has no last-record delimiter")
	}
	return record[:end], nil
}
