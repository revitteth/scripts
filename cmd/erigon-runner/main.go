package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v2"
)

// Alerting configuration and logic

const LogLevelThreshold = "ERROR"

type PatternConfig struct {
	Pattern        string `json:"pattern"`
	TimeoutMinutes int    `json:"timeoutMinutes"`
}

type Config struct {
	WebhookURL            string          `json:"webhookURL"`
	Patterns              []PatternConfig `json:"patterns"`
	LogFile               string          `json:"logFile"`
	AlertCooldownMinutes  int             `json:"alertCooldownMinutes"`
	DefaultTimeoutMinutes int             `json:"defaultTimeoutMinutes"`
}

type AlertManager struct {
	sentAlerts        map[string]time.Time
	suppressionCounts map[string]int
	mu                sync.Mutex
	defaultCooldown   time.Duration
	patternCooldowns  map[string]time.Duration
}

func NewAlertManager(defaultCooldown time.Duration, patternCooldowns map[string]time.Duration) *AlertManager {
	return &AlertManager{
		sentAlerts:        make(map[string]time.Time),
		suppressionCounts: make(map[string]int),
		defaultCooldown:   defaultCooldown,
		patternCooldowns:  patternCooldowns,
	}
}

func (am *AlertManager) ShouldSendAlert(pattern string) (bool, int) {
	am.mu.Lock()
	defer am.mu.Unlock()

	now := time.Now()
	key := pattern

	cooldown, exists := am.patternCooldowns[pattern]
	if !exists {
		cooldown = am.defaultCooldown
	}

	if lastSent, exists := am.sentAlerts[key]; exists {
		if now.Sub(lastSent) < cooldown {
			am.suppressionCounts[key]++
			return false, am.suppressionCounts[key]
		}
	}

	suppressionCount := am.suppressionCounts[key]
	am.sentAlerts[key] = now
	am.suppressionCounts[key] = 0
	return true, suppressionCount
}

func (am *AlertManager) GetSuppressionCount(pattern string) int {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.suppressionCounts[pattern]
}

func readConfig(filePath string) (*Config, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", filePath, err)
	}
	var config Config
	err = json.Unmarshal(content, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", filePath, err)
	}
	return &config, nil
}

func sendGoogleChatAlert(webhookURL, msgPrefix, log string, suppressionCount int) {
	msgContent := fmt.Sprintf("%s\n%s", msgPrefix, log)
	if suppressionCount > 0 {
		msgContent = fmt.Sprintf("%s\nSuppressed %d duplicate(s)", msgContent, suppressionCount)
	}
	message := map[string]string{"text": msgContent}
	messageBytes, err := json.Marshal(message)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating JSON message: %v\n", err)
		return
	}

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(messageBytes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error sending alert: %v\n", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode > 299 {
		fmt.Println("Alert sent to Google Chat, response status:", resp.Status)
	}
}

func logToFile(log, logFile, msgPrefix string) {
	file, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening file: %v\n", err)
		return
	}
	defer file.Close()
	l := fmt.Sprintf("%s %s \n", msgPrefix, log)
	_, err = file.WriteString(l)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing to file: %v\n", err)
	}
}

func searchLog(log string, patterns []*regexp.Regexp) (bool, string) {
	for _, pattern := range patterns {
		if pattern.MatchString(log) {
			return true, pattern.String()
		}
	}
	return false, ""
}

// Port scanning and configuration updating

func findAvailablePort(port int) (int, error) {
	for {
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			listener.Close()
			return port, nil
		}
		port++
	}
}

func extractPorts(configFile string) (map[string]string, error) {
	absPath, err := filepath.Abs(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for config file: %w", err)
	}

	fmt.Println("Reading config file:", absPath)

	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", absPath, err)
	}

	var config map[string]interface{}
	err = yaml.Unmarshal(content, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", absPath, err)
	}

	ports := make(map[string]string)
	for key, value := range config {
		if strings.Contains(key, "port") || strings.Contains(key, "ports") {
			fmt.Println(key, value)
			portList, ok := value.(string)
			if ok {
				fmt.Println(key, value)
				ports[key] = portList
			}
		}
	}

	fmt.Println(ports)

	return ports, nil
}

