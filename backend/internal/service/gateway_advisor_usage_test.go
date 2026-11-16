//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// advisorUsageFixture 取自一次真实的 Claude Code 请求（执行模型 claude-opus-5-5，advisor claude-fable-5-2）：
// 顶层 usage 等于两个 type=message 迭代之和，advisor_message 只出现在 iterations 中。
const advisorUsageFixture = `{"input_tokens":4,"cache_creation_input_tokens":4818,"cache_read_input_tokens":612798,"output_tokens":945,` +
	`"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":"standard",` +
	`"cache_creation":{"ephemeral_1h_input_tokens":2972,"ephemeral_5m_input_tokens":0},` +
	`"iterations":[` +
	`{"input_tokens":2,"output_tokens":415,"cache_read_input_tokens":304913,"cache_creation_input_tokens":2972,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":2972},"type":"message"},` +
	`{"input_tokens":311423,"output_tokens":10019,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"type":"advisor_message","model":"claude-fable-5-2"},` +
	`{"input_tokens":2,"output_tokens":530,"cache_read_input_tokens":307885,"cache_creation_input_tokens":1846,"cache_creation":{"ephemeral_5m_input_tokens":1846,"ephemeral_1h_input_tokens":0},"type":"message"}` +
	`],"speed":"standard"}`

var advisorUsageFixtureExpected = AdvisorUsage{
	Model:        "claude-fable-5-2",
	Calls:        1,
	InputTokens:  311423,
	OutputTokens: 10019,
}

func TestApplyAdvisorUsageFromJSON_SumsOnlyAdvisorIterations(t *testing.T) {
	usage := &ClaudeUsage{InputTokens: 4, OutputTokens: 945}

	applyAdvisorUsageFromJSON(usage, gjson.Parse(advisorUsageFixture))

	require.Equal(t, advisorUsageFixtureExpected, usage.Advisor)
	require.Equal(t, 4, usage.InputTokens, "top-level executor usage must stay untouched")
	require.Equal(t, 945, usage.OutputTokens)
}

func TestApplyAdvisorUsageFromJSON_SumsMultipleCallsWithCache(t *testing.T) {
	usage := &ClaudeUsage{}
	node := gjson.Parse(`{"iterations":[
		{"type":"advisor_message","model":"claude-fable-5-2","input_tokens":100,"output_tokens":10,"cache_creation_input_tokens":50,"cache_creation":{"ephemeral_5m_input_tokens":50}},
		{"type":"message","input_tokens":7,"output_tokens":7},
		{"type":"advisor_message","model":"claude-fable-5-2","input_tokens":20,"output_tokens":30,"cache_read_input_tokens":50}
	]}`)

	applyAdvisorUsageFromJSON(usage, node)

	require.Equal(t, AdvisorUsage{
		Model: "claude-fable-5-2", Calls: 2, InputTokens: 120, OutputTokens: 40,
		CacheCreationInputTokens: 50, CacheReadInputTokens: 50, CacheCreation5mTokens: 50,
	}, usage.Advisor)
}

func TestApplyAdvisorUsageFromJSON_TruncatesOverlongModel(t *testing.T) {
	usage := &ClaudeUsage{}
	long := strings.Repeat("模", advisorModelMaxLength+20)

	applyAdvisorUsageFromJSON(usage, gjson.Parse(`{"iterations":[{"type":"advisor_message","model":"`+long+`","input_tokens":1}]}`))

	require.Equal(t, advisorModelMaxLength, utf8.RuneCountInString(usage.Advisor.Model))
}

func TestApplyAdvisorUsageFromJSON_WithoutIterationsKeepsPreviousValue(t *testing.T) {
	usage := &ClaudeUsage{Advisor: advisorUsageFixtureExpected}

	applyAdvisorUsageFromJSON(usage, gjson.Parse(`{"output_tokens":3}`))

	require.Equal(t, advisorUsageFixtureExpected, usage.Advisor)
}

func TestParseClaudeUsageFromResponseBody_ReadsAdvisorUsage(t *testing.T) {
	usage := parseClaudeUsageFromResponseBody([]byte(`{"type":"message","usage":` + advisorUsageFixture + `}`))

	require.Equal(t, advisorUsageFixtureExpected, usage.Advisor)
	require.Equal(t, 945, usage.OutputTokens)
}

