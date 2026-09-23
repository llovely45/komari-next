package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/komari-monitor/komari/pkg/pluginprocess"
)

type service struct {
	client *pluginprocess.Client

	mu           sync.RWMutex
	config       config
	history      map[string]runStatus
	running      bool
	saving       bool
	testing      bool
	lastError    string
	storageError string
	blockedUntil atomic.Int64
	cf           *cloudflareProvider
}

type huaweiLinesInput struct {
	Domain      string          `json:"domain"`
	Credentials saveCredentials `json:"credentials"`
}

func newService(client *pluginprocess.Client) *service {
	return &service{client: client, config: defaultConfig(), history: make(map[string]runStatus)}
}

func (s *service) load(ctx context.Context) error {
	entries, err := s.client.ListFiles(ctx, "")
	if err != nil {
		return fmt.Errorf("读取插件存储目录失败")
	}
	files := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if !entry.Directory {
			files[entry.Name] = true
		}
	}
	value := defaultConfig()
	if files["config.json"] {
		data, err := s.client.ReadFile(ctx, "config.json")
		if err != nil {
			return errors.New("无法读取 DDNS 数据文件 config.json")
		}
		migrated, changed, err := migrateLegacyConfig(data)
		if err != nil {
			return err
		}
		value = migrated
		if changed {
			if err := s.writeConfig(ctx, value); err != nil {
				return err
			}
			_ = s.client.Log(ctx, "已迁移旧版 Cloudflare DDNS 配置；原有记录归属标记和同步状态已保留。")
		}
	}
	if files["status.json"] {
		data, err := s.client.ReadFile(ctx, "status.json")
		if err != nil {
			return errors.New("无法读取 DDNS 数据文件 status.json")
		}
		var history map[string]runStatus
		if err := json.Unmarshal(data, &history); err != nil || history == nil {
			return errors.New("无法读取 DDNS 同步状态文件 status.json")
		}
		s.history = history
	}
	s.mu.Lock()
	s.config = value
	s.mu.Unlock()
	return nil
}

func (s *service) writeConfig(ctx context.Context, value config) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.New("无法编码 DDNS 配置")
	}
	if err := s.client.WriteFile(ctx, "config.json", data); err != nil {
		return errors.New("配置写入失败，请检查插件数据目录")
	}
	return nil
}

func (s *service) state(ctx context.Context) publicState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value := publicState{
		History:      cloneHistory(s.history),
		Running:      s.running,
		Saving:       s.saving,
		Testing:      s.testing,
		LastError:    s.redactLocked(s.lastError),
		StorageError: s.storageError,
		BlockedUntil: s.blockedUntil.Load(),
		Now:          time.Now().UnixMilli(),
	}
	value.Config.Cloudflare.Mode = s.config.Cloudflare.Mode
	if value.Config.Cloudflare.Mode == "" {
		value.Config.Cloudflare.Mode = "token"
	}
	value.Config.Cloudflare.HasToken = s.config.Cloudflare.Token != ""
	value.Config.Cloudflare.HasLegacyKey = s.config.Cloudflare.Email != "" && s.config.Cloudflare.Key != ""
	value.Config.Huawei.HasCredentials = s.config.Huawei.AccessKey != "" && s.config.Huawei.SecretKey != ""
	value.Config.Huawei.Region = s.config.Huawei.Region
	value.Config.Rules = append([]rule(nil), s.config.Rules...)
	for index := range value.Config.Rules {
		value.Config.Rules[index].Servers = append([]string(nil), s.config.Rules[index].Servers...)
		value.Config.Rules[index].Schedule.Days = append([]int(nil), s.config.Rules[index].Schedule.Days...)
	}
	return value
}

func cloneHistory(source map[string]runStatus) map[string]runStatus {
	cloned := make(map[string]runStatus, len(source))
	for id, status := range source {
		status.Results = append([]result(nil), status.Results...)
		for index := range status.Results {
			status.Results[index].IPs = append([]string(nil), status.Results[index].IPs...)
		}
		cloned[id] = status
	}
	return cloned
}

