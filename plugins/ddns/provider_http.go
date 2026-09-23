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
		return nil, 0, nil, errors.New("DNS API 请求失败，请检查服务器网络和插件权限")
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

func httpFailure(provider string, status int, code string) error {
	if code != "" {
		return fmt.Errorf("%s DNS 请求失败（HTTP %d，错误码 %s）", provider, status, boundedErrorCode(code))
	}
	return fmt.Errorf("%s DNS 请求失败（HTTP %d）", provider, status)
}
