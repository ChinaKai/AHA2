package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	channeltypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const maxMediaFileBytes int64 = 25 << 20
const maxMediaImageBytes int64 = 10 * 1000 * 1000

var errMediaSize = errors.New("resource_size_invalid")
var errMediaType = errors.New("resource_type_unsupported")

type mediaFailure struct {
	code  string
	cause error
}

func (failure mediaFailure) Error() string { return failure.code }
func (failure mediaFailure) Unwrap() error { return failure.cause }

type runtimeStatusError int

func (status runtimeStatusError) Error() string { return fmt.Sprintf("runtime status %d", status) }

func mediaStageError(stage string, err error) error {
	if err == nil {
		return nil
	}
	var existing mediaFailure
	if errors.As(err, &existing) {
		return err
	}
	code := "media_" + stage + "_failed"
	var status runtimeStatusError
	if errors.As(err, &status) {
		code = fmt.Sprintf("media_%s_http_%d", stage, status)
	}
	return mediaFailure{code: code, cause: err}
}

func mediaProviderError(stage string, status, code int) error {
	if code > 0 && code <= 999999999 {
		return mediaFailure{code: fmt.Sprintf("media_%s_provider_%d", stage, code)}
	}
	if status < 200 || status >= 300 {
		return mediaStageError(stage, runtimeStatusError(status))
	}
	return mediaStageError(stage, errors.New("invalid media response"))
}

var inboundImageReference = regexp.MustCompile(`!\[[^\]\r\n]*\]\(([^)\r\n]+)\)`)

func normalizedInboundMedia(message *channeltypes.NormalizedMessage) (string, []map[string]any) {
	resources := make([]map[string]any, 0, len(message.Resources))
	imageKeys := make(map[string]bool)
	for _, resource := range message.Resources {
		resources = append(resources, map[string]any{"type": resource.Type, "file_key": resource.FileKey, "file_name": resource.FileName})
		if resource.Type == "image" && resource.FileKey != "" {
			imageKeys[resource.FileKey] = true
		}
	}
	content := inboundImageReference.ReplaceAllStringFunc(message.Content, func(reference string) string {
		match := inboundImageReference.FindStringSubmatch(reference)
		if imageKeys[match[1]] {
			return "[图片附件]"
		}
		return reference
	})
	for _, resource := range message.Resources {
		if resource.FileKey != "" {
			content = strings.ReplaceAll(content, resource.FileKey, "[附件]")
		}
	}
	return content, resources
}

type limitedMediaBody struct {
	io.ReadCloser
	remaining int64
}

func (body *limitedMediaBody) Read(buffer []byte) (int, error) {
	if body.remaining <= 0 {
		var probe [1]byte
		count, err := body.ReadCloser.Read(probe[:])
		if count > 0 {
			return 0, errMediaSize
		}
		return 0, err
	}
	if int64(len(buffer)) > body.remaining {
		buffer = buffer[:body.remaining]
	}
	count, err := body.ReadCloser.Read(buffer)
	body.remaining -= int64(count)
	return count, err
}

type mediaHTTPClient struct {
	client interface {
		Do(*http.Request) (*http.Response, error)
	}
}

func (client mediaHTTPClient) Do(request *http.Request) (*http.Response, error) {
	response, err := client.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusOK && strings.Contains(request.URL.Path, "/im/v1/messages/") && strings.Contains(request.URL.Path, "/resources/") {
		limit := maxMediaFileBytes
		if request.URL.Query().Get("type") == "image" {
			limit = maxMediaImageBytes
		}
		if response.ContentLength > limit {
			response.Body.Close()
			return nil, errMediaSize
		}
		response.Body = &limitedMediaBody{ReadCloser: response.Body, remaining: limit}
	}
	return response, nil
}

func (c *runtimeClient) downloadResource(ctx context.Context, client *lark.Client, item command) error {
	kind := stringValue(item.Payload, "resource_type")
	if kind != "image" && kind != "file" {
		return errMediaType
	}
	operationCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := c.progress(operationCtx, item, map[string]any{"status": "downloading"}); err != nil {
		return mediaStageError("progress", err)
	}
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-operationCtx.Done():
				return
			case <-ticker.C:
				_ = c.progress(operationCtx, item, map[string]any{"status": "downloading"})
			}
		}
	}()
	request := larkim.NewGetMessageResourceReqBuilder().MessageId(stringValue(item.Payload, "message_id")).FileKey(stringValue(item.Payload, "resource_key")).Type(kind).Build()
	response, err := client.Im.V1.MessageResource.Get(operationCtx, request)
	if err != nil {
		return mediaStageError("download", err)
	}
	if !response.Success() || response.File == nil {
		return mediaProviderError("download", response.StatusCode, response.Code)
	}
	limit := maxMediaFileBytes
	if kind == "image" {
		limit = maxMediaImageBytes
	}
	content, err := io.ReadAll(io.LimitReader(response.File, limit+1))
	if err != nil {
		return mediaStageError("download", err)
	}
	if len(content) == 0 || int64(len(content)) > limit {
		return errMediaSize
	}
	name := stringValue(item.Payload, "file_name")
	if name == "" {
		name = response.FileName
	}
	if kind == "image" {
		mediaType := http.DetectContentType(content)
		if !supportedImage(mediaType) {
			return errMediaType
		}
		name = "image." + strings.TrimPrefix(mediaType, "image/")
	}
	if name == "" {
		name = "attachment"
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	field, err := form.CreateFormFile("file", name)
	if err != nil {
		return err
	}
	if _, err = field.Write(content); err != nil {
		return err
	}
	if err = form.Close(); err != nil {
		return err
	}
	upload, err := http.NewRequestWithContext(operationCtx, http.MethodPost, c.baseURL+"/api/channel-runtime/v1/commands/"+item.ID+"/attachment", &body)
	if err != nil {
		return err
	}
	upload.Header.Set("Authorization", "Bearer "+c.token)
	upload.Header.Set("X-AHA-Lease-ID", item.LeaseID)
	upload.Header.Set("Content-Type", form.FormDataContentType())
	result, err := c.http.Do(upload)
	if err != nil {
		return mediaStageError("transfer", err)
	}
	defer result.Body.Close()
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(result.Body, 4096)).Decode(&failure)
		if failure.Error == errMediaSize.Error() {
			return errMediaSize
		}
		if failure.Error == errMediaType.Error() {
			return errMediaType
		}
		return mediaStageError("transfer", runtimeStatusError(result.StatusCode))
	}
	return nil
}