func updateConfig(configFile string, ports map[string]string) (string, error) {
	content, err := os.ReadFile(configFile)
	if err != nil {
		return "", fmt.Errorf("failed to read config file %s: %w", configFile, err)
	}

	var config map[string]interface{}
	err = yaml.Unmarshal(content, &config)
	if err != nil {
		return "", fmt.Errorf("failed to parse config file %s: %w", configFile, err)
	}

	for key, portList := range ports {
		portValues := strings.Split(portList, ",")
		newPortList := []string{}

		for _, portStr := range portValues {
			port, err := strconv.Atoi(strings.TrimSpace(portStr))
			if err != nil {
				return "", err
			}
			newPort, err := findAvailablePort(port)
			if err != nil {
				return "", err
			}
			newPortList = append(newPortList, strconv.Itoa(newPort))
		}

		newPortStr := strings.Join(newPortList, ", ")
		config[key] = newPortStr
		fmt.Printf("Updated %s to %s\n", key, newPortStr)
	}

	newConfigFile := configFile[:len(configFile)-len(filepath.Ext(configFile))] + "_new" + filepath.Ext(configFile)
	tempContent, err := yaml.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("failed to marshal updated config: %w", err)
	}

	err = ioutil.WriteFile(newConfigFile, tempContent, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to write new config file: %w", err)
	}

	return newConfigFile, nil
}

func main() {
	// Command-line arguments
	configFile := flag.String("config", "config.json", "Path to the configuration file")
	msgPrefix := flag.String("msg", "", "Chat message prefix")
	erigonRepo := flag.String("repo", ".", "Path to the cdk-erigon repository")
	erigonConfig := flag.String("erigon-config", "hermezconfig-bali.yaml", "Path to the erigon configuration file")
	flag.Parse()

	// Read config for alerts
	config, err := readConfig(*configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading config file: %v\n", err)
		return
	}

	regexPatterns := make([]*regexp.Regexp, len(config.Patterns))
	patternCooldowns := make(map[string]time.Duration)
	for i, patternConfig := range config.Patterns {
		regexPatterns[i] = regexp.MustCompile(patternConfig.Pattern)
		if patternConfig.TimeoutMinutes == 0 {
			patternCooldowns[patternConfig.Pattern] = 24 * time.Hour * 365 * 100 // effectively never
		} else {
			patternCooldowns[patternConfig.Pattern] = time.Duration(patternConfig.TimeoutMinutes) * time.Minute
		}
	}

	defaultCooldown := time.Duration(config.DefaultTimeoutMinutes) * time.Minute
	alertManager := NewAlertManager(defaultCooldown, patternCooldowns)

	// Port configuration
	erigonConfigPath := filepath.Join(*erigonRepo, *erigonConfig)
	fmt.Println("Updating ports in config file:", erigonConfigPath)
	originalPorts, err := extractPorts(erigonConfigPath)
	if err != nil {
		log.Fatalf("Error extracting ports from config file: %v", err)
	}

	tempConfigFile, err := updateConfig(erigonConfigPath, originalPorts)
	if err != nil {
		log.Fatalf("Error updating config file: %v", err)
	}
	defer os.Remove(tempConfigFile) // Clean up temporary file

	// Build the cdk-erigon
	buildCmd := exec.Command("make", "cdk-erigon")
	buildCmd.Dir = *erigonRepo
	if err := buildCmd.Run(); err != nil {
		log.Fatalf("Build failed: %v", err)
	}

	// Run the cdk-erigon with the updated config file
	runCmd := exec.Command("./build/bin/cdk-erigon", "--config="+tempConfigFile)
	runCmd.Dir = *erigonRepo
	stdout, err := runCmd.StdoutPipe()
	if err != nil {
		log.Fatalf("Error creating stdout pipe: %v", err)
	}
	stderr, err := runCmd.StderrPipe()
	if err != nil {
		log.Fatalf("Error creating stderr pipe: %v", err)
	}

	if err := runCmd.Start(); err != nil {
		log.Fatalf("Error starting cdk-erigon: %v", err)
	}

	// Read and process logs
	scanner := bufio.NewScanner(io.MultiReader(stdout, stderr))
	for scanner.Scan() {
		logLine := scanner.Text()
		fmt.Println(logLine)
		logToFile(logLine, config.LogFile, *msgPrefix)
		if match, pattern := searchLog(logLine, regexPatterns); match {
			if shouldSend, suppressionCount := alertManager.ShouldSendAlert(pattern); shouldSend {
				sendGoogleChatAlert(config.WebhookURL, *msgPrefix, logLine, suppressionCount)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading log output: %v\n", err)
	}

	if err := runCmd.Wait(); err != nil {
		log.Fatalf("cdk-erigon finished with error: %v", err)
	}
}
