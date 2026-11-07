package app

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/LoliaFRP/loliafrp-fnos/backend/internal/api"
	"github.com/LoliaFRP/loliafrp-fnos/backend/internal/core"
	"github.com/LoliaFRP/loliafrp-fnos/backend/internal/fnos"
)

type CoreStatus struct {
	core.Info
	State      string `json:"state"`
	Progress   int    `json:"progress"`
	Downloaded int64  `json:"downloadedBytes"`
	Total      int64  `json:"totalBytes"`
	Error      string `json:"error,omitempty"`
}

type coreTask struct {
	uid    string
	cancel context.CancelFunc
}

type coreRelease struct {
	ID   int    `json:"id"`
	Slug string `json:"slug"`
}

const coreReleaseSlug = "lolia_frp_cli"

type coreVersion struct {
	ID         int    `json:"id"`
	Tag        string `json:"tag"`
	Prerelease bool   `json:"prerelease"`
}

func (s *Service) coreStatus() CoreStatus {
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	return s.coreState
}

// Only this fixed first-party catalog selection can bootstrap a missing core.
// A NAS user cannot supply software IDs, files, URLs, or replace an installed core.
func (s *Service) ensureCore(uid, token string, retry, update bool) CoreStatus {
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	if s.ctx.Err() != nil || s.coreTask != nil || (s.coreState.Installed && !update) || (s.coreState.State == "failed" && !retry) {
		return s.coreState
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
	task := &coreTask{uid: uid, cancel: cancel}
	s.coreTask = task
	s.coreState = CoreStatus{Info: s.coreState.Info, State: "checking"}
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		defer cancel()
		err := s.downloadRecommendedCore(ctx, token)
		info := s.core.Info()
		s.coreMu.Lock()
		if err != nil {
			s.coreState.Info = info
			s.coreState.State = "failed"
			s.coreState.Error = err.Error()
		} else {
			s.coreState.Info = info
			s.coreState.State = "ready"
			s.coreState.Progress = 100
			s.coreState.Error = ""
		}
		s.coreTask = nil
		s.coreMu.Unlock()
		if err == nil && !update && s.ctx.Err() == nil {
			s.restore()
		}
	}()
	return s.coreState
}

func (s *Service) setupCore(w http.ResponseWriter, r *http.Request, uid string) {
	var input struct{}
	if err := decode(w, r, &input); err != nil {
		problem(w, err)
		return
	}
	token, err := s.token(r.Context(), uid)
	if err != nil {
		problem(w, err)
		return
	}
	write(w, s.ensureCore(uid, token, true, false))
}

func (s *Service) updateCore(w http.ResponseWriter, r *http.Request, uid string) {
	if !fnos.Current(r.Context()).IsAdmin {
		http.Error(w, "administrator required", http.StatusForbidden)
		return
	}
	var input struct{}
	if err := decode(w, r, &input); err != nil {
		problem(w, err)
		return
	}
	token, err := s.token(r.Context(), uid)
	if err != nil {
		problem(w, err)
		return
	}
	write(w, s.ensureCore(uid, token, true, true))
}

func (s *Service) cancelCoreSetup(uid string) {
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	if task := s.coreTask; task != nil && task.uid == uid && task.cancel != nil {
		task.cancel()
	}
}

func isCoreRelease(r coreRelease) bool {
	return r.Slug == coreReleaseSlug
}

func (s *Service) recommendedRelease(ctx context.Context, token string) (int, error) {
	matches := []coreRelease{}
	count := 0
	for page := 1; page <= 1000; page++ {
		var data struct {
			Items []coreRelease `json:"items"`
			Total int           `json:"total"`
		}
		if err := s.api.Do(ctx, token, "GET", fmt.Sprintf("/releases?page=%d&limit=200", page), nil, &data); err != nil {
			return 0, err
		}
		for _, r := range data.Items {
			if r.ID > 0 && isCoreRelease(r) {
				matches = append(matches, r)
			}
		}
		count += len(data.Items)
		if len(data.Items) == 0 || count >= data.Total {
			if len(matches) == 0 {
				return 0, fmt.Errorf("软件列表中没有标识为 %s 的内核，请联系平台管理员检查内核发布配置", coreReleaseSlug)
			}
			if len(matches) != 1 {
				return 0, fmt.Errorf("软件列表中存在多个标识为 %s 的内核，请联系平台管理员确认唯一的内核软件", coreReleaseSlug)
			}
			return matches[0].ID, nil
		}
	}
	return 0, errors.New("内核软件分页超过限制")
}

