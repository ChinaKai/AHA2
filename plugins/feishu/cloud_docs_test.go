package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeCloudDocumentReader struct {
	results map[string]cloudDocumentResult
	links   []cloudDocumentLink
}

type cloudDocumentResult struct {
	title   string
	content string
	err     error
}

func (reader *fakeCloudDocumentReader) Read(_ context.Context, link cloudDocumentLink) (string, string, error) {
	reader.links = append(reader.links, link)
	result, ok := reader.results[link.kind+":"+link.token]
	if !ok {
		return "", "", errors.New("cloud_doc_not_found")
	}
	return result.title, result.content, result.err
}

func TestInboundCloudDocumentLinksAreTenantScopedDeduplicatedAndLimited(t *testing.T) {
	content := strings.Join([]string{
		"https://fcni4lgg1tw3.feishu.cn/wiki/wiki_one?from=message",
		"https://fcni4lgg1tw3.feishu.cn/wiki/wiki_one",
		"https://fcni4lgg1tw3.feishu.cn/docx/docx_two",
		"https://tenant.larksuite.com/docx/docx_three#section",
		"https://tenant.larkoffice.com/wiki/wiki_four",
		"https://evil.example.com/wiki/not_feishu",
		"https://evil.feishu.cn.example.com/wiki/not_feishu",
	}, "\n")

	links := inboundCloudDocumentLinks(content)
	want := []cloudDocumentLink{
		{kind: "wiki", token: "wiki_one"},
		{kind: "docx", token: "docx_two"},
		{kind: "docx", token: "docx_three"},
	}
	if len(links) != len(want) {
		t.Fatalf("links=%#v", links)
	}
	for index := range want {
		if links[index] != want[index] {
			t.Fatalf("links=%#v", links)
		}
	}
}

func TestEnrichInboundCloudDocumentsAppendsReadableContent(t *testing.T) {
	reader := &fakeCloudDocumentReader{results: map[string]cloudDocumentResult{
		"wiki:wiki_one": {
			title:   "接入说明",
			content: "第一段\n第二段",
		},
		"docx:docx_two": {
			err: errors.New("cloud_doc_provider_99991672"),
		},
	}}
	content := "请阅读：https://tenant.feishu.cn/wiki/wiki_one 和 https://tenant.feishu.cn/docx/docx_two"
	got := enrichInboundCloudDocuments(context.Background(), reader, content)
	if !strings.Contains(got, content) ||
		!strings.Contains(got, "[飞书云文档: 接入说明]\n第一段\n第二段\n[/飞书云文档]") ||
		!strings.Contains(got, "[飞书云文档读取失败: cloud_doc_provider_99991672]") {
		t.Fatalf("content=%q", got)
	}
	if len(reader.links) != 2 {
		t.Fatalf("read links=%#v", reader.links)
	}
}

func TestEnrichInboundCloudDocumentsStopsAtTotalContentLimit(t *testing.T) {
	reader := &fakeCloudDocumentReader{results: map[string]cloudDocumentResult{
		"docx:docx_one": {title: "长文档", content: strings.Repeat("甲", maxCloudDocumentRunes+100)},
	}}
	got := enrichInboundCloudDocuments(
		context.Background(),
		reader,
		"https://tenant.feishu.cn/docx/docx_one",
	)
	if !strings.Contains(got, "[云文档内容已截断]") {
		t.Fatalf("truncation marker missing from %q", got)
	}
	if count := strings.Count(got, "甲"); count > maxCloudDocumentRunes {
		t.Fatalf("document content was not capped: %d", count)
	}
}

func TestInboundCloudDocumentLinksIgnoreOrdinaryURLs(t *testing.T) {
	if links := inboundCloudDocumentLinks("https://example.com/wiki/PzkjweK6vicsMSkirqTcFF8vnye"); len(links) != 0 {
		t.Fatalf("links=%#v", links)
	}
}
