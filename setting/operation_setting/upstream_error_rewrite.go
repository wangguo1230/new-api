package operation_setting

import (
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

// UpstreamErrorRewriteRule 描述一条"上游错误消息改写"规则。
//
// 匹配条件（必须同时满足）：
//   - StatusCodes：HTTP 状态码集合（为空表示不限制状态码）
//   - Contains：错误消息子串集合（不区分大小写，任意一个命中即视为匹配；
//     集合为空表示不按内容过滤）
//
// 替换字段（缺省项保持原值）：
//   - Message：客户端可见的 message
//   - Type：错误 type（写入 RelayError 的 type，仅在 RelayError 是 OpenAIError/ClaudeError 时生效）
//   - Code：错误 code（写入 RelayError 的 code，仅在 RelayError 是 OpenAIError 时生效）
//   - StatusCode：响应状态码（为 0 时保留原状态码）
type UpstreamErrorRewriteRule struct {
	StatusCodes []int    `json:"status_codes,omitempty"`
	Contains    []string `json:"contains,omitempty"`

	Message    string `json:"message"`
	Type       string `json:"type,omitempty"`
	Code       string `json:"code,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
}

var (
	upstreamErrorRewriteMu    sync.RWMutex
	upstreamErrorRewriteRules = defaultUpstreamErrorRewriteRules()
)

// defaultUpstreamErrorRewriteRules 返回内置默认规则。
//
// 当前默认仅覆盖一种场景：上游（如 sub2api / 二级 new-api）因账户余额不足
// 返回 402/403 + "Insufficient account balance"。为了避免向终端用户暴露
// "上游余额"等运维细节，统一改写为通用的"渠道服务暂不可用"提示。
func defaultUpstreamErrorRewriteRules() []UpstreamErrorRewriteRule {
	return []UpstreamErrorRewriteRule{
		{
			StatusCodes: []int{402, 403},
			Contains: []string{
				"insufficient account balance",
				"insufficient balance",
				"insufficient_quota",
			},
			Message:    "当前渠道服务暂不可用，请联系管理员或稍后重试",
			Type:       "upstream_unavailable",
			Code:       "upstream_unavailable",
			StatusCode: 503,
		},
	}
}

// GetUpstreamErrorRewriteRules 返回当前规则的快照副本。
func GetUpstreamErrorRewriteRules() []UpstreamErrorRewriteRule {
	upstreamErrorRewriteMu.RLock()
	defer upstreamErrorRewriteMu.RUnlock()
	out := make([]UpstreamErrorRewriteRule, len(upstreamErrorRewriteRules))
	copy(out, upstreamErrorRewriteRules)
	return out
}

// SetUpstreamErrorRewriteRules 用给定规则覆盖当前全部规则（线程安全）。
// 传 nil 或空切片表示禁用所有改写。
func SetUpstreamErrorRewriteRules(rules []UpstreamErrorRewriteRule) {
	upstreamErrorRewriteMu.Lock()
	defer upstreamErrorRewriteMu.Unlock()
	if rules == nil {
		upstreamErrorRewriteRules = []UpstreamErrorRewriteRule{}
		return
	}
	upstreamErrorRewriteRules = rules
}

// ResetUpstreamErrorRewriteRulesToDefault 重置为内置默认规则（用于测试或重新加载）。
func ResetUpstreamErrorRewriteRulesToDefault() {
	SetUpstreamErrorRewriteRules(defaultUpstreamErrorRewriteRules())
}

// LoadUpstreamErrorRewriteRulesFromJSON 用 JSON 字符串覆盖当前规则。
// 字符串为空时回退到默认规则；解析失败时不修改当前规则并返回错误。
func LoadUpstreamErrorRewriteRulesFromJSON(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		ResetUpstreamErrorRewriteRulesToDefault()
		return nil
	}
	var rules []UpstreamErrorRewriteRule
	if err := common.UnmarshalJsonStr(s, &rules); err != nil {
		return err
	}
	SetUpstreamErrorRewriteRules(rules)
	return nil
}

// MatchUpstreamErrorRewrite 在当前规则中查找首个命中的规则。
// 未命中返回 nil。
func MatchUpstreamErrorRewrite(statusCode int, errMsg string) *UpstreamErrorRewriteRule {
	lowerMsg := strings.ToLower(errMsg)
	rules := GetUpstreamErrorRewriteRules()
	for i := range rules {
		r := rules[i]
		if !matchStatus(r.StatusCodes, statusCode) {
			continue
		}
		if !matchContains(r.Contains, lowerMsg) {
			continue
		}
		return &r
	}
	return nil
}

func matchStatus(codes []int, statusCode int) bool {
	if len(codes) == 0 {
		return true
	}
	for _, c := range codes {
		if c == statusCode {
			return true
		}
	}
	return false
}

func matchContains(subs []string, lowerMsg string) bool {
	if len(subs) == 0 {
		return true
	}
	for _, sub := range subs {
		if sub == "" {
			continue
		}
		if strings.Contains(lowerMsg, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}
