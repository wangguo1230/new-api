package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
)

// RewriteUpstreamError 检查 newApiErr 是否命中"上游错误改写"规则，
// 命中则替换其暴露给客户端的字段（message / type / code / status_code），
// 同时把原始错误消息以 INFO 级别记录到日志，便于管理员排障。
//
// 设计要点（合并友好）：
//   - 仅修改 *types.NewAPIError 的公开字段：Err / RelayError / StatusCode；
//   - 不动 errorType / errorCode / skipRetry / recordErrorLog 等私有字段，
//     从而：a) 不需要在 types 包新增 setter；b) 重试与日志记录策略不被改写。
//   - 通过 defer 钩子注入到 RelayErrorHandler 末尾（参见 service/error.go），
//     单点拦截所有上游错误响应，避免在多个 handler 中重复改造。
//
// 返回值：是否发生改写。仅用于测试与可观测性，调用方无需关心。
func RewriteUpstreamError(ctx context.Context, newApiErr *types.NewAPIError) bool {
	if newApiErr == nil {
		return false
	}
	rule := operation_setting.MatchUpstreamErrorRewrite(newApiErr.StatusCode, newApiErr.Error())
	if rule == nil {
		return false
	}

	originalMsg := newApiErr.Error()
	originalStatus := newApiErr.StatusCode

	// 1) 替换 Err（NewAPIError.Error() 取的就是 Err.Error()）
	newApiErr.Err = errors.New(rule.Message)

	// 2) 同步 RelayError，让 ToOpenAIError() / ToClaudeError() 输出一致
	switch relayErr := newApiErr.RelayError.(type) {
	case types.OpenAIError:
		relayErr.Message = rule.Message
		if rule.Type != "" {
			relayErr.Type = rule.Type
		}
		if rule.Code != "" {
			relayErr.Code = rule.Code
		}
		newApiErr.RelayError = relayErr
	case types.ClaudeError:
		relayErr.Message = rule.Message
		if rule.Type != "" {
			relayErr.Type = rule.Type
		}
		newApiErr.RelayError = relayErr
	}

	// 3) 状态码替换（保持 0 表示沿用原状态码的语义）
	if rule.StatusCode != 0 {
		newApiErr.StatusCode = rule.StatusCode
	}

	logger.LogInfo(ctx, fmt.Sprintf(
		"upstream error rewritten: status %d -> %d, original=%s",
		originalStatus, newApiErr.StatusCode, common.LocalLogPreview(originalMsg),
	))
	return true
}
