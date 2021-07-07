package main

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/fatih/color"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// acronisLiveConn returns a live acronis test connection, actually working
func acronisLiveConn(t *testing.T) AcronisAPI {
	if !*liveTest {
		t.Skip()
	}
	api := AcronisAPI{base: url.URL{
		Scheme: "https",
		Host:   "us5-cloud.acronis.com",
		Path:   "/",
	}}
	clientID, clientSecret := os.Getenv("ACRONIS_CLIENT_ID"), os.Getenv("ACRONIS_CLIENT_SECRET")
	require.NoError(t, api.Auth(context.Background(), clientID, clientSecret))
	return api
}

// acronisMockConn returns a mock acronis test connection, with auth responder setup
func acronisMockConn(t *testing.T) AcronisAPI {
	// set up a responder for an auth request
	httpmock.RegisterResponder(
		http.MethodPost,
		acronisTestURL.ResolveReference(&url.URL{Path: "./api/2/idp/token"}).String(),
		func(req *http.Request) (*http.Response, error) {

			user, pass, ok := req.BasicAuth()
			if !ok || user != acronisTestUser || pass != acronisTestPass {
				return httpmock.NewJsonResponse(http.StatusBadRequest, map[string]string{
					"error":             "invalid_request",
					"error_description": "Authorization header value is not recognized",
				})
			}

			if req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				return httpmock.NewJsonResponse(http.StatusBadRequest, map[string]string{
					"error":             "invalid_request",
					"error_description": "Wrong request body format or required argument is missing",
				})
			}

			authResp := testAcronisAPI_Auth_testdata
			testAcronisAPI_Auth_testdata["expires_on"] = time.Now().Add(time.Minute).Unix()

			return httpmock.NewJsonResponse(http.StatusOK, authResp)
		},
	)

	httpmockRegisterFiles(t, "testdata/mock/http")

	*apiTimeout = time.Second * 3

	// return a preauthed test api config that will hit this
	api := AcronisAPI{base: acronisTestURL}
	require.NoError(t, api.Auth(context.Background(), acronisTestUser, acronisTestPass))
	return api
}

func TestLive_AcronisAPI_Auth(t *testing.T) {
	f := sniffHTTP()
	defer f()
	api := acronisLiveConn(t)
	color.Yellow(spew.Sdump(api))
}

func TestAcronisAPI_Auth(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	_ = acronisMockConn(t)
	api := AcronisAPI{base: acronisTestURL}

	assert.EqualError(t, api.Auth(context.Background(), "baduser", acronisTestPass),
		"auth rejected: status [400] [invalid_request] message: Authorization header value is not recognized")
	assert.Equal(t, "", api.token)

	assert.NoError(t, api.Auth(context.Background(), acronisTestUser, acronisTestPass))
	assert.Equal(t, testAcronisAPI_Auth_testdata["access_token"], api.token)

	assert.EqualError(t, api.Auth(context.Background(), "baduser", acronisTestPass),
		"auth rejected: status [400] [invalid_request] message: Authorization header value is not recognized")
	assert.Equal(t, testAcronisAPI_Auth_testdata["access_token"], api.token)
}

func TestTenantIDToUUID(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	api := acronisMockConn(t)

	for lpuid, td := range testTenantIDToUUID_testdata {
		t.Run(lpuid, func(t *testing.T) {
			uuid, err := api.TenantIDToUUID(context.Background(), td.id)
			assert.NoError(t, err)

			assert.Equal(t, td.uuid, uuid)
		})
	}
}
