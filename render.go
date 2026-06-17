package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// RenderTranscript produces a human-readable conversation view for the preview
// pane: user / assistant turns with tool calls summarised. It caps total output
// so a 20MB transcript never blows up the viewport.
func RenderTranscript(path string, maxChars int) string {
	f, err := os.Open(path)
	if err != nil {
		return "could not open transcript: " + err.Error()
	}
	defer f.Close()

	var b strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 32*1024*1024) // tolerate very long lines

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e struct {
			Type    string          `json:"type"`
			Message json.RawMessage `json:"message"`
		}
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		switch e.Type {
		case "user":
			if s := renderMessage(e.Message, "user"); s != "" {
				b.WriteString(s)
			}
		case "assistant":
			if s := renderMessage(e.Message, "assistant"); s != "" {
				b.WriteString(s)
			}
		}
		if b.Len() >= maxChars {
			b.WriteString("\n… (truncated; transcript is large)\n")
			break
		}
	}
	if b.Len() == 0 {
		return "(no user/assistant turns found)"
	}
	return b.String()
}

func renderMessage(raw json.RawMessage, role string) string {
	header := "\n" + roleTag(role) + "\n"

	// content can be a plain string or a list of typed blocks
	var asString struct {
		Content string `json:"content"`
	}
	if json.Unmarshal(raw, &asString) == nil && asString.Content != "" {
		return header + indent(asString.Content) + "\n"
	}

	var asBlocks struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &asBlocks) != nil {
		return ""
	}
	var parts []string
	for _, blk := range asBlocks.Content {
		switch blk.Type {
		case "text":
			if strings.TrimSpace(blk.Text) != "" {
				parts = append(parts, indent(blk.Text))
			}
		case "tool_use":
			parts = append(parts, indent(fmt.Sprintf("⚙ [tool: %s]", blk.Name)))
		case "tool_result":
			parts = append(parts, indent("↩ [tool result]"))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return header + strings.Join(parts, "\n") + "\n"
}

func roleTag(role string) string {
	switch role {
	case "user":
		return "▌ user"
	case "assistant":
		return "▌ assistant"
	}
	return "▌ " + role
}

func indent(s string) string {
	s = strings.TrimRight(s, "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}
