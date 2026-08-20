package price_alias

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func restoreAliasMap(t *testing.T) {
	t.Cleanup(func() {
		require.NoError(t, UpdatePriceAliasByJSONString(PriceAlias2JSONString()))
	})
}

func TestResolveModelAlias(t *testing.T) {
	restoreAliasMap(t)
	require.NoError(t, UpdatePriceAliasByJSONString(`{
		"Gemini 3.7 Flash": "gemini-3.7-flash",
		"alias-a": "alias-b",
		"alias-b": "alias-c",
		"self-loop": "self-loop",
		"cycle-a": "cycle-b",
		"cycle-b": "cycle-a"
	}`))

	tests := []struct {
		name       string
		input      string
		wantTarget string
		wantOK     bool
	}{
		{"no alias entry returns miss", "gemini-3.7-flash", "", false},
		{"empty name returns miss", "", "", false},
		{"single hop resolves", "Gemini 3.7 Flash", "gemini-3.7-flash", true},
		{"chain resolves to terminal", "alias-a", "alias-c", true},
		{"mid-chain entry resolves to terminal", "alias-b", "alias-c", true},
		{"self reference terminates as miss", "self-loop", "", false},
		{"two-node cycle terminates as miss", "cycle-a", "", false},
		{"cycle entered from outside still resolves elsewhere", "cycle-b", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, ok := ResolveModelAlias(tt.input)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantTarget, target)
		})
	}
}

func TestUpdatePriceAliasByJSONString(t *testing.T) {
	restoreAliasMap(t)

	require.NoError(t, UpdatePriceAliasByJSONString(`{"a":"b"}`))
	source, ok := GetPriceAliasSource("a")
	assert.True(t, ok)
	assert.Equal(t, "b", source)

	copy := GetPriceAliasCopy()
	assert.Equal(t, map[string]string{"a": "b"}, copy)
	assert.Equal(t, `{"a":"b"}`, PriceAlias2JSONString())

	_, ok = GetPriceAliasSource("missing")
	assert.False(t, ok)

	require.Error(t, UpdatePriceAliasByJSONString(`{invalid`))
	// reload clears the map before parsing, matching the other ratio maps
	_, ok = GetPriceAliasSource("a")
	assert.False(t, ok)
}
