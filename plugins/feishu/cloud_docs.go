package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkdocx "github.com/larksuite/oapi-sdk-go/v3/service/docx/v1"
	larkwiki "github.com/larksuite/oapi-sdk-go/v3/service/wiki/v2"
)

const (
	cloudDocumentTimeout  = 5 * time.Second
	maxCloudDocumentLinks = 3
	maxCloudDocumentRunes = 24000
)

var inboundCloudDocumentLink = regexp.MustCompile(`https?://([A-Za-z0-9.-]+)/(wiki|docx)/([A-Za-z0-9_-]+)(?:[?#][^\s<>()\[\]]*)?`)

type cloudDocumentLink struct {
	kind  string
	token string
}

type cloudDocumentReader interface {
	Read(context.Context, cloudDocumentLink) (string, string, error)
}

type larkCloudDocumentReader struct {
	client *lark.Client
}

func enrichInboundCloudDocuments(ctx context.Context, reader cloudDocumentReader, content string) string {
	if reader == nil {
		return content
	}
	links := inboundCloudDocumentLinks(content)
	if len(links) == 0 {
		return content
	}

	lookupCtx, cancel := context.WithTimeout(ctx, cloudDocumentTimeout)
	defer cancel()
	parts := []string{}
	remaining := maxCloudDocumentRunes
	for _, link := range links {
		if lookupCtx.Err() != nil || remaining <= 0 {
			break
		}
		title, documentContent, err := reader.Read(lookupCtx, link)
		if err != nil {
			parts = append(parts, fmt.Sprintf("[飞书云文档读取失败: %s]", err))
			continue
		}
		documentContent = truncateCloudDocumentRunes(documentContent, remaining)
		if documentContent == "" {
			parts = append(parts, "[飞书云文档读取失败: cloud_doc_empty]")
			continue
		}
		remaining -= len([]rune(documentContent))
		if strings.TrimSpace(title) == "" {
			title = "未命名文档"
		}
		parts = append(parts, fmt.Sprintf("[飞书云文档: %s]\n%s\n[/飞书云文档]", title, documentContent))
	}
	if len(parts) == 0 {
		return content
	}
	return strings.TrimSpace(content) + "\n\n" + strings.Join(parts, "\n\n")
}

func inboundCloudDocumentLinks(content string) []cloudDocumentLink {
	matches := inboundCloudDocumentLink.FindAllStringSubmatch(content, -1)
	links := make([]cloudDocumentLink, 0, min(len(matches), maxCloudDocumentLinks))
	seen := map[string]bool{}
	for _, match := range matches {
		if len(links) >= maxCloudDocumentLinks {
			break
		}
		host := strings.ToLower(strings.TrimSpace(match[1]))
		if !isFeishuCloudDocumentHost(host) {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(match[2]))
		token := strings.TrimSpace(match[3])
		key := kind + ":" + token
		if token == "" || seen[key] {
			continue
		}
		seen[key] = true
		links = append(links, cloudDocumentLink{kind: kind, token: token})
	}
	return links
}

func isFeishuCloudDocumentHost(host string) bool {
	for _, suffix := range []string{".feishu.cn", ".larksuite.com", ".larkoffice.com"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func (reader larkCloudDocumentReader) Read(ctx context.Context, link cloudDocumentLink) (string, string, error) {
	if reader.client == nil {
		return "", "", errors.New("cloud_doc_client_missing")
	}
	title := ""
	documentID := link.token
	documentType := link.kind
	if link.kind == "wiki" {
		response, err := reader.client.Wiki.V2.Space.GetNode(
			ctx,
			larkwiki.NewGetNodeSpaceReqBuilder().Token(link.token).ObjType("wiki").Build(),
		)
		if err != nil {
			return "", "", errors.New("cloud_doc_transport_failed")
		}
		if !response.Success() {
			return "", "", fmt.Errorf("cloud_doc_provider_%d", response.Code)
		}
		if response.Data == nil || response.Data.Node == nil {
			return "", "", errors.New("cloud_doc_node_missing")
		}
		node := response.Data.Node
		documentID = optionalString(node.ObjToken)
		documentType = strings.ToLower(optionalString(node.ObjType))
		title = optionalString(node.Title)
	}
	if documentID == "" {
		return "", "", errors.New("cloud_doc_token_missing")
	}
	if documentType != "docx" {
		return "", "", errors.New("cloud_doc_type_unsupported")
	}
	response, err := reader.client.Docx.V1.Document.RawContent(
		ctx,
		larkdocx.NewRawContentDocumentReqBuilder().DocumentId(documentID).Build(),
	)
	if err != nil {
		return "", "", errors.New("cloud_doc_transport_failed")
	}
	if !response.Success() {
		return "", "", fmt.Errorf("cloud_doc_provider_%d", response.Code)
	}
	if response.Data == nil || response.Data.Content == nil {
		return "", "", errors.New("cloud_doc_content_missing")
	}
	return title, strings.TrimSpace(*response.Data.Content), nil
}

func truncateCloudDocumentRunes(content string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(content))
	if len(runes) <= limit {
		return string(runes)
	}
	suffix := "\n[云文档内容已截断]"
	suffixRunes := []rune(suffix)
	if limit <= len(suffixRunes) {
		return string(runes[:limit])
	}
	return string(runes[:limit-len(suffixRunes)]) + suffix
}
