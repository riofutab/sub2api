package requestmodel

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"unsafe"

	"github.com/tidwall/gjson"
)

// 重复的模型载体会被不同解析器绑定到不同值（gjson 取首键且大小写敏感，
// encoding/json 取末键且大小写不敏感，常见上游运行时取末键），
// 让「准入/路由/计费」与「实际转发执行」指向不同模型，网关边界应拒绝。
var (
	ErrDuplicateModelKey        = errors.New("model is specified more than once")
	ErrDuplicateSessionKey      = errors.New("session is specified more than once")
	ErrDuplicateSessionModelKey = errors.New("session.model is specified more than once")
)

// HasDuplicateTopLevelKey 判断 JSON 对象顶层是否存在重复的指定键（大小写不敏感，
// 比较解码后的键名，覆盖 \uXXXX 转义写法）。只检查顶层：嵌套对象里的同名键
// （如 messages、tools 内部结构）是合法内容。
func HasDuplicateTopLevelKey(body []byte, key string) bool {
	return HasDuplicateTopLevelKeyFunc(body, func(k string) bool { return strings.EqualFold(k, key) })
}

// HasDuplicateTopLevelKeyFunc 判断 JSON 对象顶层是否有两个以上键满足 matches。
func HasDuplicateTopLevelKeyFunc(body []byte, matches func(string) bool) bool {
	if len(body) == 0 {
		return false
	}
	return hasDuplicateTopLevelKey(unsafe.String(unsafe.SliceData(body), len(body)), matches)
}

// hasDuplicateTopLevelKey 零拷贝遍历顶层键；每个请求都会走这里，
// 不能用 gjson.ParseBytes（会把整个请求体复制成 string）。
func hasDuplicateTopLevelKey(raw string, matches func(string) bool) bool {
	seen, duplicate := false, false
	root := gjson.Result{Type: gjson.JSON, Raw: raw}
	root.ForEach(func(k, _ gjson.Result) bool {
		if !matches(k.Str) {
			return true
		}
		if seen {
			duplicate = true
			return false
		}
		seen = true
		return true
	})
	return duplicate
}

func isModelKey(k string) bool   { return strings.EqualFold(k, "model") }
func isSessionKey(k string) bool { return strings.EqualFold(k, "session") }

// ValidateBody 在路由改写、调度与计费之前拒绝有歧义的模型载体：
// 顶层重复 model；Live 入口还检查重复 session 与 session 内重复 model。
// 语法错误留给各入口自行报告（这里不做整体校验，避免额外的全量扫描）。
func ValidateBody(routePath, contentType string, body []byte) error {
	live := IsLiveRequestRoute(routePath)
	if isMultipartContentType(contentType) {
		return validateMultipartBody(contentType, body, live)
	}
	if len(body) == 0 {
		return nil
	}
	raw := unsafe.String(unsafe.SliceData(body), len(body))
	if hasDuplicateTopLevelKey(raw, isModelKey) {
		return ErrDuplicateModelKey
	}
	if !live {
		return nil
	}
	sessions := 0
	var err error
	root := gjson.Result{Type: gjson.JSON, Raw: raw}
	root.ForEach(func(k, v gjson.Result) bool {
		if !isSessionKey(k.Str) {
			return true
		}
		if sessions++; sessions > 1 {
			err = ErrDuplicateSessionKey
			return false
		}
		if hasDuplicateTopLevelKey(v.Raw, isModelKey) {
			err = ErrDuplicateSessionModelKey
			return false
		}
		return true
	})
	return err
}

// validateMultipartBody 只看非文件字段 model/session，合法的多个图片文件不受影响。
// multipart 格式错误留给入口解析器报告。
func validateMultipartBody(contentType string, body []byte, live bool) error {
	_, params, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		return nil
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	models, sessions := 0, 0
	for {
		part, err := reader.NextPart()
		if err != nil {
			return nil
		}
		if part.FileName() != "" {
			continue
		}
		name := strings.TrimSpace(part.FormName())
		switch {
		case isModelKey(name):
			if models++; models > 1 {
				return ErrDuplicateModelKey
			}
		case live && isSessionKey(name):
			if sessions++; sessions > 1 {
				return ErrDuplicateSessionKey
			}
			session, err := io.ReadAll(part)
			if err != nil {
				return nil
			}
			if HasDuplicateTopLevelKey(session, "model") {
				return ErrDuplicateSessionModelKey
			}
		}
	}
}
