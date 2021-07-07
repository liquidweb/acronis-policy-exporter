package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alecthomas/kingpin"
)

// flags
var (
	cid = kingpin.Flag("cid", "acronis API client id").
		Envar("ACRONIS_CLIENT_ID").String()
	secret = kingpin.Flag("secret", "acronis API client secret").
		Envar("ACRONIS_CLIENT_SECRET").String()
	apiTimeout = kingpin.Flag("authTimeout", "timeout for auth requests").
			Default("5m").Duration()

	acronisURL = kingpin.Flag("acronisURL",
		`url of acronis endpoint given when creating a client`,
	).Envar("ACRONIS_CLIENT_URL").Default("https://dev-cloud.acronis.com/").URL()
)

func NewAPI(quit context.Context, id, secret string, timeout time.Duration, url url.URL) (*AcronisAPI, error) {
	api := AcronisAPI{
		base:    url,
		timeout: timeout,
	}

	err := api.Auth(timeoutNoCancel(quit, api.timeout), id, secret)
	if err != nil {
		return nil, err
	}

	if err = api.clientTenant(timeoutNoCancel(quit, api.timeout)); err != nil {
		return nil, err
	}

	api.autoRefresh(quit, id, secret)
	return &api, nil
}

type AcronisAPI struct { //nolint
	timeout    time.Duration
	base       url.URL
	token      string
	idToken    string
	clientID   string
	rootTenant string
	expires    int64
}

