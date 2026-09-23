package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"
)

type cloudflareCredentials struct {
	Mode  string `json:"mode"`
	Token string `json:"token"`
	Email string `json:"email"`
	Key   string `json:"key"`
}

type huaweiCredentials struct {
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	Region    string `json:"region"`
}

type schedule struct {
	Enabled          bool   `json:"enabled"`
	Start            string `json:"start"`
	End              string `json:"end"`
	Days             []int  `json:"days"`
	UTCOffsetMinutes int    `json:"utcOffsetMinutes"`
}

type rule struct {
	ID            string   `json:"id"`
	Provider      string   `json:"provider"`
	Domain        string   `json:"domain"`
	Type          string   `json:"type"`
	Source        string   `json:"source,omitempty"`
	ManualIP      string   `json:"manualIP,omitempty"`
	Line          string   `json:"line,omitempty"`
	Interval      int      `json:"interval"`
	Servers       []string `json:"servers"`
	Enabled       bool     `json:"enabled"`
	Proxied       bool     `json:"proxied"`
	AdoptExisting bool     `json:"adoptExisting"`
	Schedule      schedule `json:"schedule"`
	TTL           int      `json:"ttl"`
}

type config struct {
	Version    int                   `json:"version"`
	Cloudflare cloudflareCredentials `json:"cloudflare"`
	Huawei     huaweiCredentials     `json:"huaweicloud"`
	Rules      []rule                `json:"rules"`
}

type saveCredentials struct {
	CloudflareMode  string `json:"cloudflareMode"`
	CloudflareToken string `json:"cloudflareToken"`
	CloudflareEmail string `json:"cloudflareEmail"`
	CloudflareKey   string `json:"cloudflareKey"`
	HuaweiAccessKey string `json:"huaweiAccessKey"`
	HuaweiSecretKey string `json:"huaweiSecretKey"`
	HuaweiRegion    string `json:"huaweiRegion"`
	ClearCloudflare bool   `json:"clearCloudflare"`
	ClearHuawei     bool   `json:"clearHuawei"`
}

type saveInput struct {
	Credentials saveCredentials `json:"credentials"`
	Rules       []rule          `json:"rules"`
}

type testInput struct {
	Provider    string          `json:"provider"`
	Credentials saveCredentials `json:"credentials"`
}

type clientInfo struct {
	UUID  string `json:"uuid"`
	Name  string `json:"name"`
	IPv4  string `json:"ipv4"`
	IPv6  string `json:"ipv6"`
	Group string `json:"group"`
}

type result struct {
	UUID    string   `json:"uuid,omitempty"`
	Action  string   `json:"action"`
	IP      string   `json:"ip,omitempty"`
	IPs     []string `json:"ips,omitempty"`
	Message string   `json:"message,omitempty"`
}

type runStatus struct {
	StartedAt  int64    `json:"startedAt,omitempty"`
	FinishedAt int64    `json:"finishedAt,omitempty"`
	NextAt     int64    `json:"nextAt,omitempty"`
	Outcome    string   `json:"outcome,omitempty"`
	Results    []result `json:"results,omitempty"`
	Error      string   `json:"error,omitempty"`
}

type publicConfig struct {
	Cloudflare struct {
		Mode         string `json:"mode"`
		HasToken     bool   `json:"hasToken"`
		HasLegacyKey bool   `json:"hasLegacyKey"`
	} `json:"cloudflare"`
	Huawei struct {
		HasCredentials bool   `json:"hasCredentials"`
		Region         string `json:"region"`
	} `json:"huaweicloud"`
	Rules []rule `json:"rules"`
}

type publicState struct {
	Config       publicConfig         `json:"config"`
	History      map[string]runStatus `json:"history"`
	Running      bool                 `json:"running"`
	Saving       bool                 `json:"saving"`
	Testing      bool                 `json:"testing"`
	LastError    string               `json:"lastError"`
	StorageError string               `json:"storageError"`
	BlockedUntil int64                `json:"blockedUntil"`
	Now          int64                `json:"now"`
}

