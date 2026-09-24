package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/komari-monitor/komari/pkg/pluginprocess"
)

type huaweiProvider struct {
	client      *pluginprocess.Client
	credentials huaweiCredentials
	host        string
}

func newHuaweiProvider(client *pluginprocess.Client, credentials huaweiCredentials) *huaweiProvider {
	return &huaweiProvider{client: client, credentials: credentials, host: "dns." + credentials.Region + ".myhuaweicloud.com"}
}

func (p *huaweiProvider) request(ctx context.Context, method, path string, query map[string]string, payload any) (json.RawMessage, error) {
	canonicalQuery := canonicalQuery(query)
	address := "https://" + p.host + path
	if canonicalQuery != "" {
		address += "?" + canonicalQuery
	}
	var body []byte
	var err error
	if payload != nil {
		body, err = marshalBody(payload)
		if err != nil {
			return nil, err
		}
	}
	date := time.Now().UTC().Format("20060102T150405Z")
	signature := huaweiSignature(method, path, canonicalQuery, date, body, p.credentials.SecretKey)
	headers := jsonHeaders()
	headers.Set("X-Sdk-Date", date)
	headers.Set("Authorization", "SDK-HMAC-SHA256 Access="+p.credentials.AccessKey+", SignedHeaders=x-sdk-date, Signature="+signature)
	responseBody, status, responseHeaders, err := providerRequest(ctx, p.client, method, address, headers, body)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		var failure struct {
			ErrorCode string `json:"error_code"`
			Code      string `json:"code"`
			ErrorMsg  string `json:"error_msg"`
		}
		_ = json.Unmarshal(responseBody, &failure)
		code := strings.TrimSpace(failure.ErrorCode)
		if code == "" {
			code = strings.TrimSpace(failure.Code)
		}
		code = sanitizeHuaweiCode(code)
		requestID := huaweiRequestID(responseHeaders)
		detail := sanitizeHuaweiError(failure.ErrorMsg, p.credentials)
		message := formatHuaweiHTTPFailure(p.credentials.Region, method, p.host, path, status, code, requestID, detail)
		if p.client != nil {
			_ = p.client.Log(ctx, fmt.Sprintf(
				"[huawei-dns] plugin_version=%s region=%s endpoint=%s method=%s path=%s status=%d code=%s request_id=%s error_msg=%q",
				pluginVersion,
				p.credentials.Region,
				p.host,
				method,
				path,
				status,
				code,
				requestID,
				detail,
			))
		}
		return nil, errors.New(message)
	}
	return responseBody, nil
}

func (p *huaweiProvider) test(ctx context.Context) error {
	_, err := p.listZones(ctx, "")
	return err
}

type huaweiZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type huaweiLine struct {
	ID        string `json:"line"`
	ParentID  string `json:"father_id,omitempty"`
	Name      string `json:"line_name"`
	Available bool   `json:"available"`
}

type huaweiSystemLine struct {
	ID       string `json:"id"`
	ParentID string `json:"father_id"`
	Name     string `json:"name"`
}

