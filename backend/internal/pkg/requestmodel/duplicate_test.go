package requestmodel

import (
	"bytes"
	"mime/multipart"
	"testing"

	"github.com/stretchr/testify/require"
)

// escapedModelKey 是 "model" 的 JSON 转义写法 model，用 rune(92) 拼出反斜杠以免被编辑工具反转义。
var escapedModelKey = "mod" + string(rune(92)) + "u0065l"

func TestHasDuplicateTopLevelKey(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"single model key", `{"model":"a","messages":[]}`, false},
		{"duplicate model key", `{"model":"a","messages":[],"model":"b"}`, true},
		{"nested same key is not top level", `{"model":"a","messages":[{"model":"b"}]}`, false},
		{"duplicate of another key ignored", `{"stream":false,"model":"a","stream":true}`, false},
		{"key missing", `{"messages":[]}`, false},
		{"empty body", ``, false},
		{"case variant duplicate", `{"model":"a","Model":"b"}`, true},
		{"upper case duplicate", `{"MODEL":"a","messages":[],"model":"b"}`, true},
		{"escaped key duplicate", `{"model":"a","` + escapedModelKey + `":"b"}`, true},
		{"null value still counts", `{"model":"a","MODEL":null}`, true},
		{"nested model in tools is not top level", `{"model":"a","tools":[{"input_schema":{"properties":{"model":{"type":"string"}}}}]}`, false},
		{"leading whitespace", " \n{\"model\":\"a\",\"model\":\"b\"}", true},
		{"top level array", `[{"model":"a"},{"model":"b"}]`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, HasDuplicateTopLevelKey([]byte(tc.body), "model"))
		})
	}
}

func TestValidateBodyJSON(t *testing.T) {
	cases := []struct {
		name, route, body string
		want              error
	}{
		{"single model", "/v1/responses", `{"model":"a","input":[]}`, nil},
		{"duplicate model", "/v1/responses", `{"model":"a","model":"a"}`, ErrDuplicateModelKey},
		{"nested duplicates are content", "/v1/responses", `{"model":"a","input":[{"model":"b","model":"c"}]}`, nil},
		{"session ignored outside live", "/v1/responses", `{"model":"a","session":{"model":"b","model":"c"},"Session":{}}`, nil},
		{"malformed left to endpoint", "/v1/responses", `{"model":`, nil},
		{"live single session", "/v1/live", `{"sdp":"v=0","session":{"model":"a"}}`, nil},
		{"live duplicate session", "/backend-api/codex/realtime/calls", `{"session":{"model":"a"},"Session":null}`, ErrDuplicateSessionKey},
		{"live duplicate session model", "/v1/live", `{"session":{"model":"a","Model":"b"}}`, ErrDuplicateSessionModelKey},
		{"live session not object", "/v1/live", `{"session":"x"}`, nil},
		{"live top level duplicate model", "/live", `{"model":"a","model":"b","session":{"model":"c"}}`, ErrDuplicateModelKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ValidateBody(tc.route, "application/json", []byte(tc.body)))
		})
	}
}

func TestValidateBodyMultipart(t *testing.T) {
	cases := []struct {
		name, route string
		fields      [][2]string
		want        error
	}{
		{"single model with images", "/v1/images/edits", [][2]string{{"model", "a"}}, nil},
		{"duplicate model", "/v1/images/edits", [][2]string{{"model", "a"}, {"Model", "b"}}, ErrDuplicateModelKey},
		{"duplicate session outside live", "/v1/images/edits", [][2]string{{"session", "{}"}, {"session", "{}"}}, nil},
		{"live duplicate session", "/v1/live", [][2]string{{"session", `{"model":"a"}`}, {"session", `{"model":"a"}`}}, ErrDuplicateSessionKey},
		{"live session model", "/v1/live", [][2]string{{"session", `{"model":"a","model":"b"}`}}, ErrDuplicateSessionModelKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			w := multipart.NewWriter(&body)
			// 浏览器的 boundary 含大写字母，校验必须使用原始 Content-Type。
			require.NoError(t, w.SetBoundary("----WebKitFormBoundaryAbCdEf0123"))
			for _, field := range tc.fields {
				require.NoError(t, w.WriteField(field[0], field[1]))
			}
			for range 2 {
				part, err := w.CreateFormFile("image[]", "a.png")
				require.NoError(t, err)
				_, err = part.Write([]byte(`{"model":"file data"}`))
				require.NoError(t, err)
			}
			require.NoError(t, w.Close())
			require.Equal(t, tc.want, ValidateBody(tc.route, w.FormDataContentType(), body.Bytes()))
		})
	}
}
