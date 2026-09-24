package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/komari-monitor/komari/pkg/pluginprocess"
)

const providerMaxBody = 4 << 20

func providerRequest(ctx context.Context, client *pluginprocess.Client, method, address string, headers http.Header, body []byte) ([]byte, int, http.Header, error) {
	if len(body) > providerMaxBody {
		return nil, 0, nil, errors.New("DNS API 请求内容过大")
	}
	response, err := client.HTTPRequest(ctx, pluginprocess.HTTPRequest{
		URL: address, Method: method, Headers: headers, Body: body,
	})
	if err != nil {
		return nil, 0, nil, fmt.Errorf("DNS API 请求失败：%w", err)
	}
	if len(response.Body) > providerMaxBody {
		return nil, response.Status, response.Headers, errors.New("DNS API 响应内容过大")
	}
	return response.Body, response.Status, response.Headers, nil
}

func jsonHeaders() http.Header {
	return http.Header{"Content-Type": []string{"application/json"}, "Accept": []string{"application/json"}}
}

func marshalBody(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("无法编码 DNS API 请求")
	}
	return data, nil
}

func boundedErrorCode(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 80 {
		value = value[:80]
	}
	return value
}

func sanitizeHuaweiCode(value string) string {
	var safe strings.Builder
	for _, char := range strings.TrimSpace(value) {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("._-", char) {
			safe.WriteRune(char)
		}
		if safe.Len() >= 80 {
			break
		}
	}
	return safe.String()
}

func huaweiRequestID(headers http.Header) string {
	for _, name := range []string{"X-Request-Id", "X-Apig-Request-Id", "X-Apigw-Request-Id", "X-Trace-Id"} {
		value := strings.TrimSpace(headers.Get(name))
		if value == "" {
			continue
		}
		var safe strings.Builder
		for _, char := range value {
			if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("-_.:", char) {
				safe.WriteRune(char)
			} else {
				safe.WriteByte('_')
			}
			if safe.Len() >= 128 {
				break
			}
		}
		return safe.String()
	}
	return ""
}

func sanitizeHuaweiError(value string, credentials huaweiCredentials) string {
	value = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(value)
	for _, secret := range []string{credentials.AccessKey, credentials.SecretKey} {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 384 {
		value = string(runes[:384]) + "…"
	}
	return value
}

func formatHuaweiHTTPFailure(region, method, host, path string, status int, code, requestID, detail string) string {
	parts := []string{fmt.Sprintf("HTTP %d", status)}
	if code = boundedErrorCode(code); code != "" {
		parts = append(parts, "错误码 "+code)
	}
	if region = strings.TrimSpace(region); region != "" {
		parts = append(parts, "区域 "+region)
	}
	if host = strings.TrimSpace(host); host != "" {
		parts = append(parts, fmt.Sprintf("接口 %s https://%s%s", strings.TrimSpace(method), host, path))
	}
	if requestID = strings.TrimSpace(requestID); requestID != "" {
		parts = append(parts, "请求 ID "+requestID)
	}
	if detail = strings.TrimSpace(detail); detail != "" {
		parts = append(parts, "服务端说明 "+detail)
	}
	return "华为云 DNS 请求失败（" + strings.Join(parts, "；") + "）"
}

func httpFailure(provider string, status int, code string) error {
	if code != "" {
		return fmt.Errorf("%s DNS 请求失败（HTTP %d，错误码 %s）", provider, status, boundedErrorCode(code))
	}
	return fmt.Errorf("%s DNS 请求失败（HTTP %d）", provider, status)
}