func (p *huaweiProvider) listZones(ctx context.Context, name string) ([]huaweiZone, error) {
	query := map[string]string{"type": "public", "limit": "100"}
	if name != "" {
		query["name"] = name
	}
	body, err := p.request(ctx, http.MethodGet, "/v2/zones", query, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Zones []huaweiZone `json:"zones"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Zones == nil {
		return nil, errors.New("华为云 DNS Zone 列表格式无效")
	}
	return result.Zones, nil
}

func (p *huaweiProvider) zoneFor(ctx context.Context, domain string) (string, error) {
	labels := strings.Split(strings.TrimPrefix(domain, "*."), ".")
	for len(labels) >= 2 {
		candidate := strings.Join(labels, ".") + "."
		zones, err := p.listZones(ctx, candidate)
		if err != nil {
			return "", err
		}
		for _, zone := range zones {
			if strings.EqualFold(strings.TrimSuffix(zone.Name, "."), strings.TrimSuffix(candidate, ".")) {
				return zone.ID, nil
			}
		}
		labels = labels[1:]
	}
	return "", errors.New("当前华为云区域中没有找到该域名对应的公网 DNS Zone")
}

func (p *huaweiProvider) listLines(ctx context.Context, domain string) ([]huaweiLine, error) {
	zone, err := p.zoneFor(ctx, domain)
	if err != nil {
		return nil, err
	}
	path := "/v2.1/zones/" + pathSegment(zone) + "/lines"
	body, err := p.request(ctx, http.MethodGet, path, map[string]string{"limit": "500", "offset": "0"}, nil)
	if err != nil {
		return nil, err
	}
	var zoneResponse struct {
		Lines []huaweiLine `json:"lines"`
	}
	if err := json.Unmarshal(body, &zoneResponse); err != nil || zoneResponse.Lines == nil {
		return nil, errors.New("华为云 DNS 解析线路列表格式无效")
	}
	available := make(map[string]huaweiLine, len(zoneResponse.Lines)+1)
	for _, line := range zoneResponse.Lines {
		line.ID = strings.TrimSpace(line.ID)
		line.Name = strings.TrimSpace(line.Name)
		if line.ID != "" {
			line.Available = true
			available[line.ID] = line
		}
	}
	if _, ok := available["default"]; !ok {
		available["default"] = huaweiLine{ID: "default", Name: "全网默认", Available: true}
	}

	body, err = p.request(ctx, http.MethodGet, "/v2.1/system-lines", map[string]string{"locale": "zh-cn", "limit": "1000", "offset": "0"}, nil)
	if err != nil {
		return nil, err
	}
	var systemResponse struct {
		Lines []huaweiSystemLine `json:"lines"`
	}
	if err := json.Unmarshal(body, &systemResponse); err != nil || systemResponse.Lines == nil {
		return nil, errors.New("华为云 DNS 系统线路列表格式无效")
	}
	lines := make([]huaweiLine, 0, len(systemResponse.Lines)+len(available))
	seen := make(map[string]bool, len(systemResponse.Lines)+len(available))
	for _, systemLine := range systemResponse.Lines {
		id := strings.TrimSpace(systemLine.ID)
		if id == "" {
			continue
		}
		line, ok := available[id]
		line.ID = id
		line.ParentID = strings.TrimSpace(systemLine.ParentID)
		line.Available = ok
		if name := strings.TrimSpace(systemLine.Name); name != "" {
			line.Name = name
		}
		if line.Name == "" {
			line.Name = id
		}
		if id == "default" {
			line.Name = "全网默认"
			line.Available = true
		}
		lines = append(lines, line)
		seen[id] = true
	}
	for id, line := range available {
		if seen[id] {
			continue
		}
		if id == "default" {
			line.Name = "全网默认"
		}
		line.Available = true
		lines = append(lines, line)
	}
	sort.Slice(lines, func(i, j int) bool {
		if lines[i].ID == "default" || lines[j].ID == "default" {
			return lines[i].ID == "default" && lines[j].ID != "default"
		}
		if lines[i].ParentID == lines[j].ParentID {
			return lines[i].Name < lines[j].Name
		}
		return lines[i].ParentID < lines[j].ParentID
	})
	return lines, nil
}

type huaweiRecordSet struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Line        string   `json:"line"`
	TTL         int      `json:"ttl"`
	Records     []string `json:"records"`
	Description string   `json:"description"`
}

func (p *huaweiProvider) listRecordSets(ctx context.Context, zone, domain string) ([]huaweiRecordSet, error) {
	path := "/v2.1/recordsets"
	body, err := p.request(ctx, http.MethodGet, path, map[string]string{
		"limit":       "500",
		"name":        domain + ".",
		"search_mode": "equal",
		"zone_id":     zone,
		"zone_type":   "public",
	}, nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		RecordSets []huaweiRecordSet `json:"recordsets"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.RecordSets == nil {
		return nil, errors.New("华为云 DNS 记录列表格式无效")
	}
	result := make([]huaweiRecordSet, 0, len(response.RecordSets))
	for _, value := range response.RecordSets {
		if strings.TrimSpace(value.Line) == "" {
			value.Line = "default"
		}
		if strings.EqualFold(strings.TrimSuffix(value.Name, "."), domain) {
			result = append(result, value)
		}
	}
	return result, nil
}

func (p *huaweiProvider) sync(ctx context.Context, value rule, addresses []string) (string, []string, error) {
	line := strings.TrimSpace(value.Line)
	if line == "" {
		line = "default"
	}
	zone, err := p.zoneFor(ctx, value.Domain)
	if err != nil {
		return "", nil, err
	}
	recordsets, err := p.listRecordSets(ctx, zone, value.Domain)
	if err != nil {
		return "", nil, err
	}
	for _, recordset := range recordsets {
		if recordset.Type == "CNAME" || recordset.Type == "NS" {
			return "", nil, errors.New("域名已有 CNAME 或 NS 记录，未改动华为云 DNS")
		}
	}
	matches := make([]huaweiRecordSet, 0)
	owned := make([]huaweiRecordSet, 0)
	for _, recordset := range recordsets {
		if recordset.Type != value.Type || recordset.Line != line {
			continue
		}
		matches = append(matches, recordset)
		if recordset.Description == "komari-ddns:"+value.ID {
			owned = append(owned, recordset)
		}
	}
	if len(owned) > 1 {
		return "", nil, errors.New("华为云中存在重复的插件归属记录，请先整理 DNS")
	}
	var recordset *huaweiRecordSet
	if len(owned) == 1 {
		recordset = &owned[0]
	} else if len(matches) > 0 {
		if value.AdoptExisting && len(matches) == 1 && matches[0].Description == "" {
			recordset = &matches[0]
		} else {
			return "", nil, errors.New("存在未由此规则管理的同类型 DNS 记录；请先检查记录，或开启接管")
		}
	}
	addresses = uniqueSorted(addresses)
	marker := "komari-ddns:" + value.ID
	if recordset != nil && equalStringSlices(uniqueSorted(recordset.Records), addresses) && recordset.TTL == value.TTL && recordset.Description == marker {
		return "unchanged", addresses, nil
	}
	payload := map[string]any{"name": value.Domain + ".", "type": value.Type, "line": line, "ttl": value.TTL, "records": addresses, "description": marker}
	path := "/v2.1/zones/" + pathSegment(zone) + "/recordsets"
	method := http.MethodPost
	if recordset != nil {
		path += "/" + pathSegment(recordset.ID)
		method = http.MethodPut
		delete(payload, "line")
	}
	if _, err := p.request(ctx, method, path, nil, payload); err != nil {
		return "", nil, err
	}
	if recordset != nil {
		return "updated", addresses, nil
	}
	return "created", addresses, nil
}

func canonicalQuery(query map[string]string) string {
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, awsEncode(key)+"="+awsEncode(query[key]))
	}
	return strings.Join(values, "&")
}

