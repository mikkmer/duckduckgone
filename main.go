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
	apiKey       = "api"
	clip         = "clipboard"
	ddgGen       = "ddggen"

	defaultClip   = "yes"
	defaultDDGGen = "yes"
	endpoint      = "https://quack.duckduckgo.com/api/email/addresses"
	version       = "1.0.0"
)

type conf struct {
	APIKey        string
	Clipboard     string
	DDGGen        string
	SetupComplete string // Added to track if setup is complete
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

	switch {
	case cmd == "":
		cfg, err := ensureConfig(true)
		if err != nil {
			exitErr(err)
		}
		if strings.EqualFold(cfg.DDGGen, "yes") {
			doGenerate()
		} else {
			showHelp()
		}

	case strings.HasPrefix(cmd, "gen"):
		doGenerate()
	case strings.HasPrefix(cmd, "set"):
		doSettings()
	case cmd == "reset":
		doReset()
	case cmd == "version":
		showVersion()
	case cmd == "help":
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
	cfg, err := ensureConfig(false)
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
	// Update settings with flags
	if len(os.Args) > 2 {
		cfg, err := ensureConfig(false)
		if err != nil {
			exitErr(err)
		}
		for i := 2; i < len(os.Args); i++ {
			arg := os.Args[i]
			if arg == "--apikey" && i+1 < len(os.Args) {
				cfg.APIKey = os.Args[i+1]
				i++
			} else if arg == "--clipboard" && i+1 < len(os.Args) {
				val := strings.ToLower(os.Args[i+1])
				if val == "yes" || val == "no" {
					cfg.Clipboard = val
				}
				i++
			} else if arg == "--ddggen" && i+1 < len(os.Args) {
				val := strings.ToLower(os.Args[i+1])
				if val == "yes" || val == "no" {
					cfg.DDGGen = val
				}
				i++
			} else {
				fmt.Fprintf(os.Stderr, "Unknown argument: %s\n", arg)
				break
			}
		}
		if err := writeConfig(cfg); err != nil {
			exitErr(err)
		}
		fmt.Println("✅ Settings updated.")
		return
	}

	// Show current settings
	cfg, err := ensureConfig(false)
	if err != nil {
		exitErr(err)
	}
	if cfg.Clipboard == "" {
		cfg.Clipboard = defaultClip
	}
	if cfg.DDGGen == "" {
		cfg.DDGGen = defaultDDGGen
	}

	fmt.Printf(
		"Current settings:\n- API key: %s\n- Clipboard copy: %s\n- Run ddg auto-generate: %s\n\nUse 'ddg help' to learn how to change these.\n",
		emptyToDash(cfg.APIKey),
		cfg.Clipboard,
		cfg.DDGGen,
	)
}

func doReset() {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("⚠️ Are you sure you want to completely reset this application? (yes/no): ")
	answer := strings.ToLower(strings.TrimSpace(readLine(reader)))
	if answer != "yes" {
		fmt.Println("❌ Reset cancelled.")
		return
	}

	fmt.Print("Type 'Reset' to reset the application. This is your final chance to go back: ")
	confirm := strings.TrimSpace(readLine(reader))
	if confirm != "Reset" {
		fmt.Println("❌ Reset cancelled.")
		return
	}

	// Overwrite config with setupComplete = false
	cfg := conf{SetupComplete: "false"}
	if err := writeConfig(cfg); err != nil {
		exitErr(err)
	}
	fmt.Println("✅ Application reset. Run 'ddg' again to set up.")
}

func showVersion() {
	fmt.Printf("ddg version %s\n", version)
}

func emptyToDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
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

func ensureConfig(allowSetup bool) (conf, error) {
	cfg, err := readConfig()
	if err == nil && cfg.APIKey != "" && strings.EqualFold(cfg.SetupComplete, "true") {
		if cfg.Clipboard == "" {
			cfg.Clipboard = defaultClip
		}
		if cfg.DDGGen == "" {
			cfg.DDGGen = defaultDDGGen
		}
		_ = writeConfig(cfg)
		return cfg, nil
	}

	if !allowSetup {
		fmt.Println("It looks like you haven't finished setting up DuckDuckGone! Please run ddg to get started.")
		os.Exit(1)
	}

	// Setup wizard
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("Hi! Looks like you haven't used DuckDuckGone before!")

	fmt.Print("Enter your API key: ")
	api := strings.TrimSpace(readLine(reader))
	if api == "" {
		return conf{}, fmt.Errorf("no API key provided")
	}

	fmt.Printf("Copy emails to clipboard automatically? (yes/no) [yes]: ")
	clip := strings.TrimSpace(readLine(reader))
	if clip == "" {
		clip = defaultClip
	}

	fmt.Printf("Run 'ddg' to generate an email automatically? (yes/no) [yes]: ")
	ddggen := strings.TrimSpace(readLine(reader))
	if ddggen == "" {
		ddggen = defaultDDGGen
	}

	cfg = conf{
		APIKey:        api,
		Clipboard:     strings.ToLower(clip),
		DDGGen:        strings.ToLower(ddggen),
		SetupComplete: "true",
	}
	if err := writeConfig(cfg); err != nil {
		return conf{}, err
	}
	return cfg, nil
}

func writeConfig(c conf) error {
	path, err := confPath()
	if err != nil {
		return err
	}
	data := fmt.Sprintf("api = %s\nclipboard = %s\nddggen = %s\nsetupcomplete = %s\n",
		c.APIKey, c.Clipboard, c.DDGGen, c.SetupComplete)
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
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.ToLower(strings.TrimSpace(parts[1]))
		switch key {
		case apiKey:
			c.APIKey = trimQuotes(val)
		case clip:
			c.Clipboard = trimQuotes(val)
		case ddgGen:
			c.DDGGen = trimQuotes(val)
		case "setupcomplete":
			c.SetupComplete = trimQuotes(val)
		}
	}
	return c, nil
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
