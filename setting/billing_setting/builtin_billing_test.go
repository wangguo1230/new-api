package billing_setting_test

import (
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGemini4HydrogenBuiltinBilling(t *testing.T) {
	expression, ok := billing_setting.GetBuiltinBillingExpr("gemini-4-hydrogen")
	require.True(t, ok)
	xhighExpression, ok := billing_setting.GetBuiltinBillingExpr("gemini-4-hydrogen-xhigh")
	require.True(t, ok)
	assert.Equal(t, expression, xhighExpression)

	usage := &dto.Usage{
		PromptTokens:        1000,
		CompletionTokens:    100,
		PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 400},
	}
	params := service.BuildTieredTokenParams(usage, false, billingexpr.UsedVars(expression))
	result, err := billingexpr.ComputeTieredQuota(&billingexpr.BillingSnapshot{
		ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), GroupRatio: 1, QuotaPerUnit: 500000,
	}, params)
	require.NoError(t, err)
	assert.Equal(t, 840, result.ActualQuotaAfterGroup)
}

func TestGemini4CarbonBuiltinBilling(t *testing.T) {
	expression, ok := billing_setting.GetBuiltinBillingExpr("gemini-4-carbon")
	require.True(t, ok)

	cutover := time.Date(2026, time.December, 1, 8, 0, 0, 0, time.UTC)
	usage := &dto.Usage{
		PromptTokens:        1000,
		CompletionTokens:    100,
		PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 400},
	}
	params := service.BuildTieredTokenParams(usage, false, billingexpr.UsedVars(expression))
	for _, tc := range []struct {
		at   time.Time
		tier string
		cost float64
	}{
		{cutover.Add(-time.Nanosecond), "introductory", 2240},
		{cutover, "standard", 4480},
	} {
		t.Run(tc.tier, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				time.Sleep(time.Until(tc.at))
				cost, trace, err := billingexpr.RunExpr(expression, params)
				require.NoError(t, err)
				assert.Equal(t, tc.tier, trace.MatchedTier)
				assert.Equal(t, tc.cost, cost)
			})
		})
	}
}

func TestClaudeFable52BuiltinBilling(t *testing.T) {
	const modelName = "claude-fable-5-2"
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	saved := *settings
	savedRatios, savedPrices := ratio_setting.ModelRatio2JSONString(), ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		*settings = saved
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedRatios))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedPrices))
	})
	*settings = billing_setting.BillingSetting{BillingMode: map[string]string{}, BillingExpr: map[string]string{}}
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	assert.Equal(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode(modelName))
	expression, ok := billing_setting.GetBillingExpr(modelName)
	require.True(t, ok)

	for _, tc := range []struct {
		name                                    string
		input, output, cached, write5m, write1h int
		quota                                   int
	}{
		{"input", 1000, 0, 0, 0, 0, 4000},
		{"output", 0, 1000, 0, 0, 0, 20000},
		{"cache read", 0, 0, 1000, 0, 0, 100},
		{"5m cache write", 0, 0, 0, 1000, 0, 5000},
		{"1h cache write", 0, 0, 0, 0, 1000, 8000},
		{"mixed cache TTLs", 1000, 100, 2000, 3000, 4000, 53200},
		{"200k context", 1000, 100, 199000, 0, 0, 25900},
		{"above 200k context", 1001, 100, 199000, 0, 0, 25904},
		{"1M context", 1000, 100, 999000, 0, 0, 105900},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := &dto.Usage{
				PromptTokens: tc.input, CompletionTokens: tc.output, UsageSemantic: "anthropic",
				PromptTokensDetails:         dto.InputTokenDetails{CachedTokens: tc.cached, CachedCreationTokens: tc.write5m + tc.write1h},
				ClaudeCacheCreation5mTokens: tc.write5m, ClaudeCacheCreation1hTokens: tc.write1h,
			}
			params := service.BuildTieredTokenParams(usage, true, billingexpr.UsedVars(expression))
			result, err := billingexpr.ComputeTieredQuota(&billingexpr.BillingSnapshot{
				ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), GroupRatio: 1, QuotaPerUnit: 500000,
			}, params)
			require.NoError(t, err)
			assert.Equal(t, tc.quota, result.ActualQuotaAfterGroup)
		})
	}

	for _, tc := range []struct {
		name, mode, expr, ratios, prices, wantMode string
	}{
		{"custom expression", "tiered_expr", `tier("custom", p * 7)`, `{}`, `{}`, "tiered_expr"},
		{"explicit ratio mode", "ratio", "", `{}`, `{}`, "ratio"},
		{"existing free token price", "", "", `{"claude-fable-5-2":0}`, `{}`, "ratio"},
		{"existing per-call price", "", "", `{}`, `{"claude-fable-5-2":0.1}`, "ratio"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			*settings = billing_setting.BillingSetting{BillingMode: map[string]string{}, BillingExpr: map[string]string{}}
			if tc.mode != "" {
				settings.BillingMode[modelName] = tc.mode
			}
			if tc.expr != "" {
				settings.BillingExpr[modelName] = tc.expr
			}
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(tc.ratios))
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(tc.prices))
			assert.Equal(t, tc.wantMode, billing_setting.GetBillingMode(modelName))
			actual, ok := billing_setting.GetBillingExpr(modelName)
			assert.Equal(t, tc.expr, actual)
			assert.Equal(t, tc.expr != "", ok)
		})
	}
}