func checksum(raw string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "sha256:")
}

func artifactRank(a asset) int {
	if a.ID < 1 || a.OS != "linux" || a.Arch != runtime.GOARCH || strings.Contains(strings.ToLower(a.Filename), "frps") {
		return -1
	}
	name := strings.ToLower(filepath.Base(a.Filename))
	rank := 0
	switch {
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		rank = 2
	case strings.HasSuffix(name, ".zip"):
		rank = 4
	case filepath.Ext(name) != "" && filepath.Ext(name) != ".bin":
		return -1
	}
	if strings.Contains(name, "frpc") {
		rank--
	}
	return rank + 1
}

func selectCoreAsset(items []asset) (asset, error) {
	candidates := []asset{}
	for _, a := range items {
		if artifactRank(a) >= 0 {
			candidates = append(candidates, a)
		}
	}
	if len(candidates) == 0 {
		return asset{}, fmt.Errorf("没有适用于 Linux %s 的内核文件", runtime.GOARCH)
	}
	sort.SliceStable(candidates, func(i, j int) bool { return artifactRank(candidates[i]) < artifactRank(candidates[j]) })
	if len(candidates) > 1 && artifactRank(candidates[0]) == artifactRank(candidates[1]) {
		return asset{}, errors.New("发现多个同等优先级的内核文件，请联系平台管理员检查发布配置")
	}
	a := candidates[0]
	expected, err := hex.DecodeString(checksum(a.Checksum))
	if err != nil || len(expected) != 32 {
		return asset{}, errors.New("内核发布文件缺少有效 SHA-256 校验和，请联系平台管理员补充")
	}
	return a, nil
}

func (s *Service) downloadRecommendedCore(ctx context.Context, token string) error {
	id, err := s.recommendedRelease(ctx, token)
	if err != nil {
		return err
	}
	data, items, err := s.getAssets(ctx, token, id)
	if err != nil {
		return err
	}
	version := data["version"].(coreVersion)
	if version.ID < 1 || strings.TrimSpace(version.Tag) == "" {
		return errors.New("平台返回的推荐内核版本无效")
	}
	if version.Prerelease {
		return errors.New("平台推荐内核为预发布版本，请联系平台管理员选择稳定版本")
	}
	a, err := selectCoreAsset(items)
	if err != nil {
		return err
	}
	sort.SliceStable(a.Sources, func(i, j int) bool { return a.Sources[i].SortOrder < a.Sources[j].SortOrder })
	lastError := errors.New("内核没有可用下载源，请联系平台管理员检查下载源配置")
	for _, source := range a.Sources {
		if !source.Enabled || !source.GlobalEnabled || source.Source == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		var d core.Download
		s.coreMu.Lock()
		s.coreState.State = "checking"
		s.coreState.Progress = 0
		s.coreState.Downloaded = 0
		s.coreState.Total = a.Size
		s.coreMu.Unlock()
		path := fmt.Sprintf("/releases/download?release_id=%d&asset_id=%d&source=%s", id, a.ID, url.QueryEscape(source.Source))
		if err = s.api.Do(ctx, token, "GET", path, nil, &d); err == nil {
			if d.Filename != a.Filename || checksum(d.Checksum) != checksum(a.Checksum) || d.Tag != version.Tag {
				err = errors.New("下载信息与推荐内核不一致，请联系平台管理员检查发布配置")
			} else {
				err = s.core.InstallWithProgress(ctx, d, func(p core.Progress) {
					s.coreMu.Lock()
					defer s.coreMu.Unlock()
					percent := 0
					if p.Total > 0 {
						percent = min(99, int(p.Downloaded*100/p.Total))
					}
					s.coreState.State = p.Stage
					s.coreState.Downloaded = p.Downloaded
					s.coreState.Total = p.Total
					s.coreState.Progress = percent
				})
			}
		}
		if err == nil {
			return nil
		}
		lastError = err
		var upstream *api.Error
		if errors.As(err, &upstream) && (upstream.Status == 401 || upstream.Status == 403) {
			return err
		}
	}
	return lastError
}