func (a *AcronisAPI) Call(
	ctx context.Context,
	method, uri string,
	headers http.Header,
	queryParams url.Values,
	bodyParams url.Values,
	returnObj interface{},
) (int, error) {

	reqURL := a.base.ResolveReference(&url.URL{Path: uri})
	reqURL.RawQuery = queryParams.Encode()

	if headers == nil {
		headers = http.Header{}
	}

	if _, isSet := headers["Authorization"]; !isSet {
		headers.Set("Authorization", "Bearer "+a.token)
	}

	req := &http.Request{
		Method: method,
		URL:    reqURL,
		Body:   nil,
		Header: headers,
	}

	if bodyParams != nil {
		req.Body = ioutil.NopCloser(strings.NewReader(bodyParams.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	req = req.WithContext(ctx)

	client := http.DefaultClient
	if doneTime, ok := ctx.Deadline(); ok {
		client.Timeout = time.Until(doneTime)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("problem running request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		respBody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return resp.StatusCode, fmt.Errorf("problem reading error body: %w", err)
		}
		return resp.StatusCode, fmt.Errorf("error: %s", string(respBody))
	}
	err = json.NewDecoder(resp.Body).Decode(&returnObj)
	return resp.StatusCode, err
}

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

// Auth gets or refreshes the Auth token for the Acronis API
// NOTE YOU HAVE TO CREATE AN ACRONIS API CLIENT under your user.
// TODO: TOTP not implemented at this time.
func (a *AcronisAPI) Auth(ctx context.Context, clientID, clientSecret string) error {
	var resp struct {
		Token            string `json:"access_token"`
		TokenType        string `json:"token_type"`
		Expires          int64  `json:"expires_on"`
		ID               string `json:"id_token"`
		RefreshToken     string `json:"refresh_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}

	uri := "./api/2/idp/token"

	query := url.Values{
		"grant_type": []string{"client_credentials"},
	}

	header := make(http.Header)
	header.Set("Authorization", "Basic "+basicAuth(clientID, clientSecret))

	status, err := a.Call(ctx, http.MethodPost, uri, header, nil, query, &resp)
	if err != nil {
		return fmt.Errorf("problem running request: %w", err)
	}

	if status < 200 || status >= 300 {
		return fmt.Errorf("auth rejected: status [%d] [%s] message: %s",
			status, resp.Error, resp.ErrorDescription)
	}

	a.clientID = clientID
	a.token = resp.Token
	a.expires = resp.Expires
	a.idToken = resp.ID

	return a.clientTenant(ctx)
}

func (a *AcronisAPI) autoRefresh(done context.Context, id, secret string) {
	go func() {
		for {
			expires := time.Unix(a.expires, 0) // acronis expire time is epoch seconds
			ticker := time.NewTicker(time.Until(expires) - a.timeout)
			select {
			case <-done.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				err := a.Auth(timeoutNoCancel(done, a.timeout), id, secret)
				if err != nil {
					log.Fatalln(err)
				}
			}
		}
	}()
}

// clientTenant retrieves and sets the tenant UUID of the current authed user.
// It is normally run during the auth process.
func (a *AcronisAPI) clientTenant(ctx context.Context) error {
	var respData struct {
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
	}

	statusCode, err := a.Call(ctx, http.MethodGet, "./api/2/clients/"+a.clientID,
		nil, nil, nil, &respData)
	if err != nil {
		return err
	}
	if statusCode != http.StatusOK {
		return fmt.Errorf("error status %d : %s", statusCode, respData.Error.Message)
	}
	a.rootTenant = respData.TenantID
	return nil
}

// TenantIDTUUID takes a v1 ID and translates it to a v2 uuid.
//
// See: GET /groups/{group}
// https://dl.acronis.com/u/raml-console/1.0/?raml=https://us5-cloud.acronis.com/api/1/raml/api_ssi.raml&withCredentials=true
func (a *AcronisAPI) TenantIDToUUID(ctx context.Context, v1ID string) (string, error) {
	var respData struct {
		UUID  string `json:"uuid"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	statusCode, err := a.Call(ctx, http.MethodGet, "./api/1/groups/"+v1ID,
		nil, nil, nil, &respData)
	if err != nil {
		return "", err
	}
	if statusCode != http.StatusOK {
		return "", fmt.Errorf("error status %d : %s", statusCode, respData.Error.Message)
	}
	return respData.UUID, nil
}

type Tenant struct {
	ParentID  string   `json:"parent_id"`
	LastName  string   `json:"last_name"`
	Login     string   `json:"login"`
	FirstName string   `json:"first_name"`
	Path      []string `json:"path"`
	ObjType   string   `json:"obj_type"`
	UUID      string   `json:"id"`
}

func (a *AcronisAPI) TenantSearch(searchTerm string) ([]Tenant, error) {
	var respData struct {
		Tenants []Tenant `json:"items"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	reqQuery := url.Values{}
	reqQuery.Add("tenant", a.rootTenant)
	reqQuery.Add("text", searchTerm)

	reqURL := a.base.ResolveReference(&url.URL{Path: "api/2/search"})
	reqURL.RawQuery = reqQuery.Encode()

	header := http.Header{}
	header.Set("Authorization", "Bearer "+a.token)

	req := &http.Request{
		Method: http.MethodGet,
		URL:    reqURL,
		Body:   nil,
		Header: header,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("problem running request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		respBody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return []Tenant{}, fmt.Errorf("problem reading error body: %w", err)
		}
		return []Tenant{}, fmt.Errorf("error: %s", string(respBody))
	}
	err = json.NewDecoder(resp.Body).Decode(&respData)
	if resp.StatusCode != http.StatusOK {
		return []Tenant{}, fmt.Errorf("error status %d : %s", resp.StatusCode, respData.Error.Message)
	}
	return respData.Tenants, err
}

// TODO: unneeded junk below

func (a *AcronisAPI) TenantInfras(uuid string) ([]string, error) {
	var respData struct {
		Infras []string `json:"infras"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
	defer cancel()
	statusCode, err := a.Call(ctx, http.MethodGet, "./api/2/tenants/"+uuid+"/infra",
		nil, nil, nil, &respData)
	if err != nil {
		return []string{}, err
	}
	if statusCode != http.StatusOK {
		return []string{}, fmt.Errorf("error status %d : %s", statusCode, respData.Error.Message)
	}
	return respData.Infras, nil
}

func (a *AcronisAPI) UserDetails(uuid string) (string, error) {
	var respData struct {
		PersonalTenantID string `json:"personal_tenant_id"`
		Error            struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	reqQuery := url.Values{}
	// reqQuery.Add("parent_id", parent)

	reqURL := a.base.ResolveReference(&url.URL{Path: "api/2/users/" + uuid})
	reqURL.RawQuery = reqQuery.Encode()

	header := http.Header{}
	header.Set("Authorization", "Bearer "+a.token)

	req := &http.Request{
		Method: http.MethodGet,
		URL:    reqURL,
		Body:   nil,
		Header: header,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("problem running request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		respBody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("problem reading error body: %w", err)
		}
		return "", fmt.Errorf("error: %s", string(respBody))
	}
	err = json.NewDecoder(resp.Body).Decode(&respData)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("error status %d : %s", resp.StatusCode, respData.Error.Message)
	}
	return respData.PersonalTenantID, err
}

func (a *AcronisAPI) TenantUsages(uuid string) (interface{}, error) {
	var respData interface{}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
	defer cancel()
	statusCode, err := a.Call(ctx, http.MethodGet, "./api/2/tenants/usages",
		nil, url.Values{"tenants": []string{uuid}}, nil, &respData)
	_ = statusCode
	if err != nil {
		return []string{}, err
	}
	// if statusCode != http.StatusOK {
	// 	return []string{}, fmt.Errorf("error status %d : %s", statusCode, respData.Error.Message)
	// }
	return respData, nil
}

func (a *AcronisAPI) TenantInfo(uuid string) (interface{}, error) {
	type Contact struct {
		ID         string   `json:"id"`
		CreatedAt  string   `json:"created_at"`
		UpdatedAt  string   `json:"updated_at"`
		Types      []string `json:"types"`
		Firstname  string   `json:"firstname"`
		Lastname   string   `json:"lastname"`
		ExternalID string   `json:"external_id"`
	}
	var respData struct {
		ID              string        `json:"id"`
		Version         int64         `json:"version"`
		Name            string        `json:"name"`
		CustomerType    string        `json:"customer_type"`
		ParentID        string        `json:"parent_id"`
		Kind            string        `json:"kind"`
		Contact         Contact       `json:"contact"`
		Contacts        []interface{} `json:"contacts"`
		Enabled         bool          `json:"enabled"`
		CustomerID      string        `json:"customer_id"`
		BrandID         int64         `json:"brand_id"`
		BrandUUID       string        `json:"brand_uuid"`
		InternalTag     interface{}   `json:"internal_tag"`
		OwnerID         string        `json:"owner_id"`
		HasChildren     bool          `json:"has_children"`
		AncestralAccess bool          `json:"ancestral_access"`
		MfaStatus       string        `json:"mfa_status"`
		PricingMode     string        `json:"pricing_mode"`
		Error           struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
	defer cancel()
	statusCode, err := a.Call(ctx, http.MethodGet, "./api/2/tenants/"+uuid,
		nil, nil, nil, &respData)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("error status %d : %s", statusCode, respData.Error.Message)
	}
	return respData, nil
}

// Generated by https://quicktype.io

type Policy struct {
	TrusteeID   string `json:"trustee_id"`
	RoleID      string `json:"role_id"`
	TenantID    string `json:"tenant_id"`
	TrusteeType string `json:"trustee_type"`
	Version     int64  `json:"version"`
	IssuerID    string `json:"issuer_id"`
	ID          string `json:"id"`
}

func (a *AcronisAPI) UserAccessPolicies(uuid string) ([]Policy, error) {
	var respData struct {
		Policies []Policy `json:"items"`
		Error    struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
	defer cancel()
	statusCode, err := a.Call(ctx, http.MethodGet, "./api/2/users/"+uuid+"/access_policies",
		nil, nil, nil, &respData)
	if err != nil {
		return nil, err
	}
	_ = statusCode
	// if statusCode != http.StatusOK {
	// 	return []string{}, fmt.Errorf("error status %d : %s", statusCode, respData.Error.Message)
	// }
	return respData.Policies, nil
}

func (a *AcronisAPI) TenantChildren(uuid string) ([]string, error) {
	var respData struct {
		Children []string `json:"items"`
		Error    struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
	defer cancel()
	statusCode, err := a.Call(ctx, http.MethodGet, "./api/2/tenants/"+uuid+"/children",
		nil, nil, nil, &respData)
	if err != nil {
		return []string{}, err
	}
	if statusCode != http.StatusOK {
		return []string{}, fmt.Errorf("error status %d : %s", statusCode, respData.Error.Message)
	}
	return respData.Children, nil
}
