//go:build unit

package service

import (
	"math"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type parsedRequestSnapshot struct {
	Body            string
	Model           string
	Stream          bool
	MetadataUserID  string
	HasSystem       bool
	ThinkingEnabled bool
	OutputEffort    string
	Speed           string
	MaxTokens       int
	SystemRaw       string
	MessagesRaw     string
	InputRaw        string
}

func snapshotParsedRequest(p *ParsedRequest) parsedRequestSnapshot {
	return parsedRequestSnapshot{
		Body:            string(p.Body.Bytes()),
		Model:           p.Model,
		Stream:          p.Stream,
		MetadataUserID:  p.MetadataUserID,
		HasSystem:       p.HasSystem,
		ThinkingEnabled: p.ThinkingEnabled,
		OutputEffort:    p.OutputEffort,
		Speed:           p.Speed,
		MaxTokens:       p.MaxTokens,
		SystemRaw:       string(p.SystemRaw()),
		MessagesRaw:     string(p.MessagesRaw()),
		InputRaw:        string(p.InputRaw()),
	}
}

// referenceParseGatewayRequest 是改为单次顶层遍历之前的逐字段 gjson.Get 实现，
// 作为等价性对照的基准（含重复键、转义键、非对象值等 gjson 语义细节）。
func referenceParseGatewayRequest(body []byte, protocol string) (parsedRequestSnapshot, error) {
	var snap parsedRequestSnapshot
	if !gjson.ValidBytes(body) {
		return snap, DescribeInvalidJSON(body)
	}
	jsonStr := string(body)
	if modelResult := gjson.Get(jsonStr, "model"); modelResult.Exists() {
		if modelResult.Type != gjson.String {
			return snap, errInvalidModelFieldTypeForTest
		}
		snap.Model = modelResult.String()
		if protocol == domain.PlatformAnthropic {
			if normalized := normalizeClaudeCodeLongContextModel(snap.Model); normalized != snap.Model {
				next, err := sjson.SetBytes(body, "model", normalized)
				if err != nil {
					return snap, err
				}
				body = next
				jsonStr = string(body)
				snap.Model = normalized
			}
		}
	}
	if streamResult := gjson.Get(jsonStr, "stream"); streamResult.Exists() {
		if streamResult.Type != gjson.True && streamResult.Type != gjson.False {
			return snap, errInvalidStreamFieldTypeForTest
		}
		snap.Stream = streamResult.Bool()
	}
	snap.MetadataUserID = gjson.Get(jsonStr, "metadata.user_id").String()
	thinkingType := gjson.Get(jsonStr, "thinking.type").String()
	snap.ThinkingEnabled = thinkingType == "enabled" || thinkingType == "adaptive" || (protocol == domain.PlatformAnthropic && claude.IsOpus55(snap.Model))
	snap.OutputEffort = strings.TrimSpace(gjson.Get(jsonStr, "output_config.effort").String())
	if protocol == domain.PlatformAnthropic {
		snap.Speed = strings.ToLower(strings.TrimSpace(gjson.Get(jsonStr, "speed").String()))
	}
	if mt := gjson.Get(jsonStr, "max_tokens"); mt.Exists() && mt.Type == gjson.Number {
		f := mt.Float()
		if !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f) && f <= float64(math.MaxInt) && f >= float64(math.MinInt) {
			snap.MaxTokens = int(f)
		}
	}
	rawOf := func(r gjson.Result) string {
		if r.Raw == "" || r.Index <= 0 {
			return ""
		}
		return r.Raw
	}
	switch protocol {
	case domain.PlatformGemini:
		if sysParts := gjson.Get(jsonStr, "systemInstruction.parts"); sysParts.Exists() && sysParts.IsArray() {
			snap.SystemRaw = rawOf(sysParts)
		}
		if contents := gjson.Get(jsonStr, "contents"); contents.Exists() && contents.IsArray() {
			snap.MessagesRaw = rawOf(contents)
		}
	default:
		if sys := gjson.Get(jsonStr, "system"); sys.Exists() {
			snap.HasSystem = true
			snap.SystemRaw = rawOf(sys)
		}
		if msgs := gjson.Get(jsonStr, "messages"); msgs.Exists() && msgs.IsArray() {
			snap.MessagesRaw = rawOf(msgs)
		}
		if protocol == "responses" {
			if input := gjson.Get(jsonStr, "input"); input.Exists() {
				snap.InputRaw = rawOf(input)
			}
		}
	}
	snap.Body = string(body)
	return snap, nil
}

