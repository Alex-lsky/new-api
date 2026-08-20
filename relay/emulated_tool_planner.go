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
	backend := &dto.EmulatedToolBackend{
		Model:   provider.Model,
		APIKey:  provider.APIKey,
		APIBase: provider.APIBase,
		Extra:   provider.Extra,
	}
	if strings.EqualFold(provider.Type, dto.EmulatedChannelProvider) {
		backend.Provider = dto.EmulatedChannelProvider
		backend.ChannelID = provider.ChannelID
		backend.Executor = provider.Executor
		return backend
	}
	backend.Provider = provider.Type
	return backend
}

// resolveEmulatedBackend fills in channel-referenced credentials (APIKey /
// APIBase) at execution time. Backends configured inline are returned as-is.
func resolveEmulatedBackend(ctx context.Context, backend *dto.EmulatedToolBackend) (string, string, error) {
	if backend == nil {
		return "", "", fmt.Errorf("emulated tool backend is nil")
	}
	if !strings.EqualFold(strings.TrimSpace(backend.Provider), dto.EmulatedChannelProvider) {
		return backend.APIKey, backend.APIBase, nil
	}
	if backend.ChannelID <= 0 {
		return "", "", fmt.Errorf("channel-referenced backend has no channel_id")
	}
	channel, err := model.CacheGetChannel(int(backend.ChannelID))
	if err != nil {
		return "", "", fmt.Errorf("referenced channel %d unavailable: %w", backend.ChannelID, err)
	}
	key, _, apiErr := channel.GetNextEnabledKey()
	if apiErr != nil {
		return "", "", fmt.Errorf("referenced channel %d has no available key: %v", backend.ChannelID, apiErr)
	}
	return key, channel.GetBaseURL(), nil
}

// mergeGlobalToolHosting extends the channel's local emulated backends with
// global tool hosting bindings resolved for modelName. Precedence per tool
// kind: a backend pinned by the channel wins over a global binding; stripped
// and excluded kinds on the channel never get global emulation. Returns the
// merged map (nil when nothing is emulated).
func mergeGlobalToolHosting(local map[string]*dto.EmulatedToolBackend, stripSet map[string]struct{}, excludeSet map[string]struct{}, inherit bool, modelName string) map[string]*dto.EmulatedToolBackend {
	if len(local) == 0 && !inherit {
		return nil
	}
	backends := make(map[string]*dto.EmulatedToolBackend, len(local)+3)
	for kind, backend := range local {
		backends[kind] = backend
	}
	if !inherit {
		if len(backends) == 0 {
			return nil
		}
		return backends
	}
	for _, kind := range []string{dto.EmulatedToolTypeWebSearch, dto.EmulatedToolTypeRecognition, dto.EmulatedToolTypeImage} {
		if _, pinnedByChannel := backends[kind]; pinnedByChannel {
			continue
		}
		if _, stripped := stripSet[kind]; stripped {
			continue
		}
		if _, excluded := excludeSet[kind]; excluded {
			continue
		}
		providerName := tool_hosting.LookupBinding(tool_hosting.ToolKind(kind), modelName)
		if providerName == "" {
			continue
		}
		provider, ok := tool_hosting.GetProvider(providerName)
		if !ok {
			continue
		}
		backends[kind] = providerToEmulatedBackend(provider)
	}
	if len(backends) == 0 {
		return nil
	}
	return backends
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
// effective emulated backends (channel-local plus inheritable global).
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
