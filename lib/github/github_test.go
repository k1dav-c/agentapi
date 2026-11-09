package github

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A repository is as recent as its last push or change.
func TestRepoLatest(t *testing.T) {
	pushed := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, pushed, Repo{PushedAt: pushed, UpdatedAt: pushed.Add(-time.Hour)}.Latest())
	assert.Equal(t, pushed.Add(time.Hour), Repo{PushedAt: pushed, UpdatedAt: pushed.Add(time.Hour)}.Latest())
	assert.True(t, Repo{}.Latest().IsZero())
}