func canonicalURI(path string) string {
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		if decoded, err := url.PathUnescape(segment); err == nil {
			segment = decoded
		}
		segments[index] = awsEncode(segment)
	}
	canonical := strings.Join(segments, "/")
	if canonical == "" {
		return "/"
	}
	if !strings.HasSuffix(canonical, "/") {
		canonical += "/"
	}
	return canonical
}

func huaweiSignature(method, path, query, date string, body []byte, secret string) string {
	canonicalHeaders := "x-sdk-date:" + date + "\n"
	canonicalRequest := method + "\n" + canonicalURI(path) + "\n" + query + "\n" + canonicalHeaders + "\n" + "x-sdk-date" + "\n" + sha256Hex(body)
	stringToSign := "SDK-HMAC-SHA256\n" + date + "\n" + sha256Hex([]byte(canonicalRequest))
	return hmacHex(secret, stringToSign)
}

func awsEncode(value string) string {
	const hexChars = "0123456789ABCDEF"
	var result strings.Builder
	for _, current := range []byte(value) {
		if (current >= 'a' && current <= 'z') || (current >= 'A' && current <= 'Z') || (current >= '0' && current <= '9') || strings.ContainsRune("-_.~", rune(current)) {
			result.WriteByte(current)
		} else {
			result.WriteByte('%')
			result.WriteByte(hexChars[current>>4])
			result.WriteByte(hexChars[current&15])
		}
	}
	return result.String()
}

func pathSegment(value string) string {
	return strings.ReplaceAll(url.PathEscape(value), "+", "%20")
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func hmacHex(secret, value string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func equalStringSlices(first, second []string) bool {
	if len(first) != len(second) {
		return false
	}
	for i := range first {
		if first[i] != second[i] {
			return false
		}
	}
	return true
}
