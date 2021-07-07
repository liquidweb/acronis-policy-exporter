package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLive_AcronisAPI_walkTasks(t *testing.T) {
	api := acronisLiveConn(t)

	uuidCfg, err := cacheByUuid("testdata/cache/byTask")
	require.NoError(t, err)
	policyCfg, err := cacheByPolicy("testdata/cache/byPolicy")
	require.NoError(t, err)
	tenantCfg, err := cacheByTenantName("testdata/cache/byTenant")
	require.NoError(t, err)

	cachePipeline := multiTaskPipelineFunc(
		filterUpdatesOnly(uuidCfg, writeTaskPipeline(uuidCfg)),
		filterUpdatesOnly(policyCfg, writeTaskPipeline(policyCfg)),
		filterUpdatesOnly(policyCfg, writeTaskPipeline(tenantCfg)),
	)

	query := url.Values{}
	query.Set("order", "asc(updatedAt)")
	query.Set("updatedAt", "gt("+time.Now().Add(-48*time.Hour).Format(time.RFC3339)+")")
	query.Set("state", "completed")
	err = api.walkTasks(query, 5000, cachePipeline)
	assert.NoError(t, err)
}

func TestReadTask(t *testing.T) {
	for path, expErr := range testReadTask_testdata {
		uuid := strings.TrimSuffix(filepath.Base(path), ".json")
		t.Run(uuid, func(t *testing.T) {
			path, uuid, expErr := path, uuid, expErr
			t.Parallel()

			task, err := readTask(path)
			if expErr != nil {
				if os.IsNotExist(expErr) {
					assert.True(t, os.IsNotExist(err))
				} else {
					assert.EqualError(t, err, expErr.Error())
				}
				assert.Equal(t, task, Task{})
			} else {
				require.NoError(t, err)
				assert.Equal(t, uuid, task.UUID)
			}
		})
	}
}

func TestWriteTask(t *testing.T) {
	require.NoError(t, os.MkdirAll("testdata/cache/writeTask", 0755))
	for name, td := range testWriteTask_testdata {
		t.Run(name, func(t *testing.T) {
			name, td := name, td
			t.Parallel()

			cfg := cacheConfig{
				cacheDir:     td.cacheDir,
				targetToPath: stdTargetToCachePathFunc(td.cacheDir),
				taskToTarget: func(task Task) tgtStr { return tgtStr(name) },
			}
			writePipeline := writeTaskPipeline(cfg)

			task, err := readTask(td.taskPath)
			require.NoError(t, err)

			err = writePipeline(task)
			if td.expErr == "" {
				assert.NoError(t, err)
				assertGoldenFile(t, cfg.taskPath(task))
			} else {
				assert.EqualError(t, err, td.expErr)
			}
		})
	}
}

func TestFilterUpdatesOnly(t *testing.T) {
	require.NoError(t, os.MkdirAll("testdata/cache/filterUpdates/", 0755))
	for name, td := range testFilterTaskUpdatesOnly_testdata {
		t.Run(name, func(t *testing.T) {
			name, td := name, td
			t.Parallel()
			var writeErr error

			cfg := cacheConfig{
				cacheDir:     td.cacheDir,
				targetToPath: stdTargetToCachePathFunc(td.cacheDir),
				taskToTarget: func(task Task) tgtStr { return tgtStr(name) },
			}
			writePipeline := multiTaskPipelineFunc(filterUpdatesOnly(cfg,
				writeTaskPipeline(cfg)))

			var task Task
			for _, taskPath := range td.taskPath {
				task, err := readTask(taskPath)
				require.NoError(t, err)

				writeErr = writePipeline(task)
				if writeErr != nil {
					break
				}
			}

			if td.expErr == "" {
				assert.NoError(t, writeErr)
				assertGoldenFile(t, cfg.taskPath(task))
			} else {
				assert.EqualError(t, writeErr, td.expErr)
			}
		})
	}
}