func TestGPT6AstraBuiltinBilling(t *testing.T) {
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	saved := *settings
	savedRatios, savedPrices := ratio_setting.ModelRatio2JSONString(), ratio_setting.ModelPrice2JSONString()
	savedOptions := common.OptionMap
	t.Cleanup(func() {
		*settings, common.OptionMap = saved, savedOptions
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedRatios))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedPrices))
	})
	common.OptionMap = map[string]string{"billing_setting.billing_mode": `{}`, "billing_setting.billing_expr": `{}`}
	require.NoError(t, config.GlobalConfig.LoadFromDB(common.OptionMap))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	assert.Equal(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode("gpt-6-astra"))
	expression, ok := billing_setting.GetBillingExpr("gpt-6-astra")
	require.True(t, ok)

	for _, tc := range []struct {
		name                                       string
		input, output, cached, written, written12h int
		request                                    string
		quota                                      int
	}{
		{"standard", 1000, 100, 0, 0, 0, `{}`, 7500},
		{"client flex cannot discount standard pricing", 1000, 100, 0, 0, 0, `{"service_tier":"flex"}`, 7500},
		{"cache at context boundary", 272000, 1000, 200000, 20000, 0, `{}`, 510000},
		{"whole request above boundary", 272001, 1000, 200000, 20000, 0, `{}`, 1007510},
		// The prompt caching guide example: 4000 of the 7000 writes are 12h.
		{"12h cache writes", 12000, 0, 4000, 7000, 4000, `{}`, 75750},
		{"12h cache writes above boundary", 300000, 0, 200000, 20000, 10000, `{}`, 1375000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := &dto.Usage{
				PromptTokens: tc.input, CompletionTokens: tc.output,
				PromptTokensDetails: dto.InputTokenDetails{CachedTokens: tc.cached, CacheWriteTokens: tc.written,
					CacheWriteTokensDetails: &dto.CacheWriteTokenDetails{TTL30mTokens: tc.written - tc.written12h, TTL12hTokens: tc.written12h}},
			}
			params := service.BuildTieredTokenParams(usage, false, billingexpr.UsedVars(expression))
			result, err := billingexpr.ComputeTieredQuotaWithRequest(&billingexpr.BillingSnapshot{
				ExprString: expression, GroupRatio: 1, QuotaPerUnit: 500000,
			}, params, billingexpr.RequestInput{Body: []byte(tc.request)})
			require.NoError(t, err)
			assert.Equal(t, tc.quota, result.ActualQuotaAfterGroup)
		})
	}

	t.Run("admin options expose defaults without persisting them", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		controller.GetOptions(ctx)
		var response struct {
			Success bool
			Data    []model.Option
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		require.True(t, response.Success)
		found := map[string]string{}
		for _, option := range response.Data {
			if _, ok := common.OptionMap[option.Key]; ok {
				assert.NotContains(t, found, option.Key)
				var values map[string]string
				require.NoError(t, common.UnmarshalJsonStr(option.Value, &values))
				found[option.Key] = values["gpt-6-astra"]
				assert.Equal(t, `{}`, common.OptionMap[option.Key])
			}
		}
		assert.Equal(t, map[string]string{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": expression}, found)
	})

	for _, tc := range []struct {
		name, mode, expr, ratios, prices, wantMode string
	}{
		{"custom expression overrides legacy price", "tiered_expr", "p * 7", `{"gpt-6-astra":8}`, `{}`, "tiered_expr"},
		{"explicit ratio mode", "ratio", "", `{}`, `{}`, "ratio"},
		{"existing free token price", "", "", `{"gpt-6-astra":0}`, `{}`, "ratio"},
		{"existing per-call price", "", "", `{}`, `{"gpt-6-astra":0.1}`, "ratio"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			*settings = billing_setting.BillingSetting{BillingMode: map[string]string{}, BillingExpr: map[string]string{}}
			if tc.mode != "" {
				settings.BillingMode["gpt-6-astra"] = tc.mode
			}
			if tc.expr != "" {
				settings.BillingExpr["gpt-6-astra"] = tc.expr
			}
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(tc.ratios))
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(tc.prices))
			assert.Equal(t, tc.wantMode, billing_setting.GetBillingMode("gpt-6-astra"))
			actual, ok := billing_setting.GetBillingExpr("gpt-6-astra")
			assert.Equal(t, tc.expr, actual)
			assert.Equal(t, tc.expr != "", ok)
		})
	}
}