func (s *service) clients(ctx context.Context) ([]clientInfo, error) {
	var rows []clientInfo
	if err := s.client.CallKomariRPC(ctx, "admin:listDDNSClients", map[string]any{}, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *service) save(ctx context.Context, input saveInput) (publicState, error) {
	s.mu.Lock()
	if s.running || s.saving || s.testing {
		s.mu.Unlock()
		return publicState{}, errors.New("同步或凭据验证正在进行，请稍后保存")
	}
	s.saving = true
	before := s.config
	oldHistory := cloneHistory(s.history)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.saving = false
		s.mu.Unlock()
	}()
	next, err := normalizeConfig(input, before)
	if err != nil {
		return publicState{}, err
	}
	current, err := s.clients(ctx)
	if err != nil {
		return publicState{}, fmt.Errorf("无法读取 Komari 节点列表：%s", s.redact(err.Error()))
	}
	available := make(map[string]bool, len(current))
	for _, node := range current {
		available[node.UUID] = true
	}
	for _, value := range next.Rules {
		if value.Enabled {
			for _, uuid := range value.Servers {
				if !available[uuid] {
					return publicState{}, errors.New("规则包含已删除的 Komari 节点，请重新选择")
				}
			}
		}
	}
	retained := make(map[string]runStatus)
	oldRules := make(map[string]rule, len(before.Rules))
	for _, value := range before.Rules {
		oldRules[value.ID] = value
	}
	for _, value := range next.Rules {
		if old, ok := oldRules[value.ID]; ok && equalJSON(old, value) {
			if status, exists := oldHistory[value.ID]; exists {
				retained[value.ID] = status
			}
		}
	}
	if err := s.writeConfig(ctx, next); err != nil {
		return publicState{}, err
	}
	if err := s.writeHistory(ctx, retained); err != nil {
		s.mu.Lock()
		s.storageError = "运行状态保存失败，请检查插件数据目录空间和写入权限"
		s.mu.Unlock()
	} else {
		s.mu.Lock()
		s.storageError = ""
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.config = next
	s.history = retained
	s.cf = nil
	s.blockedUntil.Store(0)
	s.lastError = ""
	s.mu.Unlock()
	return s.state(ctx), nil
}

func equalJSON(first, second any) bool {
	a, errA := json.Marshal(first)
	b, errB := json.Marshal(second)
	return errA == nil && errB == nil && string(a) == string(b)
}

func (s *service) writeHistory(ctx context.Context, value map[string]runStatus) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.New("无法编码 DDNS 同步状态")
	}
	return s.client.WriteFile(ctx, "status.json", data)
}

func (s *service) persistHistory(ctx context.Context) {
	s.mu.RLock()
	value := cloneHistory(s.history)
	s.mu.RUnlock()
	if err := s.writeHistory(ctx, value); err != nil {
		s.mu.Lock()
		s.storageError = "运行状态保存失败，请检查插件数据目录空间和写入权限"
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	s.storageError = ""
	s.mu.Unlock()
}

func (s *service) test(ctx context.Context, input testInput) (map[string]string, error) {
	s.mu.Lock()
	if s.running || s.saving || s.testing {
		s.mu.Unlock()
		return nil, errors.New("已有同步或保存任务正在运行，请稍后重试")
	}
	s.testing = true
	stored := s.config
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.testing = false
		s.mu.Unlock()
	}()
	candidate := stored
	credentials := input.Credentials
	if input.Provider == "cloudflare" {
		mode := credentials.CloudflareMode
		if mode == "" {
			mode = candidate.Cloudflare.Mode
		}
		if mode == "" {
			mode = "token"
		}
		candidate.Cloudflare = cloudflareCredentials{Mode: mode}
		if mode == "token" {
			candidate.Cloudflare.Token = chooseSecret(credentials.CloudflareToken, stored.Cloudflare.Token, stored.Cloudflare.Mode == "token" || stored.Cloudflare.Mode == "")
		} else if mode == "global" {
			candidate.Cloudflare.Email = chooseSecret(credentials.CloudflareEmail, stored.Cloudflare.Email, stored.Cloudflare.Mode == "global" || stored.Cloudflare.Mode == "")
			candidate.Cloudflare.Key = chooseSecret(credentials.CloudflareKey, stored.Cloudflare.Key, stored.Cloudflare.Mode == "global" || stored.Cloudflare.Mode == "")
		}
	} else if input.Provider == "huaweicloud" {
		candidate.Huawei.AccessKey = chooseSecret(credentials.HuaweiAccessKey, stored.Huawei.AccessKey, true)
		candidate.Huawei.SecretKey = chooseSecret(credentials.HuaweiSecretKey, stored.Huawei.SecretKey, true)
		if strings.TrimSpace(credentials.HuaweiRegion) != "" {
			candidate.Huawei.Region = strings.ToLower(strings.TrimSpace(credentials.HuaweiRegion))
		}
	} else {
		return nil, errors.New("未知 DNS 服务商")
	}
	if _, err := validateCredentials(candidate, input.Provider); err != nil {
		return nil, err
	}
	if input.Provider == "cloudflare" {
		provider := newCloudflareProvider(s.client, candidate.Cloudflare, &s.blockedUntil)
		if err := provider.test(ctx); err != nil {
			return nil, err
		}
		return map[string]string{"message": "Cloudflare 凭据有效，并且可以读取 Zone。"}, nil
	}
	provider := newHuaweiProvider(s.client, candidate.Huawei)
	if err := provider.test(ctx); err != nil {
		return nil, err
	}
	return map[string]string{"message": "华为云国际站凭据有效，并且可以读取公网 DNS Zone。"}, nil
}