func mediaErrorCode(err error) string {
	if errors.Is(err, errMediaSize) {
		return errMediaSize.Error()
	}
	if errors.Is(err, errMediaType) {
		return errMediaType.Error()
	}
	var failure mediaFailure
	if errors.As(err, &failure) {
		return failure.code
	}
	return "resource_download_failed"
}

func supportedImage(mediaType string) bool {
	switch mediaType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}

func (c *runtimeClient) recordMedia(ctx context.Context, item delivery, kind, key string) error {
	return c.request(ctx, http.MethodPost, "/api/channel-runtime/v1/deliveries/"+item.ID+"/media", map[string]any{"schema_version": 1, "lease_id": item.LeaseID, "resource_type": kind, "resource_key": key}, &map[string]any{})
}

func (c *runtimeClient) sendAttachmentDelivery(ctx context.Context, client *lark.Client, item delivery) (string, string, error) {
	operationCtx, cancel := context.WithTimeout(ctx, 80*time.Second)
	defer cancel()
	if err := c.recordMedia(operationCtx, item, "", ""); err != nil {
		return "", "", mediaStageError("record", err)
	}
	kind, key := stringValue(item.SemanticPayload, "provider_resource_type"), stringValue(item.SemanticPayload, "provider_resource_key")
	if key == "" {
		request, err := http.NewRequestWithContext(operationCtx, http.MethodGet, c.baseURL+"/api/channel-runtime/v1/deliveries/"+item.ID+"/attachment", nil)
		if err != nil {
			return "", "", err
		}
		request.Header.Set("Authorization", "Bearer "+c.token)
		request.Header.Set("X-AHA-Lease-ID", item.LeaseID)
		response, err := c.http.Do(request)
		if err != nil {
			return "", "", mediaStageError("read", err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return "", "", mediaStageError("read", runtimeStatusError(response.StatusCode))
		}
		if response.ContentLength > maxMediaFileBytes {
			return "", "", errMediaSize
		}
		content, err := io.ReadAll(io.LimitReader(response.Body, maxMediaFileBytes+1))
		if err != nil {
			return "", "", mediaStageError("read", err)
		}
		if len(content) == 0 || int64(len(content)) > maxMediaFileBytes {
			return "", "", errMediaSize
		}
		kind = "file"
		if supportedImage(http.DetectContentType(content)) && int64(len(content)) <= maxMediaImageBytes {
			kind = "image"
		}
		if kind == "image" {
			request := larkim.NewCreateImageReqBuilder().Body(larkim.NewCreateImageReqBodyBuilder().ImageType("message").Image(bytes.NewReader(content)).Build()).Build()
			response, err := client.Im.V1.Image.Create(operationCtx, request)
			if err != nil {
				return "", "", mediaStageError("upload_image", err)
			}
			if !response.Success() || response.Data == nil || response.Data.ImageKey == nil {
				return "", "", mediaProviderError("upload_image", response.StatusCode, response.Code)
			}
			key = *response.Data.ImageKey
		} else {
			name := stringValue(item.SemanticPayload, "name")
			if name == "" {
				name = "attachment"
			}
			request := larkim.NewCreateFileReqBuilder().Body(larkim.NewCreateFileReqBodyBuilder().FileType("stream").FileName(name).File(bytes.NewReader(content)).Build()).Build()
			response, err := client.Im.V1.File.Create(operationCtx, request)
			if err != nil {
				return "", "", mediaStageError("upload_file", err)
			}
			if !response.Success() || response.Data == nil || response.Data.FileKey == nil {
				return "", "", mediaProviderError("upload_file", response.StatusCode, response.Code)
			}
			key = *response.Data.FileKey
		}
		if key == "" {
			return "", "", fmt.Errorf("media_upload_failed")
		}
		if err := c.recordMedia(operationCtx, item, kind, key); err != nil {
			return "", "", mediaStageError("record", err)
		}
	}
	item.SemanticPayload = map[string]any{"kind": "provider_media", "resource_type": kind, "resource_key": key}
	messageID, requestID, err := sendDelivery(operationCtx, client, item)
	return messageID, requestID, mediaStageError("send", err)
}
