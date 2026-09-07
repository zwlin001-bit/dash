package logx

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

var (
	// URI 格式密码打码：scheme://user:password@host 或 user:password@host/db 或 user:password@tcp(...)
	uriCredentialRegex = regexp.MustCompile(`((?:[a-zA-Z0-9+._-]+://|^|[\s"'` + "`" + `])(?:[a-zA-Z0-9._~%-]+):)([^/\s"'` + "`" + `]+)(@(?:\[[0-9a-fA-F:]+\]|[a-zA-Z0-9._~%-]+|tcp\(|unix\())`)

	// Oracle EZConnect 格式密码打码：user/password@host:port/service
	oracleSlashRegex = regexp.MustCompile(`((?:^|[\s"'` + "`" + `])(?:[a-zA-Z0-9._~%-]+)/)([^/\s"'` + "`" + `]+)(@(?:\[[0-9a-fA-F:]+\]|[a-zA-Z0-9._~%-]+|//))`)

	// Oracle TNS Connect Descriptor 内嵌密码打码：(PASSWORD=xxx)
	oracleTnsPasswordRegex = regexp.MustCompile(`(?i)(\(\s*PASSWORD\s*=\s*)([^)]+)(\))`)

	// 键值对敏感项打码：password=xxx, secret="xxx", wallet_password: 'xxx' 等
	keyValueSecretRegex = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|wallet_password|master_key)\b(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;)"'&]+)`)

	// Token 敏感项打码（保留后 4 位）：token=xxx, agent_token: "xxx" 等
	keyValueTokenRegex = regexp.MustCompile(`(?i)\b([a-zA-Z0-9_-]*token|api_key|access_key)\b(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;)"'&]+)`)

	// URL Query 参数敏感项打码：?password=xxx 或 &token=xxx
	urlQuerySecretRegex = regexp.MustCompile(`(?i)([?&](?:password|passwd|pwd|secret)=)[^&"'\s]+`)
	urlQueryTokenRegex  = regexp.MustCompile(`(?i)([?&](?:token|api_key|agent_token)=)[^&"'\s]+`)

	// Authorization Bearer Token 打码（保留后 4 位）
	bearerTokenRegex = regexp.MustCompile(`(?i)(Bearer\s+)([A-Za-z0-9_\-\.]+)`)
)

// RedactToken 对 Token 进行打码，保留后 4 位（例如：***abcd）。若长度 ≤ 4，则直接打码为 ***。
func RedactToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 4 {
		return "***"
	}
	return "***" + token[len(token)-4:]
}

// RedactDSN 专门处理 DSN 里内嵌的密码段。
// 匹配 password=… / //user:pw@ / user:pw@tcp(...) / user/pw@host 等形式，
// 只替换密码段为 ***，保留其余部分便于排查。
func RedactDSN(dsn string) string {
	if dsn == "" {
		return ""
	}

	result := dsn

	// 1. Oracle TNS Descriptor
	result = oracleTnsPasswordRegex.ReplaceAllString(result, "${1}***${3}")

	// 2. URI 凭据 user:pass@host
	result = uriCredentialRegex.ReplaceAllString(result, "${1}***${3}")

	// 3. Oracle EZConnect user/pass@host
	result = oracleSlashRegex.ReplaceAllString(result, "${1}***${3}")

	// 4. Key-Value 形式的 password=xxx
	result = keyValueSecretRegex.ReplaceAllStringFunc(result, func(m string) string {
		submatches := keyValueSecretRegex.FindStringSubmatch(m)
		if len(submatches) < 4 {
			return m
		}
		k := submatches[1]
		sep := submatches[2]
		val := submatches[3]

		if strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) {
			return fmt.Sprintf(`%s%s"***"`, k, sep)
		}
		if strings.HasPrefix(val, `'`) && strings.HasSuffix(val, `'`) {
			return fmt.Sprintf(`%s%s'***'`, k, sep)
		}
		return fmt.Sprintf(`%s%s***`, k, sep)
	})

	return result
}