func (s *service) huaweiLines(ctx context.Context, input huaweiLinesInput) ([]huaweiLine, error) {
	domain, err := normalizeDomain(input.Domain)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.running || s.saving || s.testing {
		s.mu.Unlock()
		return nil, errors.New("已有同步或保存任务正在运行，请稍后重试")
	}
	s.testing = true
	stored := s.config
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.testing = false
		s.mu.Unlock()
	}()

	credentials := input.Credentials
	value := huaweiCredentials{
		AccessKey: chooseSecret(credentials.HuaweiAccessKey, stored.Huawei.AccessKey, true),
		SecretKey: chooseSecret(credentials.HuaweiSecretKey, stored.Huawei.SecretKey, true),
		Region:    stored.Huawei.Region,
	}
	if strings.TrimSpace(credentials.HuaweiRegion) != "" {
		value.Region = strings.ToLower(strings.TrimSpace(credentials.HuaweiRegion))
	}
	if value.Region == "" {
		value.Region = "ap-southeast-1"
	}
	if !validRegion.MatchString(value.Region) {
		return nil, errors.New("华为云区域代码格式无效")
	}
	if value.AccessKey == "" || value.SecretKey == "" {
		return nil, errors.New("请先填写华为云国际站的 Access Key 和 Secret Key")
	}
	provider := newHuaweiProvider(s.client, value)
	return provider.listLines(ctx, domain)
}

func (s *service) start(id string, force bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running || s.saving || s.testing {
		if force {
			return false, errors.New("已有 DDNS 任务正在运行，请稍后重试")
		}
		return false, nil
	}
	if id != "" {
		found := false
		for _, value := range s.config.Rules {
			if value.ID == id {
				found = true
				break
			}
		}
		if !found {
			return false, errors.New("DDNS 规则不存在")
		}
	}
	now := time.Now().UnixMilli()
	due := make([]rule, 0, len(s.config.Rules))
	for _, value := range s.config.Rules {
		if !value.Enabled || (id != "" && value.ID != id) {
			continue
		}
		if !force && !inSchedule(value, now) {
			continue
		}
		last, exists := s.history[value.ID]
		if force || !exists || last.NextAt == 0 || last.NextAt <= now+1000 {
			due = append(due, value)
		}
	}
	if len(due) == 0 {
		if force {
			return false, errors.New("该规则没有启用，或没有可执行的任务")
		}
		return false, nil
	}
	s.running = true
	go s.execute(due)
	return true, nil
}

func (s *service) scheduleLoop(ctx context.Context) {
	_, _ = s.start("", false)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		if ctx.Err() != nil {
			return
		}
		_, _ = s.start("", false)
	}
}

