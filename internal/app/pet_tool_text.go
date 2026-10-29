package app

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/ChinaKai/AHA2/internal/gateway"
)

var textToolCallPattern = regexp.MustCompile(`(?is)<tool_call>\s*([a-z0-9_]+)\s*(\{.*?\})\s*</tool_call_end>`)

func parseTextToolCalls(text string) ([]gateway.ChatToolCall, string) {
	matches := textToolCallPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil, text
	}
	calls := make([]gateway.ChatToolCall, 0, len(matches))
	var cleaned strings.Builder
	last := 0
	for _, match := range matches {
		cleaned.WriteString(text[last:match[0]])
		name := strings.TrimSpace(text[match[2]:match[3]])
		args := strings.TrimSpace(text[match[4]:match[5]])
		if name != "" && json.Valid([]byte(args)) {
			calls = append(calls, gateway.ChatToolCall{Name: name, Arguments: args})
		}
		last = match[1]
	}
	cleaned.WriteString(text[last:])
	return calls, strings.TrimSpace(cleaned.String())
}

// PetPoint accepts both {"x":0.5,"y":0.5} and [0.5,0.5], because some models
// flatten coordinate pairs into arrays even when the schema asks for objects.
func (p *PetPoint) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var pair []float64
		if err := json.Unmarshal(data, &pair); err != nil {
			return err
		}
		if len(pair) < 2 {
			return fmt.Errorf("point needs two coordinates")
		}
		p.X, p.Y = pair[0], pair[1]
		return nil
	}
	type point PetPoint
	return json.Unmarshal(data, (*point)(p))
}
