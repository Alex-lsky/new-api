package ratio_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/price_alias"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func restorePricingMaps(t *testing.T) {
	savedRatio := ModelRatio2JSONString()
	savedPrice := ModelPrice2JSONString()
	savedCompletion := CompletionRatio2JSONString()
	savedCache := CacheRatio2JSONString()
	savedAlias := price_alias.PriceAlias2JSONString()
	savedConfig := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		savedConfig[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, UpdateModelRatioByJSONString(savedRatio))
		require.NoError(t, UpdateModelPriceByJSONString(savedPrice))
		require.NoError(t, UpdateCompletionRatioByJSONString(savedCompletion))
		require.NoError(t, UpdateCacheRatioByJSONString(savedCache))
		require.NoError(t, price_alias.UpdatePriceAliasByJSONString(savedAlias))
		require.NoError(t, config.GlobalConfig.LoadFromDB(savedConfig))
	})
}

func setupAliasFixture(t *testing.T) {
	restorePricingMaps(t)
	require.NoError(t, UpdateModelRatioByJSONString(`{"gemini-3.7-flash":0.375,"aliased-zero":0}`))
	require.NoError(t, UpdateModelPriceByJSONString(`{"priced-per-call":0.05}`))
	require.NoError(t, UpdateCompletionRatioByJSONString(`{"gemini-3.7-flash":5}`))
	require.NoError(t, UpdateCacheRatioByJSONString(`{"gemini-3.7-flash":0.1}`))
	require.NoError(t, price_alias.UpdatePriceAliasByJSONString(`{
		"Gemini 3.7 Flash": "gemini-3.7-flash",
		"Sora 2": "priced-per-call",
		"aliased-zero": "gemini-3.7-flash"
	}`))
}

func TestGetModelRatioAliasFallback(t *testing.T) {
	setupAliasFixture(t)

	ratio, ok, matchName := GetModelRatio("Gemini 3.7 Flash")
	require.True(t, ok)
	assert.Equal(t, 0.375, ratio)
	assert.Equal(t, "Gemini 3.7 Flash", matchName)
}

func TestGetModelRatioDirectEntryWinsOverAlias(t *testing.T) {
	setupAliasFixture(t)

	// explicit 0 means free; alias must never override it
	ratio, ok, _ := GetModelRatio("aliased-zero")
	require.True(t, ok)
	assert.Equal(t, float64(0), ratio)
}

func TestGetModelRatioSentinelWithoutAlias(t *testing.T) {
	setupAliasFixture(t)

	ratio, ok, _ := GetModelRatio("totally-unknown-model")
	assert.False(t, ok)
	assert.Equal(t, 37.5, ratio)
}

func TestGetModelPriceAliasFallback(t *testing.T) {
	setupAliasFixture(t)

	price, ok := GetModelPrice("Sora 2", false)
	require.True(t, ok)
	assert.Equal(t, 0.05, price)

	price, ok = GetModelPrice("totally-unknown-model", false)
	assert.False(t, ok)
	assert.Equal(t, float64(-1), price)
}

func TestGetModelRatioOrPriceAliasFallback(t *testing.T) {
	setupAliasFixture(t)

	value, usePrice, exist := GetModelRatioOrPrice("Sora 2")
	require.True(t, exist)
	assert.True(t, usePrice)
	assert.Equal(t, 0.05, value)

	value, usePrice, exist = GetModelRatioOrPrice("Gemini 3.7 Flash")
	require.True(t, exist)
	assert.False(t, usePrice)
	assert.Equal(t, 0.375, value)
}

func TestCompletionAndCacheRatioAliasFallback(t *testing.T) {
	setupAliasFixture(t)

	assert.Equal(t, float64(5), GetCompletionRatio("Gemini 3.7 Flash"))

	cacheRatio, ok := GetCacheRatio("Gemini 3.7 Flash")
	require.True(t, ok)
	assert.Equal(t, 0.1, cacheRatio)

	_, ok = GetCacheRatio("totally-unknown-model")
	assert.False(t, ok)
}

func TestBillingModeAliasFallback(t *testing.T) {
	setupAliasFixture(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"tiered-src":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"tiered-src":"tier(\"base\", p * 2)"}`,
	}))
	require.NoError(t, price_alias.UpdatePriceAliasByJSONString(`{"Tiered Src":"tiered-src"}`))

	assert.Equal(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode("Tiered Src"))
	expr, ok := billing_setting.GetBillingExpr("Tiered Src")
	require.True(t, ok)
	assert.Equal(t, "tier(\"base\", p * 2)", expr)

	assert.Equal(t, billing_setting.BillingModeRatio, billing_setting.GetBillingMode("totally-unknown-model"))
}
