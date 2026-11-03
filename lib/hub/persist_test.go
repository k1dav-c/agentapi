package hub

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Changes that come together are one write, within persistInterval; Close
// writes what is pending at once.
func TestPersisterGathersChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.json")
	var writes atomic.Int32
	p := newPersister(path, "test", slog.New(slog.DiscardHandler), func() ([]byte, error) {
		return []byte{byte('0' + writes.Add(1))}, nil
	})
	for range 100 {
		p.mark()
	}
	require.Eventually(t, func() bool { return writes.Load() == 1 }, 3*persistInterval, 10*time.Millisecond)
	time.Sleep(persistInterval / 2)
	assert.Equal(t, int32(1), writes.Load(), "one write for all of them")

	p.mark()
	require.NoError(t, p.close())
	assert.Equal(t, int32(2), writes.Load(), "close writes what is pending")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "2", string(data))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	require.NoError(t, p.close(), "a second close does nothing")
	assert.Equal(t, int32(2), writes.Load())
}

// The file is an envelope with its version; the bare array of a hub of 2.2
// or earlier reads the same.
func TestStateFileFormats(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	path := filepath.Join(t.TempDir(), "instances.json")
	legacy := `[{"id": "dev:1", "url": "http://dev:1", "source": "manual", "token": "its-own", "group": "Work", "last_seen": "2026-11-01T09:00:00Z"}]`
	require.NoError(t, os.WriteFile(path, []byte(legacy), 0o600))

	s, err := newStore(path, logger, quartz.NewMock(t))
	require.NoError(t, err)
	instance, ok := s.get("dev:1")
	require.True(t, ok)
	assert.Equal(t, "its-own", instance.Token)
	assert.Equal(t, "Work", instance.Group)
	backup, err := os.ReadFile(path + ".bak")
	require.NoError(t, err)
	assert.Equal(t, legacy, string(backup), "the file as the hub found it")
	info, err := os.Stat(path + ".bak")
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "it holds tokens")

	_, _ = s.update("dev:1", false, func(i *Instance) { i.Group = "Play" })
	require.NoError(t, s.close())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var file struct {
		Version   int              `json:"version"`
		Instances []map[string]any `json:"instances"`
	}
	require.NoError(t, json.Unmarshal(data, &file))
	assert.Equal(t, stateVersion, file.Version)
	require.Len(t, file.Instances, 1)
	assert.Equal(t, "Play", file.Instances[0]["group"])

	again, err := newStore(path, logger, quartz.NewMock(t))
	require.NoError(t, err)
	defer func() { _ = again.close() }()
	instance, _ = again.get("dev:1")
	assert.Equal(t, "Play", instance.Group)
	backup, err = os.ReadFile(path + ".bak")
	require.NoError(t, err)
	assert.Equal(t, string(data), string(backup))
}

// A file of a newer hub stops the hub, as it was: taken for damaged, it
// would be set aside and the hub would start without its instances.
func TestStateFileOfANewerHub(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instances.json")
	newer := `{"version": 99, "instances": [{"id": "dev:1"}]}`
	require.NoError(t, os.WriteFile(path, []byte(newer), 0o600))
	_, err := New(Config{Token: "t", Secret: "s", StatePath: path})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "newer hub")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, newer, string(data), "left as it was")
	aside, err := filepath.Glob(path + ".corrupt-*")
	require.NoError(t, err)
	assert.Empty(t, aside)
}

func TestDecodeState(t *testing.T) {
	for name, content := range map[string]string{
		"no version":  `{"instances": []}`,
		"not json":    `{"version": 1, "instances": [`,
		"wrong shape": `{"version": 1, "instances": {}}`,
	} {
		_, err := decodeState("f", []byte(content))
		assert.ErrorIs(t, err, errDamaged, name)
	}
	stored, err := decodeState("f", []byte(" \n[]"))
	require.NoError(t, err)
	assert.Empty(t, stored)
}

// The file keeps where the hub last read an instance's summary; a hub
// that starts again follows it there first, not at a URL an event moved
// it to that never answered. A hub before 3.0.1 reads the file too: it
// ignores the field.
func TestStateKeepsTheConfirmedURL(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	path := filepath.Join(t.TempDir(), "instances.json")
	s, err := newStore(path, logger, quartz.NewMock(t))
	require.NoError(t, err)
	_, _ = s.update("dev:1", true, func(i *Instance) {
		i.URL = "http://dev:2"
		i.Source = SourceWebhook
		i.confirmedURL = "http://dev:1"
	})
	_, _ = s.update("dev:9", true, func(i *Instance) {
		i.URL = "http://dev:9"
		i.Source = SourceWebhook
	})
	require.NoError(t, s.close())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var file struct {
		Version   int `json:"version"`
		Instances []struct {
			ID           string `json:"id"`
			URL          string `json:"url"`
			ConfirmedURL string `json:"confirmed_url"`
		} `json:"instances"`
	}
	require.NoError(t, json.Unmarshal(data, &file))
	assert.Equal(t, 1, file.Version, "an older hub still reads it")
	require.Len(t, file.Instances, 2)
	assert.Equal(t, "http://dev:2", file.Instances[0].URL)
	assert.Equal(t, "http://dev:1", file.Instances[0].ConfirmedURL)
	assert.Empty(t, file.Instances[1].ConfirmedURL, "never confirmed")

	again, err := newStore(path, logger, quartz.NewMock(t))
	require.NoError(t, err)
	defer func() { _ = again.close() }()
	instance, _ := again.get("dev:1")
	assert.Equal(t, "http://dev:1", instance.URL)
	assert.Equal(t, ConnectionConnecting, instance.Connection)
	instance, _ = again.get("dev:9")
	assert.Equal(t, "http://dev:9", instance.URL, "one kept by an earlier hub stays where it was")
}
