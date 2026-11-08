package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/demo"
)

func TestNativeFactoryDemo(t *testing.T) {
	t.Parallel()
	factory, err := nativeFactory(instanceapi.AgentTypeCustom, nativeAdapterConfig{Program: "native-demo"})
	require.NoError(t, err)
	adapter, err := factory(native.Resume{})
	require.NoError(t, err)
	assert.True(t, adapter.Capabilities().Steer)
	require.NoError(t, adapter.Close())

	// Only the exact program name selects the demo.
	_, err = nativeFactory(instanceapi.AgentTypeCustom, nativeAdapterConfig{Program: "/usr/bin/native-demo"})
	assert.ErrorContains(t, err, "not a custom agent")
}

func TestDemoPosesAsAnAgent(t *testing.T) {
	t.Parallel()
	for _, agentType := range []instanceapi.AgentType{instanceapi.AgentTypeClaude, instanceapi.AgentTypeCodex, instanceapi.AgentTypePi} {
		factory, err := nativeFactory(agentType, nativeAdapterConfig{Program: "native-demo"})
		require.NoError(t, err)
		adapter, err := factory(native.Resume{})
		require.NoError(t, err)
		_, isDemo := adapter.(*demo.Adapter)
		assert.True(t, isDemo, agentType)
	}
}