// 流式 message_delta 每次带完整的最新 iterations，后一次应整体替换而不是累加。
func advisorSSEDeltas() []string {
	return []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":2,"cache_read_input_tokens":304913,"output_tokens":1}}}`,
		`{"type":"message_delta","usage":{"output_tokens":415,"iterations":[` +
			`{"type":"message","input_tokens":2,"output_tokens":415},` +
			`{"type":"advisor_message","model":"claude-fable-5-2","input_tokens":1000,"output_tokens":100}]}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":` + advisorUsageFixture + `}`,
	}
}

func TestParseSSEUsagePassthrough_AdvisorTakesLatestDelta(t *testing.T) {
	usage := &ClaudeUsage{}

	for _, data := range advisorSSEDeltas() {
		parseSSEUsagePassthrough(data, usage)
	}

	require.Equal(t, advisorUsageFixtureExpected, usage.Advisor)
	require.Equal(t, 945, usage.OutputTokens)
}

func TestGatewayParseSSEUsage_AdvisorTakesLatestDelta(t *testing.T) {
	svc := &GatewayService{}
	usage := &ClaudeUsage{}

	for _, data := range advisorSSEDeltas() {
		svc.parseSSEUsage(data, usage)
	}

	require.Equal(t, advisorUsageFixtureExpected, usage.Advisor)
	require.Equal(t, 945, usage.OutputTokens)
}

func TestHandleNonStreamingResponse_ReadsAdvisorUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	body := []byte(`{"id":"msg_1","type":"message","usage":` + advisorUsageFixture + `}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	svc := &GatewayService{cfg: &config.Config{}, rateLimitService: &RateLimitService{}}

	usage, err := svc.handleNonStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, "claude-opus-5-5", "claude-opus-5-5")

	require.NoError(t, err)
	require.Equal(t, advisorUsageFixtureExpected, usage.Advisor)
}

func TestGatewayServiceRecordUsage_AdvisorBilledAtAdvisorModelPrice(t *testing.T) {
	record := func(requestID string, advisor AdvisorUsage) *UsageLog {
		usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
		svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
		require.NoError(t, svc.RecordUsage(context.Background(), &RecordUsageInput{
			Result: &ForwardResult{
				RequestID: requestID,
				Model:     "claude-opus-5-5",
				Duration:  time.Second,
				Usage: ClaudeUsage{
					InputTokens: 4, OutputTokens: 945, CacheReadInputTokens: 612798, Advisor: advisor,
				},
			},
			APIKey:  &APIKey{ID: 501},
			User:    &User{ID: 601},
			Account: &Account{ID: 701},
		}))
		require.NotNil(t, usageRepo.lastLog)
		return usageRepo.lastLog
	}

	base := record("advisor_base", AdvisorUsage{})
	withAdvisor := record("advisor_fable", advisorUsageFixtureExpected)

	// Fable 5.2：$8 / $40 per MTok。
	wantAdvisorCost := 311423*8e-6 + 10019*40e-6
	require.Nil(t, base.AdvisorModel)
	require.Zero(t, base.AdvisorCalls)
	require.Zero(t, base.AdvisorCost)

	require.NotNil(t, withAdvisor.AdvisorModel)
	require.Equal(t, "claude-fable-5-2", *withAdvisor.AdvisorModel)
	require.Equal(t, 1, withAdvisor.AdvisorCalls)
	require.Equal(t, 311423, withAdvisor.AdvisorInputTokens)
	require.Equal(t, 10019, withAdvisor.AdvisorOutputTokens)
	require.InDelta(t, wantAdvisorCost, withAdvisor.AdvisorCost, 1e-9)
	require.InDelta(t, base.TotalCost+wantAdvisorCost, withAdvisor.TotalCost, 1e-9)
	require.InDelta(t, withAdvisor.TotalCost*withAdvisor.RateMultiplier, withAdvisor.ActualCost, 1e-9)
	require.Equal(t, base.InputTokens, withAdvisor.InputTokens, "executor token columns exclude advisor usage")
}
