package nativetest

import (
	"net/url"

	"github.com/coder/agentapi/lib/providerproxy"
)

// ProxyToken is the token a ProviderProxy gives agents.
const ProxyToken = "proxy-token"

// ProviderProxy is a native.ProviderProxy that doesn't listen: a
// provider's endpoint is http://127.0.0.1:1/<id>/..., with ProxyToken.
// With Err set it can't start.
type ProviderProxy struct {
	Err error
}

// Endpoint implements native.ProviderProxy.
func (p ProviderProxy) Endpoint(id string) (providerproxy.Endpoint, error) {
	if p.Err != nil {
		return providerproxy.Endpoint{}, p.Err
	}
	base := "http://127.0.0.1:1/" + url.PathEscape(id)
	return providerproxy.Endpoint{AnthropicBaseURL: base + "/anthropic", OpenAIBaseURL: base + "/openai", Token: ProxyToken}, nil
}
