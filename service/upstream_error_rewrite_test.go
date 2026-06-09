package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

const insufficientBalanceRewrittenMessage = "当前渠道服务暂不可用，请联系管理员或稍后重试"

func TestRewriteUpstreamError_InsufficientBalance(t *testing.T) {
	t.Cleanup(operation_setting.ResetUpstreamErrorRewriteRulesToDefault)
	operation_setting.ResetUpstreamErrorRewriteRulesToDefault()

	// 模拟上游 sub2api / 二级 new-api 余额不足时返回的错误响应。
	body := `{"error":{"message":"Insufficient account balance","type":"bad_response_status_code","code":"bad_response_status_code"}}`
	resp := &http.Response{
		StatusCode: http.StatusForbidden,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)
	require.NotNil(t, newAPIError)

	// 客户端可见 message 已被替换；状态码改为 503
	require.Equal(t, insufficientBalanceRewrittenMessage, newAPIError.Error())
	require.Equal(t, http.StatusServiceUnavailable, newAPIError.StatusCode)

	// ToOpenAIError 与 ToClaudeError 都应使用新文案
	oaiErr := newAPIError.ToOpenAIError()
	require.Equal(t, insufficientBalanceRewrittenMessage, oaiErr.Message)
	require.Equal(t, "upstream_unavailable", oaiErr.Type)
	require.Equal(t, "upstream_unavailable", oaiErr.Code)

	claudeErr := newAPIError.ToClaudeError()
	require.Equal(t, insufficientBalanceRewrittenMessage, claudeErr.Message)
}

func TestRewriteUpstreamError_NoMatch_KeepsOriginal(t *testing.T) {
	t.Cleanup(operation_setting.ResetUpstreamErrorRewriteRulesToDefault)
	operation_setting.ResetUpstreamErrorRewriteRulesToDefault()

	// 500 + 非"余额"消息：不命中规则，保持原始错误。
	body := `{"error":{"message":"upstream timeout","type":"server_error","code":"server_error"}}`
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)
	require.NotNil(t, newAPIError)
	require.Equal(t, "upstream timeout", newAPIError.Error())
	require.Equal(t, http.StatusInternalServerError, newAPIError.StatusCode)
}

func TestRewriteUpstreamError_StatusMatch_MessageNotMatch(t *testing.T) {
	t.Cleanup(operation_setting.ResetUpstreamErrorRewriteRulesToDefault)
	operation_setting.ResetUpstreamErrorRewriteRulesToDefault()

	// 403 但消息不含"insufficient"：不应改写。
	body := `{"error":{"message":"forbidden by policy","type":"forbidden","code":"forbidden"}}`
	resp := &http.Response{
		StatusCode: http.StatusForbidden,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)
	require.NotNil(t, newAPIError)
	require.Equal(t, "forbidden by policy", newAPIError.Error())
	require.Equal(t, http.StatusForbidden, newAPIError.StatusCode)
}

func TestRewriteUpstreamError_LoadFromJSON(t *testing.T) {
	t.Cleanup(operation_setting.ResetUpstreamErrorRewriteRulesToDefault)

	// 自定义规则：把 429 包含 "rate" 的错误改写为"已限流"。
	require.NoError(t, operation_setting.LoadUpstreamErrorRewriteRulesFromJSON(`[
        {
            "status_codes": [429],
            "contains": ["rate"],
            "message": "已被上游限流，请稍后重试",
            "status_code": 429
        }
    ]`))

	body := `{"error":{"message":"rate limit exceeded","type":"rate_limit","code":"rate_limit"}}`
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)
	require.NotNil(t, newAPIError)
	require.Equal(t, "已被上游限流，请稍后重试", newAPIError.Error())
	require.Equal(t, http.StatusTooManyRequests, newAPIError.StatusCode)
}

func TestRewriteUpstreamError_NilSafe(t *testing.T) {
	require.False(t, RewriteUpstreamError(context.Background(), nil))
}

func TestRewriteUpstreamError_DirectCall_PreservesPrivateBehavior(t *testing.T) {
	t.Cleanup(operation_setting.ResetUpstreamErrorRewriteRulesToDefault)
	operation_setting.ResetUpstreamErrorRewriteRulesToDefault()

	// 直接构造一个带 skipRetry 标志的错误，确认改写后仍被 IsSkipRetryError 识别。
	// 这一断言保证私有字段未被改写覆盖（合并友好性的关键不变量）。
	orig := types.NewErrorWithStatusCode(
		errInsufficient(),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusForbidden,
		types.ErrOptionWithSkipRetry(),
	)
	require.True(t, types.IsSkipRetryError(orig))

	require.True(t, RewriteUpstreamError(context.Background(), orig))
	require.True(t, types.IsSkipRetryError(orig), "skipRetry 标志在改写后必须保留")
	require.Equal(t, insufficientBalanceRewrittenMessage, orig.Error())
	require.Equal(t, http.StatusServiceUnavailable, orig.StatusCode)
}

// errInsufficient 构造一个 message 含 "Insufficient account balance" 的 error，
// 用于直接命中默认规则。
func errInsufficient() error { return &stringErr{s: "Insufficient account balance"} }

type stringErr struct{ s string }

func (e *stringErr) Error() string { return e.s }
