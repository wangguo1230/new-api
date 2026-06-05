package operation_setting

import (
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/setting/config"
)

// ---------------------------------------------------------------------------
// Image size-tier pricing (1K / 2K / 4K, admin-configurable)
// DB key: image_tier_price_setting.tier_ratios / image_tier_price_setting.models
//
// 计费方式：基础价 × 档位倍率。仅对白名单模型生效；档位由图片尺寸(size)按
// 最长边归一到 1K/2K/4K，再乘以对应倍率，最终作为 ImagePriceRatio 参与预扣/结算。
//
// 白名单(models)元素支持精确名或前缀通配 "prefix*"。
// ---------------------------------------------------------------------------

const (
	ImageBillingTier1K = "1K"
	ImageBillingTier2K = "2K"
	ImageBillingTier4K = "4K"
)

// 硬编码默认档位倍率，作为兜底；管理员可通过后台覆盖。
var defaultImageTierRatios = map[string]float64{
	ImageBillingTier1K: 1.0,
	ImageBillingTier2K: 1.5,
	ImageBillingTier4K: 2.0,
}

// ImageTierPriceSetting is managed by config.GlobalConfig.Register.
type ImageTierPriceSetting struct {
	// 档位倍率表，key 为档位名(1K/2K/4K)，value 为系数
	TierRatios map[string]float64 `json:"tier_ratios"`
	// 启用档位计费的模型白名单，元素支持精确名或前缀通配 "prefix*"
	Models []string `json:"models"`
}

var imageTierPriceSetting = ImageTierPriceSetting{
	TierRatios: func() map[string]float64 {
		m := make(map[string]float64, len(defaultImageTierRatios))
		for k, v := range defaultImageTierRatios {
			m[k] = v
		}
		return m
	}(),
	Models: []string{},
}

func init() {
	config.GlobalConfig.Register("image_tier_price_setting", &imageTierPriceSetting)
	RebuildImageTierIndex()
}

// ---------------------------------------------------------------------------
// Precomputed index (atomic, lock-free on read path)
// ---------------------------------------------------------------------------

type imageTierIndex struct {
	tierRatios   map[string]float64  // 归一档位(大写) → 倍率
	exactModels  map[string]struct{} // 精确白名单
	prefixModels []string            // 前缀白名单(去掉尾部 "*")，长度降序
}

var currentImageTierIndex atomic.Pointer[imageTierIndex]

// RebuildImageTierIndex 从当前配置重建查找索引。
// 在 init 与配置热更新后调用，不在计费热路径上。
func RebuildImageTierIndex() {
	idx := &imageTierIndex{
		tierRatios:  make(map[string]float64, len(imageTierPriceSetting.TierRatios)),
		exactModels: make(map[string]struct{}),
	}

	// 默认倍率打底，配置值覆盖
	for k, v := range defaultImageTierRatios {
		idx.tierRatios[strings.ToUpper(k)] = v
	}
	for k, v := range imageTierPriceSetting.TierRatios {
		idx.tierRatios[strings.ToUpper(strings.TrimSpace(k))] = v
	}

	for _, m := range imageTierPriceSetting.Models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if strings.HasSuffix(m, "*") {
			if prefix := strings.TrimSuffix(m, "*"); prefix != "" {
				idx.prefixModels = append(idx.prefixModels, prefix)
			}
		} else {
			idx.exactModels[m] = struct{}{}
		}
	}
	sort.Slice(idx.prefixModels, func(i, j int) bool {
		return len(idx.prefixModels[i]) > len(idx.prefixModels[j])
	})

	currentImageTierIndex.Store(idx)
}

func (idx *imageTierIndex) matchModel(model string) bool {
	if model == "" {
		return false
	}
	if _, ok := idx.exactModels[model]; ok {
		return true
	}
	for _, prefix := range idx.prefixModels {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

// IsImageTierModel 判断模型是否启用了档位计费(在白名单内)。
func IsImageTierModel(model string) bool {
	idx := currentImageTierIndex.Load()
	if idx == nil {
		return false
	}
	return idx.matchModel(model)
}

// ClassifyImageBillingTier 将图片尺寸归一到 1K/2K/4K 档位。
// 规则：小写归一后，直接档位标签 / 特定尺寸串直映射 / 否则解析 WxH 取最长边。
// 空、"auto"、非法格式返回 ("", false)，交由上层 fallback。
func ClassifyImageBillingTier(size string) (string, bool) {
	trimmed := strings.TrimSpace(size)
	switch strings.ToLower(trimmed) {
	case "", "auto":
		return "", false
	case "1k":
		return ImageBillingTier1K, true
	case "2k":
		return ImageBillingTier2K, true
	case "4k":
		return ImageBillingTier4K, true
	case "2048x2048", "2048x1152":
		return ImageBillingTier2K, true
	case "3840x2160", "2160x3840":
		return ImageBillingTier4K, true
	}

	width, height, ok := parseImageDimensions(trimmed)
	if !ok {
		return "", false
	}
	maxEdge := width
	if height > maxEdge {
		maxEdge = height
	}
	switch {
	case maxEdge <= 1024:
		return ImageBillingTier1K, true
	case maxEdge <= 2048:
		return ImageBillingTier2K, true
	default:
		return ImageBillingTier4K, true
	}
}

// GetImageTierRatioForModel 返回模型在给定尺寸下的档位倍率。
//   - 模型不在白名单：返回 (1.0, false)，调用方应保持原有计费行为不变；
//   - 在白名单但 size 无法归档：fallback 到 1K 档(最低档，对用户友好)，applied=true；
//   - 正常命中：返回对应档位倍率，applied=true。
func GetImageTierRatioForModel(model, size string) (ratio float64, applied bool) {
	idx := currentImageTierIndex.Load()
	if idx == nil || !idx.matchModel(model) {
		return 1.0, false
	}

	tier, ok := ClassifyImageBillingTier(size)
	if !ok {
		tier = ImageBillingTier1K
	}
	if r, exists := idx.tierRatios[strings.ToUpper(tier)]; exists {
		return r, true
	}
	// 档位未配置，再兜底 1K；若 1K 也缺失则按 1.0
	if r, exists := idx.tierRatios[ImageBillingTier1K]; exists {
		return r, true
	}
	return 1.0, true
}

// GetImageTierSettingForTest 返回当前档位配置快照，仅供跨包单元测试使用。
func GetImageTierSettingForTest() ImageTierPriceSetting {
	return imageTierPriceSetting
}

// SetImageTierSettingForTest 覆盖档位配置并重建索引，仅供跨包单元测试使用。
func SetImageTierSettingForTest(s ImageTierPriceSetting) {
	imageTierPriceSetting = s
	RebuildImageTierIndex()
}

func parseImageDimensions(size string) (int, int, bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, false
	}
	height, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, false
	}
	if width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}
