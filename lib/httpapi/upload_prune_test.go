package httpapi

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/transcript"
)

// ageUpload makes the files of an upload, and its directory, older than
// uploadGrace, as if they were stored before AgentAPI started.
func ageUpload(t *testing.T, store *uploadStore, file string) {
	t.Helper()
	old := time.Now().Add(-uploadGrace - time.Hour)
	dir := filepath.Dir(file)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		require.NoError(t, os.Chtimes(filepath.Join(dir, entry.Name()), old, old))
	}
	require.NoError(t, os.Chtimes(dir, old, old))
	store.mu.Lock()
	delete(store.saved, filepath.Base(dir))
	store.mu.Unlock()
}

// saveOld stores data as name and ages it.
func saveOld(t *testing.T, store *uploadStore, name, data string) string {
	t.Helper()
	path, _, err := store.save(name, []byte(data))
	require.NoError(t, err)
	ageUpload(t, store, path)
	return path
}

// The uploads the conversation refers to, by path or URL, in a message, a
// rich message or the queue, are kept, as are new ones; old ones nothing
// refers to are removed. Nothing that save doesn't make is touched, and
// symlinks aren't followed.
func TestPruneUploads(t *testing.T) {
	srv, _ := newQuietNativeServer(t, nil)
	store := srv.uploads

	inMessage := saveOld(t, store, "message.txt", "in a message")
	sendUserMessage(t, srv, `look at @"`+inMessage+`"`)
	inQueue := saveOld(t, store, "queued.txt", "in the queue")
	srv.mu.Lock()
	srv.queue.Push(`and @"`+inQueue+`"`, time.Now(), false)
	srv.mu.Unlock()
	inToolInput := saveOld(t, store, "read.txt", "read by the agent")
	png := "\x89PNG\r\n\x1a\n" + string(make([]byte, 16))
	imageURL, err := store.saveImage([]byte(png))
	require.NoError(t, err)
	ageUpload(t, store, filepath.Join(store.dir, filepath.FromSlash(imageURL[len("/uploads/"):])))
	srv.emitter.EmitRichMessageSnapshot(transcript.RichMessage{MessageID: "a1", Role: "assistant", Content: []transcript.RichContentBlock{
		{Type: "tool_use", ToolUseID: "t1", ToolName: "Read", ToolInput: []byte(`{"file_path":"` + inToolInput + `"}`)},
		{Type: "tool_result", ToolUseID: "t1", Images: []transcript.RichImage{{MediaType: "image/png", URL: imageURL}}},
	}})

	unused := saveOld(t, store, "unused.txt", "nothing refers to this")
	young, _, err := store.save("young.txt", []byte("just uploaded"))
	require.NoError(t, err)
	// Uploaded again after a restart: the file was there already, and is
	// about to be referred to.
	again := saveOld(t, store, "again.txt", "uploaded twice")
	_, _, err = store.save("again.txt", []byte("uploaded twice"))
	require.NoError(t, err)

	// What AgentAPI doesn't make: a directory with another name, a
	// symlink named like a checksum to a directory outside, a checksum
	// directory holding a symlink to a file outside.
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "keep.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("outside"), 0o600))
	other := filepath.Join(store.dir, "other")
	require.NoError(t, os.Mkdir(other, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(other, "x"), []byte("x"), 0o600))
	link := filepath.Join(store.dir, "0123456789abcdef")
	require.NoError(t, os.Symlink(outside, link))
	withLink := filepath.Join(store.dir, "fedcba9876543210")
	require.NoError(t, os.Mkdir(withLink, 0o700))
	require.NoError(t, os.Symlink(outsideFile, filepath.Join(withLink, "keep.txt")))
	old := time.Now().Add(-uploadGrace - time.Hour)
	for _, p := range []string{outsideFile, outside, filepath.Join(other, "x"), other, withLink} {
		require.NoError(t, os.Chtimes(p, old, old))
	}

	srv.pruneUploads(time.Now())

	for _, kept := range []string{inMessage, inQueue, inToolInput, young, again, filepath.Join(other, "x"), outsideFile} {
		assert.FileExists(t, kept)
	}
	assert.FileExists(t, filepath.Join(store.dir, filepath.FromSlash(imageURL[len("/uploads/"):])))
	assert.NoFileExists(t, unused)
	assert.NoDirExists(t, filepath.Dir(unused))
	_, err = os.Lstat(link)
	assert.NoError(t, err, "the symlink is left alone")
	_, err = os.Lstat(filepath.Join(withLink, "keep.txt"))
	assert.NoError(t, err)
}

// Clearing the conversation removes the old uploads only it referred to.
func TestResetPrunesUploads(t *testing.T) {
	srv, _ := newQuietNativeServer(t, nil)
	file := saveOld(t, srv.uploads, "notes.txt", "old notes")
	sendUserMessage(t, srv, `@"`+file+`"`)
	srv.pruneUploads(time.Now())
	require.FileExists(t, file)

	_, err := srv.deleteMessages(t.Context(), nil)
	require.NoError(t, err)
	assert.Eventually(t, func() bool {
		_, err := os.Stat(file)
		return os.IsNotExist(err)
	}, 5*time.Second, 10*time.Millisecond)
}

func TestUploadRefsScan(t *testing.T) {
	t.Parallel()
	refs := newUploadRefs([]string{"0123456789abcdef", "aaaaaaaaaaaaaaaa", "1111111111111111"})
	refs.scan(`/x.uploads/0123456789abcdef/a.png`)
	// Inside a longer run of hex digits, at any offset.
	refs.scan(`deadbeefaaaaaaaaaaaaaaaab`)
	refs.scan(`111111111111111`) // One digit short.
	assert.Equal(t, map[string]bool{"0123456789abcdef": true, "aaaaaaaaaaaaaaaa": true}, refs.found)
}
