package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// dall-e 系列既有 size/quality 计费倍率必须保持不变(回归保护)。
func TestGetTokenCountMeta_DallERegression(t *testing.T) {
	cases := []struct {
		model   string
		size    string
		quality string
		ratio   float64
	}{
		{"dall-e-2", "256x256", "", 0.4},
		{"dall-e-2", "512x512", "", 0.45},
		{"dall-e-3", "1024x1024", "standard", 1.0},
		{"dall-e-3", "1024x1024", "hd", 2.0},
		{"dall-e-3", "1792x1024", "standard", 2.0},
		{"dall-e-3", "1792x1024", "hd", 3.0}, // sizeRatio 2 × qualityRatio 1.5
	}
	for _, c := range cases {
		req := &ImageRequest{Model: c.model, Size: c.size, Quality: c.quality, Prompt: "x"}
		meta := req.GetTokenCountMeta()
		require.InDeltaf(t, c.ratio, meta.ImagePriceRatio, 1e-9,
			"model=%s size=%s quality=%s", c.model, c.size, c.quality)
	}
}

// 白名单模型按 1K/2K/4K 档位倍率产出 ImagePriceRatio。
func TestGetTokenCountMeta_TierModel(t *testing.T) {
	orig := operation_setting.GetImageTierSettingForTest()
	defer operation_setting.SetImageTierSettingForTest(orig)

	operation_setting.SetImageTierSettingForTest(operation_setting.ImageTierPriceSetting{
		TierRatios: map[string]float64{
			operation_setting.ImageBillingTier1K: 1.0,
			operation_setting.ImageBillingTier2K: 1.5,
			operation_setting.ImageBillingTier4K: 2.0,
		},
		Models: []string{"seedream-4.0"},
	})

	// 1K / 2K / 4K
	require.InDelta(t, 1.0, (&ImageRequest{Model: "seedream-4.0", Size: "1024x1024", Prompt: "x"}).GetTokenCountMeta().ImagePriceRatio, 1e-9)
	require.InDelta(t, 1.5, (&ImageRequest{Model: "seedream-4.0", Size: "1536x1536", Prompt: "x"}).GetTokenCountMeta().ImagePriceRatio, 1e-9)
	require.InDelta(t, 2.0, (&ImageRequest{Model: "seedream-4.0", Size: "3840x2160", Prompt: "x"}).GetTokenCountMeta().ImagePriceRatio, 1e-9)

	// 白名单外模型:ImagePriceRatio = 1.0(行为不变)
	require.InDelta(t, 1.0, (&ImageRequest{Model: "gpt-image-1", Size: "4096x4096", Prompt: "x"}).GetTokenCountMeta().ImagePriceRatio, 1e-9)
}
