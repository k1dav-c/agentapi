package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
)

func TestNativeFactoryDemo(t *testing.T) {
	t.Parallel()
	factory, err := nativeFactory(AgentTypeCustom, nativeAdapterConfig{Program: "native-demo"})
	require.NoError(t, err)
	adapter, err := factory(native.Resume{})
	require.NoError(t, err)
	assert.True(t, adapter.Capabilities().Steer)
	require.NoError(t, adapter.Close())

	// Only the exact program name with the custom type selects the demo.
	_, err = nativeFactory(AgentTypeCustom, nativeAdapterConfig{Program: "/usr/bin/native-demo"})
	assert.Error(t, err)
	_, err = nativeFactory(AgentTypeGoose, nativeAdapterConfig{Program: "native-demo"})
	assert.Error(t, err)
}
