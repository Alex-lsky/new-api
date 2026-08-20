package relay

import (
	"context"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/tool_hosting"
)

// providerToEmulatedBackend converts a global tool-hosting provider into an
// executable emulated backend. Channel references stay unresolved here — the
// credentials are looked up per execution (so multi-key channels pick a healthy
// key each call).
func providerToEmulatedBackend(provider tool_hosting.ToolHostingProvider) *dto.EmulatedToolBackend {
	return &dto.EmulatedToolBackend{
		Provider:  provider.Type,
		ChannelID: provider.ChannelID,
		Model:     provider.Model,
		APIKey:    provider.APIKey,
		APIBase:   provider.APIBase,
		Extra:     provider.Extra,
	}
}

// resolveEmulatedToolRefs replaces channel backends that reference a named
// global provider by name (EmulatedToolBackend.Ref) with the provider's
// concrete configuration. Unknown or kind-mismatched references are dropped so
// a broken reference degrades to "tool left to upstream", never a request
// failure.
func resolveEmulatedToolRefs(backends map[string]*dto.EmulatedToolBackend) map[string]*dto.EmulatedToolBackend {
	if len(backends) == 0 {
		return backends
	}
	resolved := make(map[string]*dto.EmulatedToolBackend, len(backends))
	for kind, backend := range backends {
		ref := strings.TrimSpace(backend.Ref)
		if ref == "" {
			resolved[kind] = backend
			continue
		}
		provider, ok := tool_hosting.GetProvider(ref)
		if !ok || string(provider.Kind) != kind {
			continue
		}
		resolved[kind] = providerToEmulatedBackend(provider)
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

// withResolvedChannelCreds returns a copy of the backend whose APIKey/APIBase
// are filled from the referenced channel when backend.ChannelID is set (the
// executor type itself stays provider-driven). Reviews of the original backend
// keep the inline values.
func withResolvedChannelCreds(ctx context.Context, backend *dto.EmulatedToolBackend) (*dto.EmulatedToolBackend, error) {
	if backend.ChannelID <= 0 {
		return backend, nil
	}
	key, apiBase, err := channelCredentials(ctx, backend.ChannelID)
	if err != nil {
		return nil, err
	}
	copy := *backend
	copy.APIKey = key
	copy.APIBase = apiBase
	return &copy, nil
}

// channelCredentials resolves a channel's key and base_url.
func channelCredentials(ctx context.Context, channelID int64) (string, string, error) {
	if channelID <= 0 {
		return "", "", fmt.Errorf("channel-referenced backend has no channel_id")
	}
	channel, err := model.CacheGetChannel(int(channelID))
	if err != nil {
		return "", "", fmt.Errorf("referenced channel %d unavailable: %w", channelID, err)
	}
	key, _, apiErr := channel.GetNextEnabledKey()
	if apiErr != nil {
		return "", "", fmt.Errorf("referenced channel %d has no available key: %v", channelID, apiErr)
	}
	return key, channel.GetBaseURL(), nil
}

// resolveEmulatedBackend fills in channel-referenced credentials (APIKey /
// APIBase) at execution time for a possibly-legacy provider=="channel"
// backend. For the legacy shape the standard executor is taken from
// EmulatedToolBackend.Executor.
func resolveEmulatedBackend(ctx context.Context, backend *dto.EmulatedToolBackend) (string, string, error) {
	if backend == nil {
		return "", "", fmt.Errorf("emulated tool backend is nil")
	}
	provider := strings.ToLower(strings.TrimSpace(backend.Provider))
	if provider != dto.EmulatedChannelProvider {
		return backend.APIKey, backend.APIBase, nil
	}
	return channelCredentials(ctx, backend.ChannelID)
}

// emulatedToolKindForFunction maps an emulated function name to its tool kind.
func emulatedToolKindForFunction(name string) (string, bool) {
	switch name {
	case emulatedWebSearchFunctionName:
		return dto.EmulatedToolTypeWebSearch, true
	case emulatedRecognitionFunctionName:
		return dto.EmulatedToolTypeRecognition, true
	case emulatedImageFunctionName:
		return dto.EmulatedToolTypeImage, true
	default:
		return "", false
	}
}

// emulatedTypeSetFromBackends derives the rewrite/injection set from the
// effective emulated backends (channel-local, references resolved).
func emulatedTypeSetFromBackends(backends map[string]*dto.EmulatedToolBackend) map[string]struct{} {
	if len(backends) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(backends))
	for kind := range backends {
		set[kind] = struct{}{}
	}
	return set
}