// jsonUnicodeEscapeForTest 拼出 JSON 的反斜杠 u 转义序列（如 "0065" 对应字母 e），
// 用于构造键名/值被转义的请求体。
func jsonUnicodeEscapeForTest(hex string) string {
	return "\\" + "u" + hex
}

type testSentinelError string

func (e testSentinelError) Error() string { return string(e) }

const (
	errInvalidModelFieldTypeForTest  = testSentinelError("invalid model field type")
	errInvalidStreamFieldTypeForTest = testSentinelError("invalid stream field type")
)

func TestParseGatewayRequest_MatchesPerFieldGJSONSemantics(t *testing.T) {
	bodies := []string{
		`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}],"system":"sys","metadata":{"user_id":"u1"},"max_tokens":1024,"thinking":{"type":"enabled","budget_tokens":1000},"stream":true,"output_config":{"effort":" high "},"speed":" FAST "}`,
		`{"model":"a","model":"b","stream":false,"stream":true,"messages":{"not":"array"},"messages":[1]}`,
		`{"metadata":{"x":1},"metadata":{"user_id":"second"},"thinking":"str","thinking":{"type":"adaptive"}}`,
		`{"metadata":[{"user_id":"in-array"}],"thinking":[{"type":"enabled"}],"output_config":{"effort":null}}`,
		`{"mod` + jsonUnicodeEscapeForTest("0065") + `l":"escaped-key","m` + jsonUnicodeEscapeForTest("0065") + `ssages":[{"role":"user"}],"` + jsonUnicodeEscapeForTest("0073") + `ystem":null}`,
		` {  "model" : "claude-opus-4-6[1m]" , "messages" : [ ] , "max_tokens" : 1.5 } `,
		`{"model":"claude-opus-4-6[1M][1m]","system":[{"type":"text","text":"x"}],"max_tokens":1e3}`,
		`{"model":"claude-opus-5-5","max_tokens":-7,"system":{"a":1}}`,
		`{"max_tokens":"100","stream":true}`,
		`{}`,
		`[]`,
		`"just a string"`,
		`null`,
		`[{"model":"in-array","messages":[]}]`,
		`{"model":"m","nested":{"model":"inner","messages":[]},"messages":[{"a":1}]}`,
		`{"model":"m","input":[{"type":"message"}],"messages":[]}`,
		`{"model":"gemini-2.5","contents":[{"role":"user"}],"systemInstruction":{"parts":[{"text":"s"}]},"systemInstruction":{"parts":"late"}}`,
		`{"systemInstruction":{"x":1},"systemInstruction":{"parts":[{"text":"second"}]},"contents":{"x":1}}`,
	}
	for _, protocol := range []string{domain.PlatformAnthropic, domain.PlatformGemini, "responses", domain.PlatformOpenAI} {
		for _, body := range bodies {
			want, wantErr := referenceParseGatewayRequest([]byte(body), protocol)
			parsed, gotErr := ParseGatewayRequest(NewRequestBodyRef([]byte(body)), protocol)
			if wantErr != nil {
				require.Error(t, gotErr, "protocol=%s body=%s", protocol, body)
				require.Equal(t, wantErr.Error(), gotErr.Error(), "protocol=%s body=%s", protocol, body)
				continue
			}
			require.NoError(t, gotErr, "protocol=%s body=%s", protocol, body)
			require.Equal(t, want, snapshotParsedRequest(parsed), "protocol=%s body=%s", protocol, body)
		}
	}
}

