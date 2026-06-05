package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifyImageBillingTier(t *testing.T) {
	cases := []struct {
		size string
		tier string
		ok   bool
	}{
		// 直接档位标签(大小写不敏感)
		{"1k", ImageBillingTier1K, true},
		{"2K", ImageBillingTier2K, true},
		{"4k", ImageBillingTier4K, true},
		{"  2K ", ImageBillingTier2K, true},
		// 特定尺寸串直映射
		{"2048x2048", ImageBillingTier2K, true},
		{"2048x1152", ImageBillingTier2K, true},
		{"3840x2160", ImageBillingTier4K, true},
		{"2160x3840", ImageBillingTier4K, true},
		// maxEdge 归一
		{"1024x1024", ImageBillingTier1K, true},
		{"800x600", ImageBillingTier1K, true},
		{"1280x720", ImageBillingTier2K, true},
		{"1024x2048", ImageBillingTier2K, true},
		{"4096x4096", ImageBillingTier4K, true},
		{"3000x1000", ImageBillingTier4K, true},
		// 边界:无法归档
		{"", "", false},
		{"auto", "", false},
		{"abc", "", false},
		{"1024", "", false},
		{"1024x", "", false},
		{"0x0", "", false},
		{"-1x100", "", false},
	}
	for _, c := range cases {
		tier, ok := ClassifyImageBillingTier(c.size)
		require.Equalf(t, c.ok, ok, "size=%q ok mismatch", c.size)
		require.Equalf(t, c.tier, tier, "size=%q tier mismatch", c.size)
	}
}

func TestGetImageTierRatioForModel(t *testing.T) {
	// 保存并在结束时恢复全局配置,避免污染其他测试
	orig := imageTierPriceSetting
	defer func() {
		imageTierPriceSetting = orig
		RebuildImageTierIndex()
	}()

	imageTierPriceSetting = ImageTierPriceSetting{
		TierRatios: map[string]float64{
			ImageBillingTier1K: 1.0,
			ImageBillingTier2K: 1.5,
			ImageBillingTier4K: 2.0,
		},
		Models: []string{"seedream-4.0", "flux*"},
	}
	RebuildImageTierIndex()

	// 精确白名单命中,各档倍率
	ratio, applied := GetImageTierRatioForModel("seedream-4.0", "1024x1024")
	require.True(t, applied)
	require.Equal(t, 1.0, ratio)

	ratio, applied = GetImageTierRatioForModel("seedream-4.0", "1536x1536")
	require.True(t, applied)
	require.Equal(t, 1.5, ratio)

	ratio, applied = GetImageTierRatioForModel("seedream-4.0", "3840x2160")
	require.True(t, applied)
	require.Equal(t, 2.0, ratio)

	// 前缀白名单命中
	ratio, applied = GetImageTierRatioForModel("flux-pro-1.1", "4096x4096")
	require.True(t, applied)
	require.Equal(t, 2.0, ratio)

	// 白名单外:返回 (1.0, false),保持原有行为
	ratio, applied = GetImageTierRatioForModel("gpt-image-1", "1024x1024")
	require.False(t, applied)
	require.Equal(t, 1.0, ratio)

	// 白名单内但 size 无法归档:fallback 到 1K 档
	ratio, applied = GetImageTierRatioForModel("seedream-4.0", "auto")
	require.True(t, applied)
	require.Equal(t, 1.0, ratio)

	ratio, applied = GetImageTierRatioForModel("seedream-4.0", "")
	require.True(t, applied)
	require.Equal(t, 1.0, ratio)

	require.True(t, IsImageTierModel("flux-dev"))
	require.False(t, IsImageTierModel("dall-e-3"))
}

func TestRebuildImageTierIndex_HotUpdate(t *testing.T) {
	orig := imageTierPriceSetting
	defer func() {
		imageTierPriceSetting = orig
		RebuildImageTierIndex()
	}()

	imageTierPriceSetting = ImageTierPriceSetting{
		TierRatios: map[string]float64{ImageBillingTier2K: 1.5},
		Models:     []string{"my-image-model"},
	}
	RebuildImageTierIndex()
	ratio, applied := GetImageTierRatioForModel("my-image-model", "2K")
	require.True(t, applied)
	require.Equal(t, 1.5, ratio)

	// 模拟管理员热改 2K 倍率,重建后即时生效
	imageTierPriceSetting.TierRatios[ImageBillingTier2K] = 1.8
	RebuildImageTierIndex()
	ratio, applied = GetImageTierRatioForModel("my-image-model", "2K")
	require.True(t, applied)
	require.Equal(t, 1.8, ratio)
}

func TestImageTierDefaultsDoNotAffectExistingModels(t *testing.T) {
	// 默认空白名单时,任何模型都不应被档位计费命中
	orig := imageTierPriceSetting
	defer func() {
		imageTierPriceSetting = orig
		RebuildImageTierIndex()
	}()

	imageTierPriceSetting = ImageTierPriceSetting{
		TierRatios: map[string]float64{ImageBillingTier1K: 1.0, ImageBillingTier2K: 1.5, ImageBillingTier4K: 2.0},
		Models:     []string{},
	}
	RebuildImageTierIndex()

	_, applied := GetImageTierRatioForModel("gpt-image-1", "4096x4096")
	require.False(t, applied)
	require.False(t, IsImageTierModel("gpt-image-1"))
}
