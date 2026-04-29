package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const serverURL = "http://localhost:8642"

var version = "dev"

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
	if len(os.Args) > 1 {
		os.Exit(runCLI(os.Args[1:], serverURL))
	}
	runHook()
}

func runHook() {
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

func runCLI(args []string, baseURL string) int {
	if len(args) == 0 {
		printUsage()
		return 1
	}

	client := &http.Client{Timeout: 5 * time.Second}

	switch args[0] {
	case "health":
		return cmdHealth(client, baseURL)
	case "allowlist":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: leak-prevention-hook allowlist <list|add|remove> [term]")
			return 1
		}
		return runAllowlist(client, baseURL, args[1:])
	case "watchlist":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: leak-prevention-hook watchlist <list|add|remove> [term] [--category CAT]")
			return 1
		}
		return runWatchlist(client, baseURL, args[1:])
	case "version", "--version", "-v":
		fmt.Println(version)
		return 0
	case "help", "--help", "-h":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", args[0])
		printUsage()
		return 1
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage: leak-prevention-hook [command]

Without arguments, runs as a Claude Code UserPromptSubmit hook (reads JSON from stdin).

Commands:
  health                        Show server health and counts
  allowlist list                List allowlisted terms
  allowlist add <term>          Add a term to the allowlist
  allowlist remove <term>       Remove a term from the allowlist
  watchlist list                List custom watchlist entries
  watchlist add <term>          Add a custom watchlist entry
  watchlist add <term> --category <CAT>  Add with a category (default: CUSTOM)
  watchlist remove <term>       Remove a custom watchlist entry
  version                       Show version
  help                          Show this help`)
}

func runAllowlist(client *http.Client, baseURL string, args []string) int {
	switch args[0] {
	case "list":
		return cmdAllowlistList(client, baseURL)
	case "add":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: leak-prevention-hook allowlist add <term>")
			return 1
		}
		return cmdAllowlistAdd(client, baseURL, args[1])
	case "remove":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: leak-prevention-hook allowlist remove <term>")
			return 1
		}
		return cmdAllowlistRemove(client, baseURL, args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown allowlist command: %s\n", args[0])
		return 1
	}
}

func runWatchlist(client *http.Client, baseURL string, args []string) int {
	switch args[0] {
	case "list":
		return cmdWatchlistList(client, baseURL)
	case "add":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: leak-prevention-hook watchlist add <term> [--category CAT]")
			return 1
		}
		term := args[1]
		category := ""
		for i := 2; i < len(args)-1; i++ {
			if args[i] == "--category" {
				category = args[i+1]
				break
			}
		}
		return cmdWatchlistAdd(client, baseURL, term, category)
	case "remove":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: leak-prevention-hook watchlist remove <term>")
			return 1
		}
		return cmdWatchlistRemove(client, baseURL, args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown watchlist command: %s\n", args[0])
		return 1
	}
}

func cmdHealth(client *http.Client, baseURL string) int {
	resp, err := client.Get(baseURL + "/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: server not reachable. Start it with: podman start leak-prevention")
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	var health struct {
		Status              string `json:"status"`
		WatchlistCount      int    `json:"watchlist_count"`
		AliasCount          int    `json:"alias_count"`
		CustomWatchlistCount int   `json:"custom_watchlist_count"`
		AllowlistCount      int    `json:"allowlist_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		fmt.Fprintln(os.Stderr, "Error: invalid response from server")
		return 1
	}

	fmt.Printf("Status:           %s\n", health.Status)
	fmt.Printf("Watchlist:        %d companies\n", health.WatchlistCount)
	fmt.Printf("Aliases:          %d\n", health.AliasCount)
	fmt.Printf("Custom watchlist: %d entries\n", health.CustomWatchlistCount)
	fmt.Printf("Allowlist:        %d terms\n", health.AllowlistCount)
	return 0
}

