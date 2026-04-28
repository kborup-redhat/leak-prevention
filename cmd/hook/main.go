package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const serverURL = "http://localhost:8642"

type matchEntry struct {
	Name     string `json:"name"`
	Parent   string `json:"parent,omitempty"`
	Category string `json:"category"`
}

type checkResponse struct {
	Blocked           bool         `json:"blocked"`
	Matches           []matchEntry `json:"matches"`
	AllowlistCommands string       `json:"allowlist_commands,omitempty"`
}

func main() {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprint(os.Stderr, formatServerDownResponse())
		os.Exit(1)
	}

	var hookInput struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(input, &hookInput); err != nil || hookInput.Prompt == "" {
		os.Exit(0)
	}

	result, err := callServer(serverURL+"/check", hookInput.Prompt)
	if err != nil {
		fmt.Print(formatServerDownResponse())
		os.Exit(0)
	}

	if result.Blocked {
		fmt.Print(formatBlockResponse(result.Matches))
	}
}

func callServer(url, prompt string) (*checkResponse, error) {
	body, err := json.Marshal(map[string]string{"prompt": prompt})
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result checkResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

func formatBlockResponse(matches []matchEntry) string {
	var names []string
	for _, m := range matches {
		names = append(names, m.Name)
	}
	namesList := strings.Join(names, ", ")

	var cmds []string
	for _, m := range matches {
		cmds = append(cmds, fmt.Sprintf(
			`  ! curl -s -X POST -H 'Content-Type: application/json' -d '{"term":"%s"}' %s/allowlist`,
			m.Name, serverURL,
		))
	}

	reason := fmt.Sprintf(
		"Organization name(s) detected: %s\\n\\nTo allowlist, run:\\n%s\\n\\nThen re-send your message.",
		namesList, strings.Join(cmds, "\\n"),
	)

	resp := map[string]string{
		"decision": "block",
		"reason":   reason,
	}
	out, _ := json.Marshal(resp)
	return string(out)
}

func formatServerDownResponse() string {
	resp := map[string]string{
		"decision": "block",
		"reason":   "Leak prevention server is not running.\\n\\nStart it with:\\n  ! podman start leak-prevention\\n\\nThen re-send your message.",
	}
	out, _ := json.Marshal(resp)
	return string(out)
}
