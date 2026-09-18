package termexec

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSwapWakesScreenConsumer(t *testing.T) {
	old := &Process{screenUpdates: make(chan struct{}, 1)}
	next := &Process{screenUpdates: make(chan struct{}, 1)}
	s := NewSwappableProcess(old)
	waiting := s.ScreenUpdates()
	s.Set(next)
	select {
	case <-waiting:
	default:
		t.Fatal("consumer on old process was not woken")
	}
	require.Equal(t, next.ScreenUpdates(), s.ScreenUpdates())
	select {
	case <-s.ScreenUpdates():
	default:
		t.Fatal("replacement screen was not announced")
	}
	for range 100 {
		next.notifyScreenUpdate()
	}
	require.Len(t, next.screenUpdates, 1)
}
