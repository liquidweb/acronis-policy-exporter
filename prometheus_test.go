package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"io/ioutil"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbeHandler(t *testing.T) {
	ts := httptest.NewServer(probeHandler(
		stdTargetToCachePathFunc("testdata/mock/byTask"),
	))
	tsURL, err := url.Parse(ts.URL)
	require.NoError(t, err)

	regex, err := regexp.CompilePOSIX(`^probe_duration_seconds.*`)
	require.NoError(t, err)

	for id, target := range testProbeHandler_testdata {
		t.Run(strconv.Itoa(id), func(t *testing.T) {
			name, target, tsURL := strconv.Itoa(id), target, tsURL
			tsURL.RawQuery = url.Values{"target": []string{target}}.Encode()

			resp, err := http.Get(tsURL.String())
			assert.NoError(t, err)
			defer resp.Body.Close()
			respBytes, err := ioutil.ReadAll(resp.Body)
			assert.NoError(t, err)
			respBytes = regex.ReplaceAll(respBytes, []byte("probe_duration_seconds *"))

			goldenAssert(t, name, respBytes)
		})
	}
}

func TestTaskToRegistry(t *testing.T) {
	tempdir, err := ioutil.TempDir(os.TempDir(), t.Name())
	require.NoError(t, err)
	for name, taskfile := range testTaskToRegistry_testdata {
		t.Run(name, func(t *testing.T) {
			name, taskfile := name, taskfile
			registry := prometheus.NewRegistry()
			filename := filepath.Join(tempdir, name+".prom")

			task, err := readTask(taskfile)
			require.NoError(t, err)

			taskToRegistry(registry, task)
			assert.NoError(t, prometheus.WriteToTextfile(filename, registry))

			assertGoldenFile(t, filename)
		})
	}
}
