package main

import (
	"flag"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	connspy "github.com/j0hnsmith/connspy/http"
	"github.com/jarcoal/httpmock"

	"github.com/fatih/color"
	goldie "github.com/sebdah/goldie/v2"
	"github.com/stretchr/testify/require"
)

func init() {
	color.NoColor = false
}

func sniffHTTP() func() {
	realClient := http.DefaultClient
	http.DefaultClient = connspy.NewClient(nil, nil)
	return func() {
		http.DefaultClient = realClient
	}
}

var liveTest = flag.Bool("live", false, "enable live testing")

var acronisTestURL = url.URL{
	Scheme: "http",
	Host:   "dev-cloud.acronis.com",
}

const (
	acronisTestUser = "testuser"
	acronisTestPass = "hunter2"
)

var testAcronisAPI_Auth_testdata = map[string]interface{}{
	"access_token": "testAcronisValidToken",
	"id_token":     "testAcronisValidTokenInstanceID",
	"token_type":   "bearer",
}

// httpmockRegisterFiles walks a directory and sets up file responders
//
// generally it registeres items at path of (with whatever topdir):
// [topdir]/[method]/[domain]/uri
//
// as a specific example, topdir of testdata/mock/http it assumes paths like
// testdata/mock/http/GET/dev-cloud.acronis.com/api/2/clients/testuser
func httpmockRegisterFiles(t *testing.T, topdir string) {
	// ensure trailing / on topdir
	if !strings.HasSuffix(topdir, string(os.PathSeparator)) {
		topdir += string(os.PathSeparator)
	}

	// for each file in the given topdir path
	require.NoError(t, filepath.Walk(topdir,
		func(fpath string, info os.FileInfo, err error) error {
			// if it's not a file, skip it
			if info == nil || !info.Mode().IsRegular() {
				return nil
			}

			// read in the target file, should be no error possible
			fileBytes, err := ioutil.ReadFile(fpath)
			if err != nil {
				panic(err)
			}

			// the responder that we'll assign multiple times
			responder := func(req *http.Request) (*http.Response, error) {
				// check the auth token, if no match 401 reject
				reqToken := req.Header.Get("Authorization")
				reqToken = strings.TrimSpace(reqToken)            // trim spaces around both
				reqToken = strings.TrimPrefix(reqToken, "Bearer") // strip bearer
				reqToken = strings.TrimSpace(reqToken)            // remove any more spaces

				// if the auth token was there and matched, return the fileBytes
				if reqToken == testAcronisAPI_Auth_testdata["access_token"] {
					return httpmock.NewBytesResponse(http.StatusOK, fileBytes), nil
				}

				// otherwise 401 unauthorized
				return httpmock.NewJsonResponse(
					http.StatusUnauthorized,
					map[string]map[string]string{"error": {"message": "OK"}},
				)
			}

			// now to find the url and method
			// chop the topdir off the file
			relFPath := strings.TrimPrefix(fpath, topdir)
			filepathParts := strings.Split(relFPath, string(os.PathSeparator))
			method := filepathParts[0]             // first element
			url := path.Join(filepathParts[1:]...) // rest is the url

			// then, register that responder on every protocol imagined
			httpmock.RegisterResponder(method, url, responder)
			httpmock.RegisterResponder(method, "http://"+url, responder)
			httpmock.RegisterResponder(method, "https://"+url, responder)
			return nil
		},
	))
	return
}

var testAcronisAPI_ClientTenant_testdata = struct {
	Type     string `json:"type"`
	TenantID string `json:"tenant_id"`
	Data     struct {
		ClientName string `json:"client_name"`
		AgentType  string `json:"agent_type"`
		Hostname   string `json:"hostname"`
	} `json:"data"`
	Status string `json:"status"`
	Error  struct {
		Message string `json:"message"`
	} `json:"error"`
}{
	Type: "api_client",
	Data: struct {
		ClientName string `json:"client_name"`
		AgentType  string `json:"agent_type"`
		Hostname   string `json:"hostname"`
	}{ClientName: "Acronis-Exporter dev client"},
	TenantID: "c8e6259d-a4d7-4ffc-8614-79c1d143cc54", // random uuid
	Status:   "enabled",
}

func goldenAssert(t *testing.T, filename string, actual []byte) {
	goldie.New(t,
		goldie.WithFixtureDir("testdata/fixtures"),
		goldie.WithNameSuffix(".golden"),
		goldie.WithTestNameForDir(true),
		goldie.WithDiffEngine(goldie.ColoredDiff),
	).Assert(t, filename, actual)
}

