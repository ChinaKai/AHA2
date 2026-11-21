package httpapi

import "net/http"

func (s *Server) browserMCPOperations() []agentMCPOperation {
	op := func(name, description, method string, handler http.HandlerFunc, tab, write bool, fields map[string]any, required ...string) agentMCPOperation {
		properties := map[string]any{"grant_id": mcpString()}
		path := map[string]string{"grant_id": "grant"}
		keys := []string{"grant_id"}
		if tab {
			properties["tab_id"] = mcpString()
			path["tab_id"] = "tab"
			keys = append(keys, "tab_id")
		}
		for key, value := range fields {
			properties[key] = value
		}
		return agentMCPOperation{Tool: mcpTool{Name: name, Description: description, InputSchema: mcpObject(properties, append(keys, required...)...)}, Method: method, Handler: handler, PathFields: path, Write: write}
	}
	items := []agentMCPOperation{
		op("browser_tab_open", "Open a tab within an existing Task grant and its approved capabilities.", http.MethodPost, s.agentBrowserOpenTab, false, true, map[string]any{"url": mcpString()}),
		op("browser_tab_bind_current", "Bind the current tab within the existing Task grant.", http.MethodPost, s.agentBrowserBindCurrent, false, true, nil),
		op("browser_tab_activate", "Activate an existing granted tab.", http.MethodPost, s.agentBrowserActivate, true, true, nil),
		op("browser_tab_close", "Close an existing granted tab.", http.MethodDelete, s.agentBrowserCloseTab, true, true, nil),
		op("browser_navigate", "Navigate an existing granted tab. Existing URL and capability policy applies.", http.MethodPost, s.agentBrowserNavigate, true, true, map[string]any{"url": mcpString()}, "url"),
		op("browser_action", "Act on an approved tab using a snapshot ref or coordinates. Requires existing action capability; does not expand the share target.", http.MethodPost, s.agentBrowserAction, true, true, map[string]any{
			"kind": mcpEnum("click", "double_click", "type", "key", "scroll", "hover"), "ref": mcpString(), "x": mcpInteger(0, 100000), "y": mcpInteger(0, 100000), "text": map[string]any{"type": "string", "maxLength": 65536}, "keys": map[string]any{"type": "array", "items": mcpString(), "maxItems": 16}, "delta_x": mcpInteger(-100000, 100000), "delta_y": mcpInteger(-100000, 100000)}, "kind"),
		op("browser_upload", "Upload bounded file bytes to an approved tab using the existing files capability. For larger files use the Task-scoped Agent upload transport.", http.MethodPost, s.agentBrowserUpload, true, true, map[string]any{
			"ref": mcpString(), "x": mcpInteger(0, 100000), "y": mcpInteger(0, 100000), "files": map[string]any{"type": "array", "minItems": 1, "maxItems": 5, "items": mcpObject(map[string]any{"name": mcpString(), "content_base64": map[string]any{"type": "string", "maxLength": 524288}}, "name", "content_base64")}}, "files"),
		op("browser_frame", "Read one bounded frame from an approved tab. Returns media_type and base64 bytes.", http.MethodGet, s.agentBrowserFrame, true, false, nil),
		op("browser_downloads_list", "List downloads belonging to the current Task grant.", http.MethodGet, s.agentBrowserDownloads, false, false, nil),
		op("browser_download_get", "Read a bounded download belonging to the current Task grant. Larger downloads use the same authenticated Agent byte transport.", http.MethodGet, s.agentBrowserDownload, false, false, map[string]any{"download_id": mcpString()}, "download_id"),
		op("browser_bookmarks_list", "Read bookmarks available under the current Task grant.", http.MethodGet, s.agentBrowserBookmarks, false, false, nil),
		op("browser_cookie_metadata", "Read cookie names, domains, expiry and security flags under the current Task grant. Cookie values are never returned; import/export of values requires owner secure input or download.", http.MethodGet, s.browserMCPCookieMetadata, false, false, map[string]any{"url": mcpString(), "domain": mcpString()}),
		op("browser_release", "Release this Task's browser grant. Revocation takes effect for subsequent calls immediately.", http.MethodDelete, s.agentBrowserRelease, false, true, nil),
	}
	for index := range items {
		if items[index].Tool.Name == "browser_frame" || items[index].Tool.Name == "browser_download_get" {
			items[index].Binary = true
		}
		if items[index].Tool.Name == "browser_cookie_metadata" {
			items[index].QueryFields = []string{"url", "domain"}
		}
		if items[index].Tool.Name == "browser_download_get" {
			items[index].PathFields["download_id"] = "download"
		}
	}
	return items
}

func (s *Server) browserMCPCookieMetadata(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	items, truncated, err := service.ExportCookies(request.Context(), browserActor(request), grant.ID, request.URL.Query().Get("url"), request.URL.Query().Get("domain"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	metadata := make([]map[string]any, 0, len(items))
	for _, item := range items {
		metadata = append(metadata, map[string]any{"name": item.Name, "domain": item.Domain, "path": item.Path, "expires": item.Expires, "http_only": item.HTTPOnly, "secure": item.Secure, "same_site": item.SameSite})
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "cookies": metadata, "truncated": truncated})
}

// Mutation results expose only the identifiers needed for the next operation.
// Host/session/profile handles and URL credentials never leave this adapter.
func browserMCPSafeResult(name string, result map[string]any) map[string]any {
	view := map[string]any{"ok": result["ok"]}
	switch name {
	case "browser_tab_open", "browser_tab_bind_current", "browser_tab_activate", "browser_navigate":
		if tab, ok := result["tab"].(map[string]any); ok {
			url, _ := tab["url"].(string)
			safe, site := sanitizeBrowserMCPURL(url)
			view["tab"] = map[string]any{"id": tab["id"], "title": tab["title"], "url": safe, "site": site, "active": tab["active"], "focused": tab["focused"]}
		}
	case "browser_bookmarks_list":
		items := []map[string]any{}
		if bookmarks, ok := result["bookmarks"].([]any); ok {
			for _, raw := range bookmarks {
				if item, ok := raw.(map[string]any); ok {
					url, _ := item["url"].(string)
					safe, _ := sanitizeBrowserMCPURL(url)
					items = append(items, map[string]any{"title": item["title"], "url": safe, "folder": item["folder"]})
				}
			}
		}
		view["bookmarks"] = items
	case "browser_downloads_list":
		view["downloads"] = result["downloads"]
	case "browser_cookie_metadata":
		view["cookies"] = result["cookies"]
	case "browser_frame", "browser_download_get":
		return result
	}
	return view
}

func (s *Server) browserMCPToolsFor(write bool) []mcpTool {
	tools := agentBrowserMCPTools()
	for _, operation := range s.browserMCPOperations() {
		if !operation.Write || write {
			tools = append(tools, operation.Tool)
		}
	}
	return tools
}
