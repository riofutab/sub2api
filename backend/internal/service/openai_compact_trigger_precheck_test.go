//go:build unit

package service

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeCompactionTriggerInputOrder_NoTriggerSkipsDecoding(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":[` + strings.Repeat(`{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},`, 2000) + `{"type":"message","role":"user","content":"x"}],"stream":true}`)
	var got []byte
	var changed bool
	var err error
	allocs := testing.AllocsPerRun(5, func() {
		got, changed, err = NormalizeCompactionTriggerInputOrder(body)
	})
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, unsafe.SliceData(body), unsafe.SliceData(got))
	require.Zero(t, allocs, "不含 compaction_trigger 时不应解码请求体")
}

func TestNormalizeCompactionTriggerInputOrder_NoTriggerKeepsErrorSemantics(t *testing.T) {
	for _, body := range []string{
		`{"input":[`,
		`[{"type":"message"}]`,
		`"str"`,
		`{"input":[]} {"input":[]}`,
	} {
		_, changed, err := NormalizeCompactionTriggerInputOrder([]byte(body))
		require.Error(t, err, body)
		require.False(t, changed, body)
	}
	for _, body := range []string{`null`, ` {"input":"text"} `, `{}`} {
		_, changed, err := NormalizeCompactionTriggerInputOrder([]byte(body))
		require.NoError(t, err, body)
		require.False(t, changed, body)
	}
}

func TestNormalizeCompactionTriggerInputOrder_EscapedTriggerStillNormalized(t *testing.T) {
	body := []byte(`{"input":[{"type":"compaction` + jsonUnicodeEscapeForTest("005f") + `trigger"},{"type":"message","role":"user","content":"x"}]}`)
	require.NotContains(t, string(body), "compaction_trigger")
	got, changed, err := NormalizeCompactionTriggerInputOrder(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "compaction_trigger", gjson.GetBytes(got, "input.1.type").String())
}

func TestJSONMayContainEscapedLetter(t *testing.T) {
	esc := jsonUnicodeEscapeForTest
	cases := map[string]bool{
		`{"a":"plain"}`: false,
		`{"a":"` + esc("003c") + `div` + esc("003e") + ` ` + esc("0026") + `"}`: false,
		`{"a":"` + esc("4e2d") + esc("6587") + `"}`:                             false,
		`{"a":"` + esc("0041") + `"}`:                                           true,
		`{"a":"` + esc("005f") + `"}`:                                           true,
		`{"a":"` + esc("005F") + `"}`:                                           true,
		`{"a":"` + esc("0074") + `"}`:                                           true,
		`{"a":"` + esc("003c") + esc("0063") + `"}`:                             true,
		`{"a":"` + jsonUnicodeEscapeForTest("00"):                               false,
	}
	for body, want := range cases {
		require.Equal(t, want, JSONMayContainEscapedLetter([]byte(body)), body)
	}
}

func TestHasCompactionTriggerInInput_EscapedTypeStillDetected(t *testing.T) {
	esc := jsonUnicodeEscapeForTest
	require.True(t, HasCompactionTriggerInInput([]byte(`{"input":[{"type":"compaction`+esc("005F")+`trigger"}]}`)))
	require.False(t, HasCompactionTriggerInInput([]byte(`{"input":[{"type":"message","content":"`+esc("003c")+`b`+esc("003e")+`"}]}`)))
}
