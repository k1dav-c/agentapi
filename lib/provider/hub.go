package provider

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/instanceapi"
)

// SyncPath is the hub's route where an AgentAPI gets the hub's providers,
// with their keys: a request signed with the hub's secret, as webhook
// events are.
const SyncPath = "/api/providers/sync"

// SyncEvent is the event header of a sync request.
const SyncEvent = "providers.sync"

// SyncRequest is the body of a sync request; Nonce makes each one
// different.
type SyncRequest struct {
	Nonce string `json:"nonce"`
}

// SyncResponse is the answer to a sync request: the providers, keys
// included, sealed with the hub's secret (Seal), so that they don't cross
// a network in the clear when the hub is reached over plain HTTP.
type SyncResponse struct {
	Sealed string `json:"sealed"`
}

// wireProvider is a provider with its key, as Seal seals it.
type wireProvider struct {
	Provider
	APIKey string `json:"api_key,omitempty"`
}

// sealKey derives the key that seals providers from the hub's secret.
func sealKey(secret string) ([]byte, error) {
	return hkdf.Key(sha256.New, []byte(secret), nil, "agentapi providers sync", 32)
}

// Seal encrypts providers, keys included, with secret (AES-256-GCM).
func Seal(secret string, providers []Provider) (string, error) {
	wire := make([]wireProvider, 0, len(providers))
	for _, p := range providers {
		wire = append(wire, wireProvider{Provider: p, APIKey: p.APIKey})
	}
	plain, err := json.Marshal(wire)
	if err != nil {
		return "", err
	}
	aead, err := newAEAD(secret)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	_, _ = rand.Read(nonce)
	return base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, plain, nil)), nil
}

// Open decrypts what Seal sealed with secret.
func Open(secret, sealed string) ([]Provider, error) {
	data, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(secret)
	if err != nil {
		return nil, err
	}
	if len(data) < aead.NonceSize() {
		return nil, errors.New("sealed providers too short")
	}
	plain, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], nil)
	if err != nil {
		return nil, errors.New("the providers aren't sealed with this secret")
	}
	var wire []wireProvider
	if err := json.Unmarshal(plain, &wire); err != nil {
		return nil, err
	}
	out := make([]Provider, 0, len(wire))
	for _, w := range wire {
		p := w.Provider
		p.APIKey = w.APIKey
		out = append(out, p)
	}
	return out, nil
}

func newAEAD(secret string) (cipher.AEAD, error) {
	key, err := sealKey(secret)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// syncInterval is how often an AgentAPI gets the hub's providers again.
const syncInterval = time.Minute

// HubSync keeps a Source's hub providers those of the hub the AgentAPI
// reports to.
type HubSync struct {
	Source *Source
	// Hub returns the hub's webhook URL (…/hooks) and secret, which can
	// change while the server runs (agentapi hub join); an empty URL or
	// secret is no hub.
	Hub    func() (url, secret string)
	Client *http.Client
	Logger *slog.Logger
}

// Run gets the hub's providers every minute, until ctx ends.
func (h *HubSync) Run(ctx context.Context) {
	if h.Logger == nil {
		h.Logger = slog.Default()
	}
	failing := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(syncInterval):
		}
		err := h.Sync(ctx)
		switch {
		case err != nil && err.Error() != failing:
			h.Logger.Warn("Couldn't get the hub's model providers; trying again in a minute", "error", err)
			failing = err.Error()
		case err == nil && failing != "":
			h.Logger.Info("Got the hub's model providers again")
			failing = ""
		}
	}
}

// Sync gets the hub's providers once. Without a hub, or with a hub that
// has no providers route (older than AgentAPI 4.4), there are none.
func (h *HubSync) Sync(ctx context.Context) error {
	if h.Client == nil {
		h.Client = &http.Client{Timeout: 15 * time.Second}
	}
	url, secret := h.Hub()
	if url == "" || secret == "" {
		h.Source.SetHub(nil)
		return nil
	}
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	body, _ := json.Marshal(SyncRequest{Nonce: hex.EncodeToString(nonce)})
	base := strings.TrimSuffix(strings.TrimSuffix(url, "/"), "/hooks")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+SyncPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(instanceapi.HeaderEvent, SyncEvent)
	req.Header.Set(instanceapi.HeaderTimestamp, timestamp)
	req.Header.Set(instanceapi.HeaderSignature, instanceapi.Sign(secret, timestamp, body))
	resp, err := h.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		h.Source.SetHub(nil)
		return nil
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("the hub answered %s", resp.Status)
	}
	var sync SyncResponse
	if err := json.Unmarshal(data, &sync); err != nil {
		return fmt.Errorf("read the hub's answer: %w", err)
	}
	providers, err := Open(secret, sync.Sealed)
	if err != nil {
		return err
	}
	h.Source.SetHub(Valid(providers, h.Logger))
	return nil
}