// Redact 敏感信息打码辅助函数，是全工程唯一允许输出可能含机密内容的途径。
// 支持自动识别打码：
// 1. DSN 内嵌密码（只替换密码段）
// 2. 密码、wallet 密码打码为 ***
// 3. agent token / session token / enrollment token 等打码保留后 4 位（***abcd）
// 4. URL Query 参数中的密码和 token
// 5. Bearer token
func Redact(s string) string {
	if s == "" {
		return ""
	}

	// 1. DSN 识别打码
	result := RedactDSN(s)

	// 2. Key-Value Token 匹配打码（保留后 4 位）
	result = keyValueTokenRegex.ReplaceAllStringFunc(result, func(m string) string {
		submatches := keyValueTokenRegex.FindStringSubmatch(m)
		if len(submatches) < 4 {
			return m
		}
		k := submatches[1]
		sep := submatches[2]
		val := submatches[3]

		cleanVal := val
		quote := ""
		if (strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`)) || (strings.HasPrefix(val, `'`) && strings.HasSuffix(val, `'`)) {
			quote = string(val[0])
			cleanVal = val[1 : len(val)-1]
		}
		masked := RedactToken(cleanVal)
		return fmt.Sprintf(`%s%s%s%s%s`, k, sep, quote, masked, quote)
	})

	// 3. URL Query 中的 password
	result = urlQuerySecretRegex.ReplaceAllString(result, "${1}***")

	// 4. URL Query 中的 token
	result = urlQueryTokenRegex.ReplaceAllStringFunc(result, func(m string) string {
		submatches := urlQueryTokenRegex.FindStringSubmatch(m)
		if len(submatches) < 2 {
			return m
		}
		prefix := submatches[1]
		rawVal := m[len(prefix):]
		return prefix + RedactToken(rawVal)
	})

	// 5. Bearer Token
	result = bearerTokenRegex.ReplaceAllStringFunc(result, func(m string) string {
		submatches := bearerTokenRegex.FindStringSubmatch(m)
		if len(submatches) < 3 {
			return m
		}
		return submatches[1] + RedactToken(submatches[2])
	})

	return result
}

// RedactConfig 对任意配置结构体或映射进行安全脱敏并序列化为 JSON 字符串。
func RedactConfig(cfg any) string {
	if cfg == nil {
		return "{}"
	}

	sanitized := SanitizeAny(cfg)
	raw, err := json.Marshal(sanitized)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}

	// 双重防线：再经过一次正则 Redact
	return Redact(string(raw))
}

// isSensitiveKey 检查字段名或键名是否代表敏感信息
func isSensitiveKey(key string) bool {
	lower := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
	switch lower {
	case "password", "passwd", "pwd", "token", "secret", "walletpassword", "masterkey", "apikey", "accesskey", "privatekey":
		return true
	default:
		return strings.Contains(lower, "password") || strings.Contains(lower, "passwd") || strings.Contains(lower, "secret") || strings.Contains(lower, "token")
	}
}

// isTokenKey 检查是否属于 Token 类键
func isTokenKey(key string) bool {
	lower := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
	return strings.Contains(lower, "token")
}

const maxSanitizeDepth = 8

// SanitizeAny 对任意类型进行深度反射清洗（脱敏敏感字段、DSN 与 Token）。
// 包含最大深度上限（8层）和循环引用保护。
func SanitizeAny(v any) any {
	if v == nil {
		return nil
	}
	visited := make(map[uintptr]bool)
	return sanitizeInternal(reflect.ValueOf(v), 0, visited)
}

func sanitizeInternal(val reflect.Value, depth int, visited map[uintptr]bool) any {
	if !val.IsValid() {
		return nil
	}

	if depth > maxSanitizeDepth {
		return "[max depth exceeded]"
	}

	switch val.Kind() {
	case reflect.Pointer:
		if val.IsNil() {
			return nil
		}
		ptr := val.Pointer()
		if visited[ptr] {
			return "[circular reference]"
		}
		visited[ptr] = true
		defer delete(visited, ptr)

		elem := val.Elem()
		if elem.Kind() == reflect.String {
			return Redact(elem.String())
		}
		return sanitizeInternal(elem, depth+1, visited)

	case reflect.Interface:
		if val.IsNil() {
			return nil
		}
		return sanitizeInternal(val.Elem(), depth+1, visited)

	case reflect.Struct:
		if e, ok := val.Interface().(error); ok {
			return Redact(e.Error())
		}

		t := val.Type()
		hasExported := false
		for i := 0; i < t.NumField(); i++ {
			if t.Field(i).IsExported() {
				hasExported = true
				break
			}
		}

		if !hasExported {
			if s, ok := val.Interface().(fmt.Stringer); ok {
				return Redact(s.String())
			}
			return map[string]any{}
		}

		resMap := make(map[string]any, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}

			keyName := field.Name
			if tag := field.Tag.Get("json"); tag != "" {
				parts := strings.Split(tag, ",")
				if parts[0] == "-" {
					continue
				}
				if parts[0] != "" {
					keyName = parts[0]
				}
			} else if tag := field.Tag.Get("toml"); tag != "" {
				parts := strings.Split(tag, ",")
				if parts[0] == "-" {
					continue
				}
				if parts[0] != "" {
					keyName = parts[0]
				}
			}

			fieldVal := val.Field(i)
			actualVal := fieldVal
			for actualVal.Kind() == reflect.Interface {
				if actualVal.IsNil() {
					break
				}
				actualVal = actualVal.Elem()
			}

			isRedactTag := field.Tag.Get("redact") == "true"
			isSensitive := isRedactTag || isSensitiveKey(field.Name) || isSensitiveKey(keyName)
			isToken := isTokenKey(field.Name) || isTokenKey(keyName)

			if !actualVal.IsValid() || ((actualVal.Kind() == reflect.Pointer || actualVal.Kind() == reflect.Interface) && actualVal.IsNil()) {
				resMap[keyName] = nil
				continue
			}

			if isSensitive {
				if actualVal.Kind() == reflect.String {
					strVal := actualVal.String()
					if strVal == "" {
						resMap[keyName] = ""
					} else if isToken {
						resMap[keyName] = RedactToken(strVal)
					} else {
						resMap[keyName] = "***"
					}
				} else if actualVal.Kind() == reflect.Pointer && !actualVal.IsNil() && actualVal.Elem().Kind() == reflect.String {
					strVal := actualVal.Elem().String()
					if isToken {
						resMap[keyName] = RedactToken(strVal)
					} else {
						resMap[keyName] = "***"
					}
				} else {
					resMap[keyName] = "***"
				}
				continue
			}

			if (strings.EqualFold(field.Name, "dsn") || strings.EqualFold(keyName, "dsn")) && actualVal.Kind() == reflect.String {
				resMap[keyName] = RedactDSN(actualVal.String())
				continue
			}

			if actualVal.Kind() == reflect.String {
				resMap[keyName] = Redact(actualVal.String())
			} else {
				resMap[keyName] = sanitizeInternal(actualVal, depth+1, visited)
			}
		}
		return resMap

	case reflect.Map:
		if val.IsNil() {
			return nil
		}
		ptr := val.Pointer()
		if visited[ptr] {
			return "[circular reference]"
		}
		visited[ptr] = true
		defer delete(visited, ptr)

		resMap := make(map[string]any, val.Len())
		iter := val.MapRange()
		for iter.Next() {
			k := iter.Key()
			v := iter.Value()
			kStr := fmt.Sprintf("%v", k.Interface())

			actualVal := v
			for actualVal.Kind() == reflect.Interface {
				if actualVal.IsNil() {
					break
				}
				actualVal = actualVal.Elem()
			}

			isSensitive := isSensitiveKey(kStr)
			isToken := isTokenKey(kStr)

			if !actualVal.IsValid() || ((actualVal.Kind() == reflect.Pointer || actualVal.Kind() == reflect.Interface) && actualVal.IsNil()) {
				resMap[kStr] = nil
				continue
			}

			if isSensitive {
				if actualVal.Kind() == reflect.String {
					strVal := actualVal.String()
					if strVal == "" {
						resMap[kStr] = ""
					} else if isToken {
						resMap[kStr] = RedactToken(strVal)
					} else {
						resMap[kStr] = "***"
					}
				} else if actualVal.Kind() == reflect.Pointer && !actualVal.IsNil() && actualVal.Elem().Kind() == reflect.String {
					strVal := actualVal.Elem().String()
					if isToken {
						resMap[kStr] = RedactToken(strVal)
					} else {
						resMap[kStr] = "***"
					}
				} else {
					resMap[kStr] = "***"
				}
				continue
			}

			if strings.EqualFold(kStr, "dsn") && actualVal.Kind() == reflect.String {
				resMap[kStr] = RedactDSN(actualVal.String())
				continue
			}

			if actualVal.Kind() == reflect.String {
				resMap[kStr] = Redact(actualVal.String())
			} else {
				resMap[kStr] = sanitizeInternal(actualVal, depth+1, visited)
			}
		}
		return resMap

	case reflect.Slice, reflect.Array:
		if val.Kind() == reflect.Slice && val.IsNil() {
			return nil
		}
		if val.Type().Elem().Kind() == reflect.Uint8 {
			if val.Kind() == reflect.Slice {
				return Redact(string(val.Bytes()))
			}
			b := make([]byte, val.Len())
			reflect.Copy(reflect.ValueOf(b), val)
			return Redact(string(b))
		}

		if val.Kind() == reflect.Slice && val.Len() > 0 {
			ptr := val.Pointer()
			if visited[ptr] {
				return "[circular reference]"
			}
			visited[ptr] = true
			defer delete(visited, ptr)
		}

		resSlice := make([]any, val.Len())
		for i := 0; i < val.Len(); i++ {
			elem := val.Index(i)
			if elem.Kind() == reflect.String {
				resSlice[i] = Redact(elem.String())
			} else {
				resSlice[i] = sanitizeInternal(elem, depth+1, visited)
			}
		}
		return resSlice

	case reflect.String:
		return Redact(val.String())

	default:
		if val.CanInterface() {
			if e, ok := val.Interface().(error); ok {
				return Redact(e.Error())
			}
			if s, ok := val.Interface().(fmt.Stringer); ok {
				return Redact(s.String())
			}
			return val.Interface()
		}
		return nil
	}
}