func (s *service) execute(rules []rule) {
	ctx := context.Background()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()
	nodes, err := s.clients(ctx)
	if err != nil {
		message := "无法读取 Komari 节点列表：" + s.redact(err.Error()) + "；下个周期会重试"
		s.mu.Lock()
		s.lastError = message
		for _, value := range rules {
			s.history[value.ID] = runStatus{Outcome: "error", Error: message, Results: []result{}, FinishedAt: time.Now().UnixMilli(), NextAt: time.Now().Add(time.Duration(value.Interval) * time.Minute).UnixMilli()}
		}
		s.mu.Unlock()
		s.persistHistory(ctx)
		return
	}
	current := make(map[string]*clientInfo, len(nodes))
	for index := range nodes {
		current[nodes[index].UUID] = &nodes[index]
	}
	for _, value := range rules {
		started := time.Now().UnixMilli()
		run := runStatus{StartedAt: started, Outcome: "running", Results: []result{}}
		s.mu.Lock()
		s.history[value.ID] = run
		s.mu.Unlock()
		targets := make([]target, 0, len(value.Servers))
		seen := make(map[string]bool)
		for _, uuid := range value.Servers {
			address, addressErr := validAddress(current[uuid], value.Type)
			if addressErr != nil {
				run.Results = append(run.Results, result{UUID: uuid, Action: "error", Message: s.redact(addressErr.Error())})
				continue
			}
			key := strings.ToLower(address)
			if seen[key] {
				run.Results = append(run.Results, result{UUID: uuid, Action: "error", Message: "多个来源节点上报了相同 IP，已跳过重复目标"})
				continue
			}
			seen[key] = true
			targets = append(targets, target{UUID: uuid, IP: address})
		}
		if value.Provider == "cloudflare" {
			provider := s.cloudflare()
			for _, item := range targets {
				action, syncErr := provider.sync(ctx, value, item.UUID, item.IP)
				if syncErr != nil {
					run.Results = append(run.Results, result{UUID: item.UUID, Action: "error", IP: item.IP, Message: s.redact(syncErr.Error())})
					if provider.isRateLimited(syncErr) {
						break
					}
				} else {
					run.Results = append(run.Results, result{UUID: item.UUID, Action: action, IP: item.IP})
				}
			}
		} else if len(targets) == len(value.Servers) && len(targets) > 0 {
			addresses := make([]string, 0, len(targets))
			for _, item := range targets {
				addresses = append(addresses, item.IP)
			}
			provider := newHuaweiProvider(s.client, s.configSnapshot().Huawei)
			action, ips, syncErr := provider.sync(ctx, value, addresses)
			if syncErr != nil {
				run.Results = append(run.Results, result{Action: "error", Message: s.redact(syncErr.Error())})
			} else {
				run.Results = append(run.Results, result{Action: action, IPs: ips})
			}
		}
		run.FinishedAt = time.Now().UnixMilli()
		run.NextAt = started + int64(value.Interval)*60_000
		run.Outcome = "success"
		for _, item := range run.Results {
			if item.Action == "error" {
				run.Outcome = "error"
				break
			}
		}
		s.mu.Lock()
		s.history[value.ID] = run
		s.mu.Unlock()
		s.persistHistory(ctx)
	}
	s.mu.Lock()
	s.lastError = ""
	s.mu.Unlock()
}

type target struct {
	UUID string
	IP   string
}

func (s *service) configSnapshot() config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

func (s *service) cloudflare() *cloudflareProvider {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cf == nil {
		s.cf = newCloudflareProvider(s.client, s.config.Cloudflare, &s.blockedUntil)
	}
	return s.cf
}

func (s *service) redact(value string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.redactLocked(value)
}

func (s *service) redactLocked(value string) string {
	secrets := []string{s.config.Cloudflare.Token, s.config.Cloudflare.Key, s.config.Huawei.AccessKey, s.config.Huawei.SecretKey}
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	if len(value) > 1024 {
		value = value[:1024]
	}
	return value
}

func (s *service) toPluginError(err error) *pluginprocess.PluginError {
	return pluginErr("ddns_error", s.redact(err.Error()))
}