func TestGPT62BuiltinBilling(t *testing.T) {
	for _, modelCase := range []struct {
		name           string
		standardQuota  int
		boundaryQuota  int
		longQuota      int
		writeQuota     int
		longWriteQuota int
	}{
		{name: "gpt-6.2-astra", standardQuota: 6000, boundaryQuota: 368000, longQuota: 726008, writeQuota: 59800, longWriteQuota: 1020000},
		{name: "gpt-6.2-sol", standardQuota: 1500, boundaryQuota: 92000, longQuota: 181502, writeQuota: 14950, longWriteQuota: 255000},
	} {
		t.Run(modelCase.name, func(t *testing.T) {
			expression, ok := billing_setting.GetBuiltinBillingExpr(modelCase.name)
			require.True(t, ok)

			for _, usageCase := range []struct {
				name                                       string
				input, output, cached, written, written12h int
				quota                                      int
			}{
				{name: "standard", input: 1000, output: 100, quota: modelCase.standardQuota},
				{name: "cache at context boundary", input: 272000, output: 1000, cached: 200000, written: 20000, quota: modelCase.boundaryQuota},
				{name: "whole request above boundary", input: 272001, output: 1000, cached: 200000, written: 20000, quota: modelCase.longQuota},
				{name: "12h cache writes", input: 12000, cached: 4000, written: 7000, written12h: 4000, quota: modelCase.writeQuota},
				{name: "12h cache writes above boundary", input: 300000, cached: 200000, written: 20000, written12h: 10000, quota: modelCase.longWriteQuota},
			} {
				t.Run(usageCase.name, func(t *testing.T) {
					usage := &dto.Usage{
						PromptTokens: usageCase.input, CompletionTokens: usageCase.output,
						PromptTokensDetails: dto.InputTokenDetails{
							CachedTokens: usageCase.cached, CacheWriteTokens: usageCase.written,
							CacheWriteTokensDetails: &dto.CacheWriteTokenDetails{
								TTL30mTokens: usageCase.written - usageCase.written12h,
								TTL12hTokens: usageCase.written12h,
							},
						},
					}
					params := service.BuildTieredTokenParams(usage, false, billingexpr.UsedVars(expression))
					result, err := billingexpr.ComputeTieredQuotaWithRequest(&billingexpr.BillingSnapshot{
						ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), GroupRatio: 1, QuotaPerUnit: 500000,
					}, params, billingexpr.RequestInput{})
					require.NoError(t, err)
					assert.Equal(t, usageCase.quota, result.ActualQuotaAfterGroup)
				})
			}
		})
	}
}

