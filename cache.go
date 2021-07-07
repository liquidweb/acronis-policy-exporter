package main

import (
	"net/url"
	"os"
	"path/filepath"
	"time"
)

func refreshCache(api *AcronisAPI, cache taskPipelineFunc, age time.Duration) error {
	query := url.Values{}
	query.Set("order", "asc(updatedAt)")
	query.Set("updatedAt", "gt("+time.Now().Add(-1*age).Format(time.RFC3339)+")")
	query.Set("state", "completed")
	err := api.walkTasks(query, 5000, cache)
	return err
}

func pruneCache(age time.Duration) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		if !info.ModTime().Add(age).After(time.Now()) {
			return nil
		}
		return os.Remove(path)
	}
}

type tgtStr string
type taskToTargetFunc func(Task) tgtStr
type targetToCachePathFunc func(tgtStr) string
type cacheConfig struct {
	cacheDir     string
	taskToTarget taskToTargetFunc
	targetToPath targetToCachePathFunc
}

func (cfg cacheConfig) taskPath(task Task) string {
	return cfg.targetToPath(cfg.taskToTarget(task))
}

func ensureCachePath(taskPath targetToCachePathFunc) error {
	return os.MkdirAll(filepath.Dir(taskPath("junk")), 0755)
}

func stdTargetToCachePathFunc(cacheDir string) targetToCachePathFunc {
	return func(tgt tgtStr) string {
		return filepath.Join(cacheDir, string(tgt)+".json")
	}
}

func stdCachePathFunc(cacheDir string, f taskToTargetFunc) (cacheConfig, error) {
	ret := cacheConfig{
		cacheDir:     cacheDir,
		taskToTarget: f,
		targetToPath: stdTargetToCachePathFunc(cacheDir),
	}
	return ret, ensureCachePath(ret.targetToPath)
}

func cacheByPolicy(cacheDir string) (cacheConfig, error) {
	return stdCachePathFunc(cacheDir, func(task Task) tgtStr { return tgtStr(task.Policy.ID) })
}
func cacheByUuid(cacheDir string) (cacheConfig, error) {
	return stdCachePathFunc(cacheDir, func(task Task) tgtStr { return tgtStr(task.UUID) })
}

func cacheByTenantName(cacheDir string) (cacheConfig, error) {
	return stdCachePathFunc(cacheDir, func(task Task) tgtStr {
		if task.Tenant.Name != "" {
			return tgtStr(task.Tenant.Name)
		}
		return tgtStr(task.Tenant.ID)
	})
}

// func TenantIDToUUIDGetter(ctx context.Context, v1id string, dest groupcache.Sink) error {
// 	TenantIDToUUIDGetter()

// }
