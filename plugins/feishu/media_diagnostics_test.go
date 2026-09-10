package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

func TestMediaDiagnosticsKeepStagesWithoutRawErrors(t *testing.T) {
	for _, scenario := range []struct {
		name string
		err  error
		want string
	}{
		{"provider", mediaProviderError("download", 400, 99991672), "media_download_provider_99991672"},
		{"http", mediaStageError("transfer", runtimeStatusError(403)), "media_transfer_http_403"},
		{"transport", mediaStageError("read", errors.New("connection failed: private-url secret")), "media_read_failed"},
		{"size", mediaStageError("download", errMediaSize), "resource_size_invalid"},
		{"unknown", errors.New("private-url secret"), "resource_download_failed"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if got := mediaErrorCode(scenario.err); got != scenario.want {
				t.Fatalf("code=%s", got)
			}
		})
	}
	if !isAmbiguous(mediaStageError("send", errors.New("connection lost"))) {
		t.Fatal("ambiguous send classification lost")
	}
}

func TestMediaInboundFailureStageSurvivesSmallImage(t *testing.T) {
	for _, stage := range []string{"progress", "download", "transfer"} {
		t.Run(stage, func(t *testing.T) {
			runtimeServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if strings.HasSuffix(request.URL.Path, "/progress") && stage != "progress" {
					writer.Write([]byte("{\"ok\":true}"))
					return
				}
				writer.WriteHeader(403)
				writer.Write([]byte("private response must not be exposed"))
			}))
			defer runtimeServer.Close()
			client := lark.NewClient("diagnostic-in-"+stage, "test-secret", lark.WithHttpClient(mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
				if strings.Contains(request.URL.Path, "/auth/") {
					return mediaTestResponse(200, "{\"code\":0,\"tenant_access_token\":\"sdk-test\",\"expire\":3600}"), nil
				}
				if stage == "download" {
					return mediaTestResponse(400, "{\"code\":99991672,\"msg\":\"private response\"}"), nil
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/octet-stream"}}, Body: io.NopCloser(strings.NewReader("\x89PNG\r\n\x1a\nfixture"))}, nil
			}}))
			runtime := &runtimeClient{baseURL: runtimeServer.URL, http: runtimeServer.Client()}
			err := runtime.downloadResource(context.Background(), client, command{ID: "command", LeaseID: "lease", Payload: map[string]any{"message_id": "message", "resource_key": "resource", "resource_type": "image"}})
			want := "media_" + stage + "_http_403"
			if stage == "download" {
				want = "media_download_provider_99991672"
			}
			if err == nil || mediaErrorCode(err) != want {
				t.Fatalf("err=%v want=%s", err, want)
			}
		})
	}
}

func TestMediaOutboundNackRetainsFailureStage(t *testing.T) {
	for _, stage := range []string{"record", "read", "upload_image", "upload_file", "send"} {
		t.Run(stage, func(t *testing.T) {
			nackCode := ""
			runtimeServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if strings.HasSuffix(request.URL.Path, "/nack") {
					var payload struct {
						ErrorCode string `json:"error_code"`
					}
					if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					nackCode = payload.ErrorCode
				} else if strings.HasSuffix(request.URL.Path, "/media") {
					if stage == "record" {
						writer.WriteHeader(403)
						return
					}
				} else if strings.HasSuffix(request.URL.Path, "/attachment") {
					if stage == "read" {
						writer.WriteHeader(403)
						return
					}
					if stage == "upload_file" {
						writer.Write([]byte("report"))
					} else {
						writer.Write([]byte("\x89PNG\r\n\x1a\nfixture"))
					}
					return
				} else {
					t.Errorf("unexpected runtime endpoint %s", request.URL.Path)
				}
				writer.Write([]byte("{\"ok\":true}"))
			}))
			defer runtimeServer.Close()
			client := lark.NewClient("diagnostic-out-"+stage, "test-secret", lark.WithHttpClient(mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
				if strings.Contains(request.URL.Path, "/auth/") {
					return mediaTestResponse(200, "{\"code\":0,\"tenant_access_token\":\"sdk-test\",\"expire\":3600}"), nil
				}
				if strings.HasSuffix(request.URL.Path, "/images") && stage == "send" {
					return mediaTestResponse(200, "{\"code\":0,\"data\":{\"image_key\":\"resource\"}}"), nil
				}
				return mediaTestResponse(400, "{\"code\":99991672,\"msg\":\"private response\"}"), nil
			}}))
			runtime := &runtimeClient{baseURL: runtimeServer.URL, http: runtimeServer.Client()}
			runtime.deliver(context.Background(), client, delivery{ID: "delivery", LeaseID: "lease", Attempts: 1, IdempotencyKey: "key", Target: map[string]string{"chat_id": "chat"}, SemanticPayload: map[string]any{"kind": "attachment"}})
			want := "media_" + stage + "_provider_99991672"
			if stage == "record" || stage == "read" {
				want = "media_" + stage + "_http_403"
			}
			if nackCode != want {
				t.Fatalf("nack=%s want=%s", nackCode, want)
			}
		})
	}
}
