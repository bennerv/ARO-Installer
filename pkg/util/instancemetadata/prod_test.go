package instancemetadata

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/pborman/uuid"

	"github.com/openshift/ARO-Installer/pkg/util/azureclient"
)

func TestPopulateInstanceMetadata(t *testing.T) {
	for _, tt := range []struct {
		name               string
		do                 func(*http.Request) (*http.Response, error)
		wantSubscriptionID string
		wantLocation       string
		wantResourceGroup  string
		wantEnvironment    *azureclient.AROEnvironment
		wantErr            string
	}{
		{
			name: "valid (Public Cloud)",
			do: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{"application/json; charset=utf-8"},
					},
					Body: io.NopCloser(strings.NewReader(
						`{
							"subscriptionId": "rpSubscriptionId",
							"location": "eastus",
							"resourceGroupName": "rpResourceGroup",
							"azEnvironment": "AzurePublicCloud"
						}`,
					)),
				}, nil
			},
			wantSubscriptionID: "rpSubscriptionId",
			wantLocation:       "eastus",
			wantResourceGroup:  "rpResourceGroup",
			wantEnvironment:    &azureclient.PublicCloud,
		},
		{
			name: "valid (US Government Cloud)",
			do: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{"application/json; charset=utf-8"},
					},
					Body: io.NopCloser(strings.NewReader(
						`{
							"subscriptionId": "rpSubscriptionId",
							"location": "eastus",
							"resourceGroupName": "rpResourceGroup",
							"azEnvironment": "AzureUSGovernmentCloud"
						}`,
					)),
				}, nil
			},
			wantSubscriptionID: "rpSubscriptionId",
			wantLocation:       "eastus",
			wantResourceGroup:  "rpResourceGroup",
			wantEnvironment:    &azureclient.USGovernmentCloud,
		},
		{
			name: "invalid JSON",
			do: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{"application/json"},
					},
					Body: io.NopCloser(strings.NewReader("not JSON")),
				}, nil
			},
			wantErr: "invalid character 'o' in literal null (expecting 'u')",
		},
		{
			name: "invalid - error",
			do: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("fake error")
			},
			wantErr: "fake error",
		},
		{
			name: "invalid - status code",
			do: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Body:       io.NopCloser(nil),
				}, nil
			},
			wantErr: "unexpected status code 502",
		},
		{
			name: "invalid - content type",
			do: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{"text/plain"},
					},
					Body: io.NopCloser(nil),
				}, nil
			},
			wantErr: `unexpected content type "text/plain"`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := &prod{
				do: func(req *http.Request) (*http.Response, error) {
					if req.Method != http.MethodGet {
						return nil, fmt.Errorf("unexpected method %q", req.Method)
					}
					if req.URL.String() != "http://169.254.169.254/metadata/instance/compute?api-version=2019-03-11" {
						return nil, fmt.Errorf("unexpected URL %q", req.URL.String())
					}
					if req.Header.Get("Metadata") != "true" {
						return nil, fmt.Errorf("unexpected metadata header %q", req.Header.Get("Metadata"))
					}
					return tt.do(req)
				},
			}

			err := p.populateInstanceMetadata()

			if err != nil && err.Error() != tt.wantErr ||
				err == nil && tt.wantErr != "" {
				t.Fatal(err)
			}

			if p.subscriptionID != tt.wantSubscriptionID {
				t.Error(p.subscriptionID)
			}

			if p.location != tt.wantLocation {
				t.Error(p.location)
			}

			if p.resourceGroup != tt.wantResourceGroup {
				t.Error(p.resourceGroup)
			}

			if !reflect.DeepEqual(p.environment, tt.wantEnvironment) {
				t.Error(p.environment)
			}
		})
	}
}

func TestPopulateTenantIDFromMSI(t *testing.T) {
	ctx := context.Background()

	for _, tt := range []struct {
		name         string
		mockToken    string
		mockClientId string
		wantTenantID string
		wantErr      string
	}{
		{
			name:         "valid",
			mockToken:    base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"tid":"rpTenantID"}`)) + ".",
			mockClientId: uuid.NewUUID().String(),
			wantTenantID: "rpTenantID",
		},
		{
			name:         "oauthtoken invalid",
			mockToken:    "invalid",
			mockClientId: uuid.NewUUID().String(),
			wantErr:      "token contains an invalid number of segments",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			controller := gomock.NewController(t)
			defer controller.Finish()

			p := &prod{
				instanceMetadata: instanceMetadata{
					environment: &azureclient.PublicCloud,
				},
				do: func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header: http.Header{
							"Content-Type": []string{"application/json; charset=utf-8"},
						},
						Body: io.NopCloser(strings.NewReader(
							`{
								"subscriptionId": "rpSubscriptionId",
								"location": "eastus",
								"resourceGroupName": "rpResourceGroup",
								"azEnvironment": "AzureUSGovernmentCloud"
							}`,
						)),
					}, nil
				},
			}

			err := p.populateInstanceMetadata()
			if err != nil {
				t.Fatal(fmt.Errorf("Unexopected error populating instancemeta"))
			}

			p.do = func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{"application/json; charset=utf-8"},
					},
					Body: io.NopCloser(strings.NewReader(
						`{
							"access_token": "` + tt.mockToken + `",
							"client_id": "` + tt.mockClientId + `"
						}`,
					)),
				}, nil
			}

			err = p.populateTenantAndClientIDFromMSI(ctx)

			if err != nil && err.Error() != tt.wantErr ||
				err == nil && tt.wantErr != "" {
				t.Fatal(err)
			}

			if p.tenantID != tt.wantTenantID {
				t.Error(p.tenantID)
			}
		})
	}
}