// note, returns backwards of t.Failed()
func assertGoldenFile(t *testing.T, filename string) {
	fileBytes, err := ioutil.ReadFile(filename)
	require.NoError(t, err)
	goldenAssert(t, filepath.Base(filename), fileBytes)
}

func readerGoldenAssert(t *testing.T, name string, actual io.Reader) {
	fileBytes, err := ioutil.ReadAll(actual)
	require.NoError(t, err)
	goldenAssert(t, name, fileBytes)
}

var testReadTask_testdata = map[string]error{
	"testdata/mock/byTask/020c2794-e24c-4c78-af8f-f5f4f6cca110.json": nil,
	"testdata/mock/byTask/391cf484-f9a9-4491-b379-d18cec00fa55.json": nil,
	"testdata/mock/byTask/680d2028-0279-4642-90e8-ffa9fcc08a5c.json": nil,
	"testdata/mock/byTask/7130f8f5-192f-4017-b668-d0cad9b672a0.json": nil,
	"testdata/mock/byTask/7974c902-5165-41bd-8077-65b4ab90a62b.json": nil,
	"testdata/mock/byTask/bd78859e-531a-4173-ba84-3d6d5bd62fff.json": nil,
	"testdata/mock/byTask/eff6f78f-584e-46e7-9b78-9da3771cf2ba.json": nil,
	"testdata/mock/byTask/fdec0d76-e405-4cd9-b657-37a9cdf314c7.json": nil,
	"testdata/mock/byTask/missing.json":                              os.ErrNotExist,
}

var testWriteTask_testdata = map[string]struct {
	taskPath string
	cacheDir string
	expErr   string
}{
	"normal": {
		taskPath: "testdata/mock/byTask/020c2794-e24c-4c78-af8f-f5f4f6cca110.json",
		cacheDir: "testdata/cache/writeTask",
	},
	"badDest": {
		taskPath: "testdata/mock/byTask/020c2794-e24c-4c78-af8f-f5f4f6cca110.json",
		cacheDir: "testdata/missing",
		expErr:   "open testdata/missing/badDest.json: no such file or directory",
	},
}

var testFilterTaskUpdatesOnly_testdata = map[string]struct {
	taskPath []string
	cacheDir string
	lastTs   time.Time
	expErr   string
}{
	"normal": {
		taskPath: []string{
			"testdata/mock/byTask/020c2794-e24c-4c78-af8f-f5f4f6cca110.json",
		},
		cacheDir: "testdata/cache/filterUpdates",
	},
	"olderFirst": {
		taskPath: []string{
			"testdata/mock/byTask/7130f8f5-192f-4017-b668-d0cad9b672a0.json",
			"testdata/mock/byTask/391cf484-f9a9-4491-b379-d18cec00fa55.json",
		},
		cacheDir: "testdata/cache/filterUpdates",
	},
	"newerFirst": {
		taskPath: []string{
			"testdata/mock/byTask/391cf484-f9a9-4491-b379-d18cec00fa55.json",
			"testdata/mock/byTask/7130f8f5-192f-4017-b668-d0cad9b672a0.json",
		},
		cacheDir: "testdata/cache/filterUpdates",
	},
	"badDest": {
		taskPath: []string{"testdata/mock/byTask/020c2794-e24c-4c78-af8f-f5f4f6cca110.json"},
		cacheDir: "testdata/missing",
		expErr:   "open testdata/missing/badDest.json: no such file or directory",
	},
}

var testTaskToRegistry_testdata = map[string]string{
	"first":  "testdata/mock/byTask/7130f8f5-192f-4017-b668-d0cad9b672a0.json",
	"second": "testdata/mock/byTask/391cf484-f9a9-4491-b379-d18cec00fa55.json",
}

var testProbeHandler_testdata = []string{
	"020c2794-e24c-4c78-af8f-f5f4f6cca110",
	"391cf484-f9a9-4491-b379-d18cec00fa55",
	"680d2028-0279-4642-90e8-ffa9fcc08a5c",
	"7130f8f5-192f-4017-b668-d0cad9b672a0",
	"7974c902-5165-41bd-8077-65b4ab90a62b",
	"bd78859e-531a-4173-ba84-3d6d5bd62fff",
	"eff6f78f-584e-46e7-9b78-9da3771cf2ba",
	"fdec0d76-e405-4cd9-b657-37a9cdf314c7",
	"missing",
}

var testTenantIDToUUID_testdata = map[string]struct {
	id   string
	uuid string
}{
	"C3R2PB": {id: "1272636", uuid: "1ca2ea47-e6f1-48af-9328-41757c298d03"},
	"RZU0ND": {id: "1272639", uuid: "e8846c9a-41db-4534-bcbb-29b21a5eb34d"},
}