func TestParseGatewayRequest_InvalidFieldTypes(t *testing.T) {
	_, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":1}`)), domain.PlatformAnthropic)
	require.EqualError(t, err, "invalid model field type")
	_, err = ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"m","stream":"yes"}`)), domain.PlatformAnthropic)
	require.EqualError(t, err, "invalid stream field type")
	// model 校验先于 stream。
	_, err = ParseGatewayRequest(NewRequestBodyRef([]byte(`{"stream":"yes","model":1}`)), domain.PlatformAnthropic)
	require.EqualError(t, err, "invalid model field type")
}

func TestReplaceBody_SameSliceKeepsDerivedState(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}],"system":"s","stream":true,"max_tokens":10}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), domain.PlatformAnthropic)
	require.NoError(t, err)
	before := snapshotParsedRequest(parsed)

	require.NoError(t, parsed.ReplaceBody(parsed.Body.Bytes()))
	require.Equal(t, before, snapshotParsedRequest(parsed))
}

func TestReplaceBody_SameSliceAfterExternalModelOverrideReparses(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), domain.PlatformAnthropic)
	require.NoError(t, err)

	// 调用方直接改 Model 后再以原 body 调 ReplaceBody：沿用旧语义，Model 以 body 为准。
	parsed.Model = "mapped-model"
	require.NoError(t, parsed.ReplaceBody(parsed.Body.Bytes()))
	require.Equal(t, "claude-sonnet-4-5", parsed.Model)
}

func TestReplaceBody_ChangedBodyRefreshesState(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[],"stream":false}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), domain.PlatformAnthropic)
	require.NoError(t, err)

	next := []byte(`{"model":"claude-opus-4-6","system":"x","messages":[{"role":"user"}],"stream":true}`)
	require.NoError(t, parsed.ReplaceBody(next))
	require.Equal(t, "claude-opus-4-6", parsed.Model)
	require.True(t, parsed.Stream)
	require.True(t, parsed.HasSystem)
	require.Equal(t, `[{"role":"user"}]`, string(parsed.MessagesRaw()))

	// 同长度前缀子切片（首地址相同、长度不同）也必须重新解析。
	require.Error(t, parsed.ReplaceBody(next[:len(next)-1]))
}

func TestReplaceBody_InvalidJSONStillRejected(t *testing.T) {
	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"m","messages":[]}`)), domain.PlatformAnthropic)
	require.NoError(t, err)
	require.Error(t, parsed.ReplaceBody([]byte(`{"model":`)))
	require.Nil(t, parsed.MessagesRaw())
}

func TestCloneForBody_SameSliceSharesDerivedState(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user"}],"metadata":{"user_id":"u"},"stream":true}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), domain.PlatformAnthropic)
	require.NoError(t, err)
	parsed.OnUpstreamAccepted = func() {}

	clone, err := parsed.CloneForBody(parsed.Body.Bytes())
	require.NoError(t, err)
	require.Nil(t, clone.OnUpstreamAccepted)
	require.NotSame(t, parsed.Body, clone.Body)
	require.Equal(t, snapshotParsedRequest(parsed), snapshotParsedRequest(clone))

	// 克隆上的 ReplaceBody 不影响原请求。
	require.NoError(t, clone.ReplaceBody([]byte(`{"model":"other","messages":[]}`)))
	require.Equal(t, "claude-sonnet-4-5", parsed.Model)
	require.Equal(t, `[{"role":"user"}]`, string(parsed.MessagesRaw()))
}

func TestCloneForBody_AfterExternalModelOverrideReparses(t *testing.T) {
	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"claude-sonnet-4-5","messages":[]}`)), domain.PlatformAnthropic)
	require.NoError(t, err)
	parsed.Model = "overridden"
	clone, err := parsed.CloneForBody(parsed.Body.Bytes())
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet-4-5", clone.Model)
}
