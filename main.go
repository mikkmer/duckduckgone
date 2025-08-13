package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

const (
	confFileName = ".ddg.conf"
	apiKeyKey    = "api"
	clipKey      = "clipboard"
	defaultClip  = "yes"
	endpoint     = "https://quack.duckduckgo.com/api/email/addresses"
)

type conf struct {
	APIKey    string
	Clipboard string
}

type ddgResp struct {
	Address string `json:"address"`
}

func main() {
	printBanner()
	cmd := ""
	if len(os.Args) > 1 {
		cmd = strings.ToLower(os.Args[1])
	}

	switch cmd {
	case "gen", "generate":
		doGenerate()
	case "settings":
		doSettings()
	case "help", "":
		showHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		showHelp()
	}
}

func printBanner() {
	orange := "\033[38;5;214m"
	reset := "\033[0m"

	fmt.Printf(`%s ____             _    ____             _     ____                  
|  _ \ _   _  ___| | _|  _ \ _   _  ___| | __/ ___| ___  _ __   ___ 
| | | | | | |/ __| |/ / | | | | | |/ __| |/ / |  _ / _ \| '_ \ / _ \
| |_| | |_| | (__|   <| |_| | |_| | (__|   <| |_| | (_) | | | |  __/
|____/ \__,_|\___|_|\_\____/ \__,_|\___|_|\_\\____|\___/|_| |_|\___/%s

`, orange, reset)
}

func showHelp() {
	fmt.Println(`Usage: ddg <command>

Commands:
  gen, generate    Generate new Duck email
  settings         View or change settings
  help             Show this help

Examples:
  ddg gen
  ddg settings`)
}

func doGenerate() {
	cfg, err := ensureConfig()
	if err != nil {
		exitErr(err)
	}
	email, _, err := requestEmail(cfg.APIKey)
	if err != nil {
		if respErr, ok := err.(*httpError); ok && respErr.StatusCode == 401 {
			fmt.Fprintf(os.Stderr, "\033[31mError! Invalid token\033[0m\n")
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\033[36m%s\033[0m\n", email)
	if strings.EqualFold(cfg.Clipboard, "yes") {
		if err := copyToClipboard(email); err == nil {
			fmt.Println("(copied to clipboard)")
		}
	}
}

func doSettings() {
	cfg, _ := readConfig() // ignore error
	if cfg.Clipboard == "" {
		cfg.Clipboard = defaultClip
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("Current API key: %s\n", emptyToDash(cfg.APIKey))
	fmt.Printf("Clipboard copy: %s\n\n", cfg.Clipboard)
	fmt.Println("Press Enter to keep current value.")

	fmt.Printf("New API key [%s]: ", cfg.APIKey)
	newAPI := strings.TrimSpace(readLine(reader))
	if newAPI != "" {
		cfg.APIKey = newAPI
	}

	fmt.Printf("Clipboard copy yes/no [%s]: ", cfg.Clipboard)
	newClip := strings.TrimSpace(readLine(reader))
	if newClip != "" && (strings.EqualFold(newClip, "yes") || strings.EqualFold(newClip, "no")) {
		cfg.Clipboard = strings.ToLower(newClip)
	}

	if err := writeConfig(cfg); err != nil {
		exitErr(err)
	}
	fmt.Println("✅ Settings updated.")
}

type httpError struct {
	StatusCode int
	Err        error
}

func (h *httpError) Error() string {
	return h.Err.Error()
}

func requestEmail(apiKey string) (string, []byte, error) {
	req, err := http.NewRequest("POST", endpoint, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 401 {
		// Only print invalid token, no response
		return "", body, &httpError{StatusCode: 401, Err: fmt.Errorf("invalid token")}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", body, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var parsed ddgResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", body, fmt.Errorf("decode error: %w", err)
	}
	if parsed.Address == "" {
		return "", body, fmt.Errorf("no address in response")
	}
	return parsed.Address + "@duck.com", body, nil
}

func ensureConfig() (conf, error) {
	cfg, err := readConfig()
	if err == nil && cfg.APIKey != "" {
		if cfg.Clipboard == "" {
			cfg.Clipboard = defaultClip
			_ = writeConfig(cfg)
		}
		return cfg, nil
	}

	fmt.Println("Hi! Looks like you haven't used duckduckgone before!")
	fmt.Print("Enter your API key: ")
	reader := bufio.NewReader(os.Stdin)
	api := strings.TrimSpace(readLine(reader))
	if api == "" {
		return conf{}, fmt.Errorf("no API key provided")
	}

	cfg = conf{APIKey: api, Clipboard: defaultClip}
	if err := writeConfig(cfg); err != nil {
		return conf{}, err
	}
	return cfg, nil
}

func readConfig() (conf, error) {
	path, err := confPath()
	if err != nil {
		return conf{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return conf{}, err
	}
	var c conf
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := sc.Text()
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch strings.ToLower(key) {
		case apiKeyKey:
			c.APIKey = trimQuotes(val)
		case clipKey:
			c.Clipboard = strings.ToLower(trimQuotes(val))
		}
	}
	return c, nil
}

func writeConfig(c conf) error {
	path, err := confPath()
	if err != nil {
		return err
	}
	data := fmt.Sprintf("api = %s\nclipboard = %s\n", c.APIKey, c.Clipboard)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(data); err != nil {
		return err
	}
	_ = os.Chmod(path, 0600)
	return nil
}

func confPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, confFileName), nil
}

func trimQuotes(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'`)
	return s
}

func emptyToDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func readLine(r *bufio.Reader) string {
	text, _ := r.ReadString('\n')
	return strings.TrimRight(text, "\r\n")
}

func copyToClipboard(text string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("clipboard not supported on %s", runtime.GOOS)
	}
	cmd := exec.Command("pbcopy")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if _, err := io.WriteString(stdin, text); err != nil {
		return err
	}
	_ = stdin.Close()
	return cmd.Wait()
}

func exitErr(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	if ee, ok := err.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
			os.Exit(ws.ExitStatus())
		}
	}
	os.Exit(1)
}