func TestImageModelBuiltinPricesAndOverrides(t *testing.T) {
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	saved := *settings
	savedRatios, savedPrices := ratio_setting.ModelRatio2JSONString(), ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		*settings = saved
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedRatios))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedPrices))
	})
	for _, name := range []string{"gpt-image-2", "gpt-image-2.5-sunburst", "gpt-image-2.5-flare"} {
		t.Run(name, func(t *testing.T) {
			*settings = billing_setting.BillingSetting{BillingMode: map[string]string{}, BillingExpr: map[string]string{}}
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{}`))
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
			expression, ok := billing_setting.GetBillingExpr(name)
			require.True(t, ok)
			usage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 100, PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens: 300, ImageTokens: 600, CachedTokensDetails: &dto.CachedTokenDetails{ImageTokens: common.GetPointer(200)},
			}}
			result, err := billingexpr.ComputeTieredQuota(&billingexpr.BillingSnapshot{ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), GroupRatio: 1, QuotaPerUnit: 500000},
				service.BuildTieredTokenParams(usage, false, billingexpr.UsedVars(expression)))
			require.NoError(t, err)
			assert.Equal(t, 4113, result.ActualQuotaAfterGroup)
			encoded, err := common.Marshal(map[string]float64{name: 0})
			require.NoError(t, err)
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(encoded)))
			assert.Equal(t, "ratio", billing_setting.GetBillingMode(name))
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{}`))
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(encoded)))
			assert.Equal(t, "ratio", billing_setting.GetBillingMode(name))
			settings.BillingMode[name] = "tiered_expr"
			settings.BillingExpr[name] = `tier("custom", p * 7)`
			actual, ok := billing_setting.GetBillingExpr(name)
			require.True(t, ok)
			assert.Equal(t, settings.BillingExpr[name], actual)
		})
	}
}

// DeepSeek prices are peak/off-peak expressions in USD per 1M tokens; peak is
// twice off-peak. The same usage settles to the same quota from a Chat,
// Responses or Anthropic response, and the Search/Fetch routing names cost
// $0.75 per 1,000 requests with no token price. Which tier runs depends on the clock; both are pinned here.
func TestDeepSeekBuiltinBilling(t *testing.T) {
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	saved := *settings
	savedRatios, savedPrices := ratio_setting.ModelRatio2JSONString(), ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		*settings = saved
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedRatios))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedPrices))
	})
	*settings = billing_setting.BillingSetting{BillingMode: map[string]string{}, BillingExpr: map[string]string{}}
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))

	// 1000 cache-miss + 2000 cache-hit input tokens and 500 output tokens.
	chat := &dto.Usage{PromptTokens: 3000, CompletionTokens: 500, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 2000}}
	responses := &dto.Usage{}
	service.ApplyResponsesUsage(responses, &dto.Usage{InputTokens: 3000, OutputTokens: 500, InputTokensDetails: &dto.InputTokenDetails{CachedTokens: 2000}})
	anthropic := &dto.Usage{PromptTokens: 1000, CompletionTokens: 500, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 2000}, UsageSemantic: "anthropic"}
	quota := func(expression string, usage *dto.Usage) billingexpr.TieredResult {
		params := service.BuildTieredTokenParams(usage, usage.UsageSemantic == "anthropic", billingexpr.UsedVars(expression))
		result, err := billingexpr.ComputeTieredQuota(&billingexpr.BillingSnapshot{
			ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), GroupRatio: 1, QuotaPerUnit: 500000,
		}, params)
		require.NoError(t, err)
		return result
	}
	for model, want := range map[string]map[string]int{
		// flash: 1000*0.15 + 2000*0.003 + 500*0.6 = $0.000456 off-peak.
		"deepseek-flash":               {"off_peak": 228, "peak": 456},
		"deepseek-v4-flash":            {"off_peak": 228, "peak": 456},
		"deepseek-v4-flash-vision-exp": {"off_peak": 228, "peak": 456},
		// pro: 1000*0.66 + 2000*0.022 + 500*1.98 = $0.001694 off-peak.
		"deepseek-v4-pro": {"off_peak": 847, "peak": 1694},
	} {
		t.Run(model, func(t *testing.T) {
			assert.Equal(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode(model))
			expression, ok := billing_setting.GetBillingExpr(model)
			require.True(t, ok)
			for name, usage := range map[string]*dto.Usage{"chat": chat, "responses": responses, "anthropic": anthropic} {
				result := quota(expression, usage)
				require.Contains(t, want, result.MatchedTier, name)
				assert.Equal(t, want[result.MatchedTier], result.ActualQuotaAfterGroup, name)
			}
		})
	}
	for _, model := range []string{"deepseek-search", "deepseek-fetch"} {
		expression, ok := billing_setting.GetBillingExpr(model)
		require.True(t, ok, model)
		result := quota(expression, &dto.Usage{})
		assert.Equal(t, billingexpr.BillingUnitRequest, result.BillingUnit, model)
		assert.Equal(t, 375, result.ActualQuotaAfterGroup, model)
	}
}
