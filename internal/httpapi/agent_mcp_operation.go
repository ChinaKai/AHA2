package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// agentMCPOperation binds one named, schema-checked tool to an existing Agent
// handler. Neither a URL nor an HTTP method is accepted from the model. The
// original capability context is retained; no owner identity is manufactured.
type agentMCPOperation struct {
	Tool         mcpTool
	Handler      http.HandlerFunc
	Method       string
	PathFields   map[string]string
	QueryFields  []string
	Write        bool
	ResultFields []string
	Binary       bool
}

func mcpObject(properties map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func mcpString() map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": 4096}
}
func mcpEnum(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}
func mcpInteger(min, max int) map[string]any {
	return map[string]any{"type": "integer", "minimum": min, "maximum": max}
}

// validateMCPOperation enforces the supported schema vocabulary server-side.
// All operation schemas use this finite subset. Unknown input keys, nulls,
// wrong types and out-of-range values fail before any business handler runs.
func validateMCPOperation(value any, schema map[string]any) error {
	fail := errors.New("invalid tool arguments")
	switch schema["type"] {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fail
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			return fail
		}
		for key, child := range object {
			spec, ok := properties[key].(map[string]any)
			if !ok {
				spec, ok = schema["additionalProperties"].(map[string]any)
				if !ok {
					return fail
				}
			}
			if err := validateMCPOperation(child, spec); err != nil {
				return fail
			}
		}
		if max, ok := schema["maxProperties"].(int); ok && len(object) > max {
			return fail
		}
		for _, key := range schema["required"].([]string) {
			if _, ok := object[key]; !ok {
				return fail
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return fail
		}
		if min, ok := schema["minLength"].(int); ok && len([]rune(text)) < min {
			return fail
		}
		if max, ok := schema["maxLength"].(int); ok && len([]rune(text)) > max {
			return fail
		}
		if values, ok := schema["enum"].([]string); ok {
			found := false
			for _, item := range values {
				if text == item {
					found = true
				}
			}
			if !found {
				return fail
			}
		}
	case "integer", "number":
		number, ok := value.(json.Number)
		if !ok {
			return fail
		}
		var n float64
		if schema["type"] == "integer" {
			i, err := number.Int64()
			if err != nil {
				return fail
			}
			n = float64(i)
		} else {
			var err error
			n, err = number.Float64()
			if err != nil {
				return fail
			}
		}
		if min, ok := schema["minimum"].(int); ok && n < float64(min) {
			return fail
		}
		if max, ok := schema["maximum"].(int); ok && n > float64(max) {
			return fail
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fail
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fail
		}
		if min, ok := schema["minItems"].(int); ok && len(items) < min {
			return fail
		}
		if max, ok := schema["maxItems"].(int); ok && len(items) > max {
			return fail
		}
		child, ok := schema["items"].(map[string]any)
		if !ok {
			return fail
		}
		for _, item := range items {
			if validateMCPOperation(item, child) != nil {
				return fail
			}
		}
	default:
		return fail
	}
	return nil
}

// mcpOperationResponse bounds buffered results even if a handler writes more
// than the model-facing budget. Headers (including cookies) are never copied.
type mcpOperationResponse struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (r *mcpOperationResponse) Header() http.Header { return r.header }
func (r *mcpOperationResponse) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}
func (r *mcpOperationResponse) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.body.Len()+len(p) > 2<<20 {
		r.overflow = true
		return 0, io.ErrShortBuffer
	}
	return r.body.Write(p)
}

func runAgentMCPOperation(request *http.Request, op agentMCPOperation, raw json.RawMessage) (result map[string]any, err error) {
	if request.Context().Err() != nil {
		return nil, errors.New("operation cancelled")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var arguments map[string]any
	if decoder.Decode(&arguments) != nil || validateMCPOperation(arguments, op.Tool.InputSchema) != nil {
		return nil, errors.New("invalid tool arguments")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid tool arguments")
	}
	child := request.Clone(request.Context())
	child.Method = op.Method
	child.Header = make(http.Header)
	child.URL = &url.URL{Path: request.URL.Path}
	query := url.Values{}
	for field, pathName := range op.PathFields {
		if value, ok := arguments[field]; ok {
			child.SetPathValue(pathName, fmt.Sprint(value))
			delete(arguments, field)
		}
	}
	for _, field := range op.QueryFields {
		if value, ok := arguments[field]; ok {
			query.Set(field, fmt.Sprint(value))
			delete(arguments, field)
		}
	}
	child.URL.RawQuery = query.Encode()
	body, err := json.Marshal(arguments)
	if err != nil {
		return nil, errors.New("invalid tool arguments")
	}
	child.Header.Set("Content-Type", "application/json")
	child.Body = io.NopCloser(bytes.NewReader(body))
	child.ContentLength = int64(len(body))
	response := &mcpOperationResponse{header: make(http.Header)}
	defer func() {
		if recovered := recover(); recovered != nil {
			if recovered == http.ErrAbortHandler {
				result = nil
				err = errors.New("operation stream interrupted")
				return
			}
			panic(recovered)
		}
	}()
	op.Handler(response, child)
	// Raw runtime errors can contain endpoints or credentials. Only a fixed
	// classification leaves this boundary, and no body is logged here.
	if response.status >= 400 {
		return nil, errors.New("operation failed; check resource permissions and state")
	}
	if response.overflow {
		return nil, errors.New("operation response exceeds limit")
	}
	if op.Binary {
		return map[string]any{"media_type": response.header.Get("Content-Type"), "content_base64": base64.StdEncoding.EncodeToString(response.body.Bytes())}, nil
	}
	if json.Unmarshal(response.body.Bytes(), &result) != nil {
		return nil, errors.New("operation result unavailable")
	}
	if op.ResultFields != nil {
		view := map[string]any{}
		for _, key := range op.ResultFields {
			if value, ok := result[key]; ok {
				view[key] = value
			}
		}
		return view, nil
	}
	return result, nil
}
