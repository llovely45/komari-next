package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/komari-monitor/komari/pkg/pluginprocess"
)

const cloudflareAPI = "https://api.cloudflare.com/client/v4"

type cloudflareProvider struct {
	client       *pluginprocess.Client
	credentials  cloudflareCredentials
	blockedUntil *atomic.Int64
	zones        map[string]string
	zonesExpires time.Time
}

type cfAPIError struct {
	Status int
	Text   string
}

func (e *cfAPIError) Error() string { return e.Text }

func newCloudflareProvider(client *pluginprocess.Client, credentials cloudflareCredentials, blocked *atomic.Int64) *cloudflareProvider {
	return &cloudflareProvider{client: client, credentials: credentials, blockedUntil: blocked, zones: make(map[string]string)}
}

func (p *cloudflareProvider) isRateLimited(err error) bool {
	var apiErr *cfAPIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusTooManyRequests
}

func (p *cloudflareProvider) request(ctx context.Context, path, method string, payload any) (json.RawMessage, cfResultInfo, error) {
	if until := p.blockedUntil.Load(); until > time.Now().UnixMilli() {
		return nil, cfResultInfo{}, &cfAPIError{Status: 429, Text: "Cloudflare 正在限流，插件会在等待期结束后重试"}
	}
	var body []byte
	var err error
	if payload != nil {
		body, err = marshalBody(payload)
		if err != nil {
			return nil, cfResultInfo{}, err
		}
	}
	headers := jsonHeaders()
	if p.credentials.Mode == "global" && p.credentials.Email != "" && p.credentials.Key != "" {
		headers.Set("X-Auth-Email", p.credentials.Email)
		headers.Set("X-Auth-Key", p.credentials.Key)
	} else {
		headers.Set("Authorization", "Bearer "+p.credentials.Token)
	}
	responseBody, status, responseHeaders, err := providerRequest(ctx, p.client, method, cloudflareAPI+path, headers, body)
	if err != nil {
		return nil, cfResultInfo{}, err
	}
	if status == http.StatusTooManyRequests {
		delay := time.Minute
		if seconds, parseErr := strconv.Atoi(strings.TrimSpace(responseHeaders.Get("Retry-After"))); parseErr == nil && seconds > 0 {
			delay = time.Duration(seconds) * time.Second
		} else if retryAt, parseErr := http.ParseTime(responseHeaders.Get("Retry-After")); parseErr == nil && retryAt.After(time.Now()) {
			delay = time.Until(retryAt)
		}
		if delay < time.Minute {
			delay = time.Minute
		}
		if delay > 24*time.Hour {
			delay = 24 * time.Hour
		}
		p.blockedUntil.Store(time.Now().Add(delay).UnixMilli())
		return nil, cfResultInfo{}, &cfAPIError{Status: status, Text: "Cloudflare API 限流，等待限流窗口结束后会自动重试"}
	}
	var envelope struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
		Info    cfResultInfo    `json:"result_info"`
		Errors  []struct {
			Code int `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return nil, cfResultInfo{}, fmt.Errorf("Cloudflare 返回无效响应（HTTP %d）", status)
	}
	if status < 200 || status >= 300 || !envelope.Success {
		codes := make([]string, 0, len(envelope.Errors))
		for _, item := range envelope.Errors {
			codes = append(codes, strconv.Itoa(item.Code))
		}
		return nil, cfResultInfo{}, &cfAPIError{Status: status, Text: httpFailure("Cloudflare", status, strings.Join(codes, ", ")).Error()}
	}
	return envelope.Result, envelope.Info, nil
}

type cfResultInfo struct {
	TotalPages int `json:"total_pages"`
}

type cfZone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (p *cloudflareProvider) test(ctx context.Context) error {
	_, _, err := p.request(ctx, "/zones?per_page=1", http.MethodGet, nil)
	return err
}

func (p *cloudflareProvider) zoneFor(ctx context.Context, domain string) (string, error) {
	now := time.Now()
	if now.After(p.zonesExpires) {
		p.zones = make(map[string]string)
		p.zonesExpires = now.Add(5 * time.Minute)
	}
	labels := strings.Split(strings.TrimPrefix(domain, "*."), ".")
	for len(labels) >= 2 {
		candidate := strings.Join(labels, ".")
		if id := p.zones[candidate]; id != "" {
			return id, nil
		}
		path := "/zones?name=" + url.QueryEscape(candidate) + "&per_page=50"
		result, _, err := p.request(ctx, path, http.MethodGet, nil)
		if err != nil {
			return "", err
		}
		var zones []cfZone
		if err := json.Unmarshal(result, &zones); err != nil {
			return "", errors.New("Cloudflare Zone 列表格式无效")
		}
		for _, zone := range zones {
			if zone.Name == candidate && zone.Status == "active" {
				p.zones[candidate] = zone.ID
				return zone.ID, nil
			}
		}
		labels = labels[1:]
	}
	return "", errors.New("Cloudflare 账户中没有找到该域名对应的启用 Zone")
}

type cfDNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment"`
}

func (p *cloudflareProvider) listRecords(ctx context.Context, zone, domain string) ([]cfDNSRecord, error) {
	all := make([]cfDNSRecord, 0)
	for page := 1; page <= 100; page++ {
		path := "/zones/" + url.PathEscape(zone) + "/dns_records?name=" + url.QueryEscape(domain) + "&per_page=100&page=" + strconv.Itoa(page)
		data, info, err := p.request(ctx, path, http.MethodGet, nil)
		if err != nil {
			return nil, err
		}
		var records []cfDNSRecord
		if err := json.Unmarshal(data, &records); err != nil {
			return nil, errors.New("Cloudflare DNS 列表格式无效")
		}
		all = append(all, records...)
		pages := info.TotalPages
		if pages < 1 {
			pages = 1
		}
		if page >= pages {
			return all, nil
		}
	}
	return nil, errors.New("同名 Cloudflare DNS 记录数量过多，已停止同步")
}

func (p *cloudflareProvider) sync(ctx context.Context, value rule, uuid, address string) (string, error) {
	zone, err := p.zoneFor(ctx, value.Domain)
	if err != nil {
		return "", err
	}
	records, err := p.listRecords(ctx, zone, value.Domain)
	if err != nil {
		return "", err
	}
	for _, record := range records {
		if record.Type == "CNAME" || record.Type == "NS" {
			return "", errors.New("域名已有 CNAME 或 NS 记录，未改动 Cloudflare DNS")
		}
	}
	marker := "komari-ddns:" + value.ID + ":" + uuid
	related := "komari-ddns:" + value.ID + ":"
	owned := make([]cfDNSRecord, 0)
	unowned := make([]cfDNSRecord, 0)
	for _, record := range records {
		if record.Type != value.Type {
			continue
		}
		if record.Comment == marker {
			owned = append(owned, record)
		} else if !strings.HasPrefix(record.Comment, related) {
			unowned = append(unowned, record)
		}
	}
	if len(owned) > 1 {
		return "", errors.New("Cloudflare 中存在重复的插件归属记录，请先整理 DNS")
	}
	var record *cfDNSRecord
	if len(owned) == 1 {
		record = &owned[0]
	} else if len(unowned) > 0 {
		if value.AdoptExisting && (value.Source == "manual" || len(value.Servers) == 1) && len(unowned) == 1 && unowned[0].Comment == "" {
			record = &unowned[0]
		} else {
			return "", errors.New("存在未由此规则管理的同类型 DNS 记录；请先检查记录，或开启单记录接管")
		}
	}
	ttl := value.TTL
	if value.Proxied {
		ttl = 1
	}
	if record != nil && sameIP(record.Content, address) && record.Proxied == value.Proxied && record.TTL == ttl && record.Comment == marker {
		return "unchanged", nil
	}
	payload := map[string]any{"type": value.Type, "name": value.Domain, "content": address, "ttl": ttl, "proxied": value.Proxied, "comment": marker}
	body, err := marshalBody(payload)
	if err != nil {
		return "", err
	}
	path := "/zones/" + url.PathEscape(zone) + "/dns_records"
	method := http.MethodPost
	if record != nil {
		path += "/" + url.PathEscape(record.ID)
		method = http.MethodPatch
	}
	if _, _, err := p.request(ctx, path, method, json.RawMessage(body)); err != nil {
		return "", err
	}
	if record != nil {
		return "updated", nil
	}
	return "created", nil
}

func sameIP(first, second string) bool {
	one, two := net.ParseIP(first), net.ParseIP(second)
	return one != nil && two != nil && one.Equal(two)
}