var (
	validRuleID = regexp.MustCompile(`^[a-zA-Z0-9-]{8,64}$`)
	validUUID   = regexp.MustCompile(`^[a-zA-Z0-9-]{1,64}$`)
	validRegion = regexp.MustCompile(`^[a-z0-9-]{2,32}$`)
	validTime   = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	validLabel  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	intervals   = map[int]bool{1: true, 5: true, 10: true, 15: true, 30: true, 60: true}
)

func defaultConfig() config {
	return config{
		Version:    1,
		Cloudflare: cloudflareCredentials{Mode: "token"},
		Huawei:     huaweiCredentials{Region: "ap-southeast-1"},
		Rules:      []rule{},
	}
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("生成规则 ID 失败")
	}
	return hex.EncodeToString(bytes[:]), nil
}

func normalizeConfig(input saveInput, previous config) (config, error) {
	if len(input.Rules) > 100 {
		return config{}, errors.New("最多支持 100 条 DDNS 规则")
	}
	next := defaultConfig()
	next.Rules = make([]rule, 0, len(input.Rules))
	previousRules := make(map[string]rule, len(previous.Rules))
	for _, old := range previous.Rules {
		previousRules[old.ID] = old
	}
	ids := make(map[string]bool)
	targets := make(map[string]bool)
	for _, raw := range input.Rules {
		old, hasOld := previousRules[raw.ID]
		value, err := normalizeRule(raw, old, hasOld)
		if err != nil {
			return config{}, err
		}
		key := value.Provider + "|" + value.Domain + "|" + value.Type
		if value.Provider == "huaweicloud" {
			key += "|" + value.Line
		}
		if ids[value.ID] {
			return config{}, errors.New("规则 ID 重复")
		}
		if targets[key] {
			return config{}, errors.New("同一服务商的同一域名和记录类型请使用一条规则")
		}
		ids[value.ID], targets[key] = true, true
		next.Rules = append(next.Rules, value)
	}
	credentials := input.Credentials
	mode := strings.TrimSpace(credentials.CloudflareMode)
	if mode == "" {
		mode = previous.Cloudflare.Mode
	}
	if mode == "" {
		mode = "token"
	}
	if mode != "token" && mode != "global" {
		return config{}, errors.New("Cloudflare 凭据类型无效")
	}
	next.Cloudflare.Mode = mode
	if mode == "token" {
		next.Cloudflare.Token = chooseSecret(credentials.CloudflareToken, previous.Cloudflare.Token, previous.Cloudflare.Mode == "token" || previous.Cloudflare.Mode == "")
	} else {
		email := strings.TrimSpace(credentials.CloudflareEmail)
		key := strings.TrimSpace(credentials.CloudflareKey)
		next.Cloudflare.Email = chooseSecret(email, previous.Cloudflare.Email, previous.Cloudflare.Mode == "global" || previous.Cloudflare.Mode == "")
		next.Cloudflare.Key = chooseSecret(key, previous.Cloudflare.Key, previous.Cloudflare.Mode == "global" || previous.Cloudflare.Mode == "")
		if previous.Cloudflare.Email != "" && next.Cloudflare.Email != previous.Cloudflare.Email && key == "" {
			return config{}, errors.New("更换 Cloudflare 邮箱时必须同时填写对应的 Global API Key")
		}
	}
	next.Huawei.AccessKey = chooseSecret(credentials.HuaweiAccessKey, previous.Huawei.AccessKey, true)
	next.Huawei.SecretKey = chooseSecret(credentials.HuaweiSecretKey, previous.Huawei.SecretKey, true)
	next.Huawei.Region = strings.ToLower(strings.TrimSpace(credentials.HuaweiRegion))
	if next.Huawei.Region == "" {
		next.Huawei.Region = previous.Huawei.Region
	}
	if next.Huawei.Region == "" {
		next.Huawei.Region = "ap-southeast-1"
	}
	if !validRegion.MatchString(next.Huawei.Region) {
		return config{}, errors.New("华为云国际站区域代码格式无效")
	}
	if credentials.ClearCloudflare {
		next.Cloudflare.Token, next.Cloudflare.Email, next.Cloudflare.Key = "", "", ""
	}
	if credentials.ClearHuawei {
		next.Huawei.AccessKey, next.Huawei.SecretKey = "", ""
	}
	return next, nil
}