func cmdAllowlistList(client *http.Client, baseURL string) int {
	resp, err := client.Get(baseURL + "/allowlist")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: server not reachable")
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Terms []string `json:"terms"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Fprintln(os.Stderr, "Error: invalid response from server")
		return 1
	}

	if len(result.Terms) == 0 {
		fmt.Println("Allowlist is empty.")
		return 0
	}
	for _, t := range result.Terms {
		fmt.Println(t)
	}
	return 0
}

func cmdAllowlistAdd(client *http.Client, baseURL, term string) int {
	body, _ := json.Marshal(map[string]string{"term": term})
	resp, err := client.Post(baseURL+"/allowlist", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: server not reachable")
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusCreated {
		fmt.Printf("Added \"%s\" to allowlist.\n", term)
		return 0
	}
	fmt.Fprintf(os.Stderr, "Error: server returned %d\n", resp.StatusCode)
	return 1
}

func cmdAllowlistRemove(client *http.Client, baseURL, term string) int {
	deleteURL := baseURL + "/allowlist/" + url.PathEscape(term)
	req, err := http.NewRequest(http.MethodDelete, deleteURL, nil) // #nosec G704 -- baseURL is hardcoded localhost
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: invalid term")
		return 1
	}
	resp, err := client.Do(req) // #nosec G704
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: server not reachable")
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent {
		fmt.Printf("Removed \"%s\" from allowlist.\n", term)
		return 0
	}
	fmt.Fprintf(os.Stderr, "Error: server returned %d\n", resp.StatusCode)
	return 1
}

func cmdWatchlistList(client *http.Client, baseURL string) int {
	resp, err := client.Get(baseURL + "/watchlist/custom")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: server not reachable")
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Entries []struct {
			Term     string `json:"term"`
			Category string `json:"category"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Fprintln(os.Stderr, "Error: invalid response from server")
		return 1
	}

	if len(result.Entries) == 0 {
		fmt.Println("Custom watchlist is empty.")
		return 0
	}
	for _, e := range result.Entries {
		fmt.Printf("%-30s  [%s]\n", e.Term, e.Category)
	}
	return 0
}

func cmdWatchlistAdd(client *http.Client, baseURL, term, category string) int {
	payload := map[string]string{"term": term}
	if category != "" {
		payload["category"] = category
	}
	body, _ := json.Marshal(payload)
	resp, err := client.Post(baseURL+"/watchlist/custom", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: server not reachable")
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusCreated {
		if category == "" {
			category = "CUSTOM"
		}
		fmt.Printf("Added \"%s\" [%s] to custom watchlist.\n", term, category)
		return 0
	}
	fmt.Fprintf(os.Stderr, "Error: server returned %d\n", resp.StatusCode)
	return 1
}

func cmdWatchlistRemove(client *http.Client, baseURL, term string) int {
	deleteURL := baseURL + "/watchlist/custom/" + url.PathEscape(term)
	req, err := http.NewRequest(http.MethodDelete, deleteURL, nil) // #nosec G704 -- baseURL is hardcoded localhost
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: invalid term")
		return 1
	}
	resp, err := client.Do(req) // #nosec G704
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: server not reachable")
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent {
		fmt.Printf("Removed \"%s\" from custom watchlist.\n", term)
		return 0
	}
	fmt.Fprintf(os.Stderr, "Error: server returned %d\n", resp.StatusCode)
	return 1
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

func selfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "leak-prevention-hook"
	}
	return exe
}

func formatBlockResponse(matches []matchEntry) string {
	var names []string
	for _, m := range matches {
		names = append(names, m.Name)
	}
	namesList := strings.Join(names, ", ")

	hookPath := selfPath()
	var cmds []string
	for _, m := range matches {
		cmds = append(cmds, fmt.Sprintf(
			`  ! %s allowlist add "%s"`,
			hookPath, m.Name,
		))
	}

	reason := fmt.Sprintf(
		"Organization name(s) detected: %s\n\nTo allowlist, run:\n%s\n\nThen re-send your message.",
		namesList, strings.Join(cmds, "\n"),
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
		"reason":   "Leak prevention server is not running.\n\nStart it with:\n  ! podman start leak-prevention\n\nThen re-send your message.",
	}
	out, _ := json.Marshal(resp)
	return string(out)
}