func chooseSecret(submitted, previous string, keepPrevious bool) string {
	if value := strings.TrimSpace(submitted); value != "" {
		return value
	}
	if keepPrevious {
		return previous
	}
	return ""
}

func normalizeRule(raw, old rule, hasOld bool) (rule, error) {
	value := raw
	value.ID = strings.TrimSpace(value.ID)
	if value.ID == "" {
		id, err := newID()
		if err != nil {
			return rule{}, err
		}
		value.ID = id
	}
	if !validRuleID.MatchString(value.ID) {
		return rule{}, errors.New("规则 ID 无效")
	}
	if value.Provider != "cloudflare" && value.Provider != "huaweicloud" {
		return rule{}, errors.New("请选择 Cloudflare 或华为云国际站")
	}
	var err error
	value.Domain, err = normalizeDomain(value.Domain)
	if err != nil {
		return rule{}, err
	}
	if value.Type != "A" && value.Type != "AAAA" {
		return rule{}, errors.New("记录类型只能是 A 或 AAAA")
	}
	value.Source = strings.TrimSpace(value.Source)
	if value.Source == "" {
		value.Source = "nodes"
	}
	switch value.Source {
	case "nodes":
		value.ManualIP = ""
		if len(value.Servers) < 1 || len(value.Servers) > 20 {
			return rule{}, errors.New("每条规则请选择 1–20 个不同的 Komari 节点")
		}
	case "manual":
		value.Servers = nil
		address, err := normalizeManualAddress(value.ManualIP, value.Type)
		if err != nil {
			return rule{}, err
		}
		value.ManualIP = address
	default:
		return rule{}, errors.New("请选择 Komari 节点或手动指定 IP")
	}
	if value.Provider == "huaweicloud" {
		value.Line = strings.TrimSpace(value.Line)
		if value.Line == "" {
			value.Line = "default"
		}
		if len(value.Line) > 128 {
			return rule{}, errors.New("华为云解析线路无效")
		}
	} else {
		value.Line = ""
	}
	if !intervals[value.Interval] {
		return rule{}, errors.New("更新间隔只能是 1、5、10、15、30 或 60 分钟")
	}
	seen := make(map[string]bool, len(value.Servers))
	for _, uuid := range value.Servers {
		if !validUUID.MatchString(uuid) || seen[uuid] {
			return rule{}, errors.New("每条规则请选择 1–20 个不同的 Komari 节点")
		}
		seen[uuid] = true
	}
	if value.Schedule.Start == "" {
		value.Schedule.Start = "00:00"
	}
	if value.Schedule.End == "" {
		value.Schedule.End = "23:59"
	}
	if !validTime.MatchString(value.Schedule.Start) || !validTime.MatchString(value.Schedule.End) {
		return rule{}, errors.New("时段起止时间必须使用 HH:MM 格式")
	}
	if len(value.Schedule.Days) == 0 {
		value.Schedule.Days = []int{0, 1, 2, 3, 4, 5, 6}
	}
	daySet := make(map[int]bool, len(value.Schedule.Days))
	for _, day := range value.Schedule.Days {
		if day < 0 || day > 6 {
			return rule{}, errors.New("至少选择一个有效星期")
		}
		daySet[day] = true
	}
	value.Schedule.Days = value.Schedule.Days[:0]
	for day := range daySet {
		value.Schedule.Days = append(value.Schedule.Days, day)
	}
	sort.Ints(value.Schedule.Days)
	if len(value.Schedule.Days) == 0 {
		return rule{}, errors.New("至少选择一个有效星期")
	}
	if value.Schedule.UTCOffsetMinutes < -720 || value.Schedule.UTCOffsetMinutes > 840 || value.Schedule.UTCOffsetMinutes%15 != 0 {
		return rule{}, errors.New("时区偏移必须在 UTC-12:00 至 UTC+14:00 之间，且为 15 分钟的倍数")
	}
	if value.Provider != "cloudflare" {
		value.Proxied = false
	}
	if value.TTL == 0 {
		if value.Provider == "cloudflare" {
			value.TTL = 1
		} else {
			value.TTL = 300
		}
	}
	if value.Provider == "cloudflare" && value.TTL != 1 {
		value.TTL = clamp(value.TTL, 60, 86400)
	} else if value.Provider == "huaweicloud" {
		value.TTL = clamp(value.TTL, 60, 86400)
	}
	if hasOld && old.ID == value.ID && (old.Provider != value.Provider || old.Domain != value.Domain || old.Type != value.Type) {
		id, err := newID()
		if err != nil {
			return rule{}, err
		}
		value.ID = id
	}
	return value, nil
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func normalizeDomain(value string) (string, error) {
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	labels := strings.Split(name, ".")
	if len(name) > 253 || len(labels) < 2 {
		return "", errors.New("请输入完整域名，例如 home.example.com；中文域名请使用 Punycode。")
	}
	for index, label := range labels {
		if index == 0 && label == "*" {
			continue
		}
		if len(label) > 63 || !validLabel.MatchString(label) {
			return "", errors.New("请输入完整域名，例如 home.example.com；中文域名请使用 Punycode。")
		}
	}
	return name, nil
}

func validateCredentials(value config, provider string) (any, error) {
	if provider == "cloudflare" {
		if value.Cloudflare.Mode == "global" && value.Cloudflare.Email != "" && value.Cloudflare.Key != "" {
			return value.Cloudflare, nil
		}
		if value.Cloudflare.Token != "" {
			return value.Cloudflare, nil
		}
		return nil, errors.New("请先填写 Cloudflare API Token，或填写账户邮箱和 Global API Key")
	}
	if provider != "huaweicloud" {
		return nil, errors.New("未知 DNS 服务商")
	}
	if !validRegion.MatchString(value.Huawei.Region) {
		return nil, errors.New("华为云区域代码格式无效")
	}
	if value.Huawei.AccessKey == "" || value.Huawei.SecretKey == "" {
		return nil, errors.New("请先填写华为云国际站的 Access Key 和 Secret Key")
	}
	return value.Huawei, nil
}

func validAddress(node *clientInfo, recordType string) (string, error) {
	if node == nil {
		return "", errors.New("来源节点已删除或无法读取")
	}
	value := strings.TrimSpace(node.IPv4)
	if recordType == "AAAA" {
		value = strings.TrimSpace(node.IPv6)
	}
	ip := net.ParseIP(value)
	if (recordType == "A" && (ip == nil || ip.To4() == nil)) || (recordType == "AAAA" && (ip == nil || ip.To4() != nil)) {
		label := "IPv4"
		if recordType == "AAAA" {
			label = "IPv6"
		}
		return "", fmt.Errorf("来源节点尚未上报有效的 %s", label)
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
		return "", errors.New("来源节点的 IP 是未指定、回环、链路本地或组播地址")
	}
	if ipv4 := ip.To4(); ipv4 != nil && ipv4[0] >= 224 {
		return "", errors.New("来源节点的 IP 是未指定、回环、链路本地或组播地址")
	}
	return ip.String(), nil
}

func normalizeManualAddress(value, recordType string) (string, error) {
	value = strings.TrimSpace(value)
	ip := net.ParseIP(value)
	if (recordType == "A" && (ip == nil || ip.To4() == nil)) || (recordType == "AAAA" && (ip == nil || ip.To4() != nil)) {
		if recordType == "A" {
			return "", errors.New("请输入有效的 IPv4 地址")
		}
		return "", errors.New("请输入有效的 IPv6 地址")
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
		return "", errors.New("IP 不能是未指定、回环、链路本地或组播地址")
	}
	if ipv4 := ip.To4(); ipv4 != nil && ipv4[0] >= 224 {
		return "", errors.New("IP 不能是未指定、回环、链路本地或组播地址")
	}
	return ip.String(), nil
}

func inSchedule(value rule, timestamp int64) bool {
	if !value.Schedule.Enabled {
		return true
	}
	local := time.UnixMilli(timestamp).UTC().Add(time.Duration(value.Schedule.UTCOffsetMinutes) * time.Minute)
	day := int(local.Weekday())
	minute := local.Hour()*60 + local.Minute()
	start := clockMinute(value.Schedule.Start)
	end := clockMinute(value.Schedule.End)
	selected := make(map[int]bool, len(value.Schedule.Days))
	for _, day := range value.Schedule.Days {
		selected[day] = true
	}
	if start == end {
		return selected[day]
	}
	if start < end {
		return selected[day] && minute >= start && minute < end
	}
	if minute >= start {
		return selected[day]
	}
	return minute < end && selected[(day+6)%7]
}

func clockMinute(value string) int {
	if len(value) != 5 {
		return 0
	}
	return int(value[0]-'0')*600 + int(value[1]-'0')*60 + int(value[3]-'0')*10 + int(value[4]-'0')
}

func migrateLegacyConfig(data []byte) (config, bool, error) {
	var current config
	if err := json.Unmarshal(data, &current); err != nil {
		return config{}, false, fmt.Errorf("无法读取 DDNS 配置文件")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return config{}, false, fmt.Errorf("无法读取 DDNS 配置文件")
	}
	if _, ok := fields["cloudflare"]; ok {
		if current.Version == 0 {
			current.Version = 1
		}
		if current.Cloudflare.Mode == "" {
			if current.Cloudflare.Email != "" && current.Cloudflare.Key != "" {
				current.Cloudflare.Mode = "global"
			} else {
				current.Cloudflare.Mode = "token"
			}
		}
		if current.Huawei.Region == "" {
			current.Huawei.Region = "ap-southeast-1"
		}
		if current.Rules == nil {
			current.Rules = []rule{}
		}
		return current, false, nil
	}
	var legacy struct {
		Email string `json:"email"`
		Key   string `json:"key"`
		Rules []struct {
			ID       string   `json:"id"`
			Domain   string   `json:"domain"`
			Type     string   `json:"type"`
			Interval int      `json:"interval"`
			Servers  []string `json:"servers"`
			Enabled  *bool    `json:"enabled"`
			Proxied  bool     `json:"proxied"`
			Adopt    bool     `json:"adoptExisting"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return config{}, false, fmt.Errorf("无法读取 DDNS 配置文件")
	}
	if _, hasRules := fields["rules"]; !hasRules && legacy.Email == "" && legacy.Key == "" {
		return defaultConfig(), false, nil
	}
	migrated := defaultConfig()
	if legacy.Email != "" && legacy.Key != "" {
		migrated.Cloudflare = cloudflareCredentials{Mode: "global", Email: legacy.Email, Key: legacy.Key}
	}
	for _, old := range legacy.Rules {
		interval := old.Interval
		if !intervals[interval] {
			interval = 5
		}
		enabled := true
		if old.Enabled != nil {
			enabled = *old.Enabled
		}
		typ := old.Type
		if typ != "AAAA" {
			typ = "A"
		}
		id := old.ID
		if !validRuleID.MatchString(id) {
			generated, err := newID()
			if err != nil {
				return config{}, false, err
			}
			id = generated
		}
		migrated.Rules = append(migrated.Rules, rule{
			ID: id, Provider: "cloudflare", Domain: strings.ToLower(strings.TrimSuffix(old.Domain, ".")),
			Type: typ, Interval: interval, Servers: old.Servers, Enabled: enabled, Proxied: old.Proxied,
			AdoptExisting: old.Adopt, TTL: 1,
			Schedule: schedule{Start: "00:00", End: "23:59", Days: []int{0, 1, 2, 3, 4, 5, 6}, UTCOffsetMinutes: 480},
		})
	}
	return migrated, true, nil
}
