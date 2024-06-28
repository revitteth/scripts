package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sync"
	"time"
)

const (
	LogLevelThreshold = "ERROR"
)

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

func main() {
	configFile := flag.String("config", "config.json", "Path to the configuration file")
	msgPrefix := flag.String("msg", "", "Chat message prefix")
	flag.Parse()

	fmt.Println("prefix:", *msgPrefix)

	hostname, err := os.Hostname()
	if err != nil {
		fmt.Printf("Error getting hostname: %v\n", err)
		return
	}
	fmt.Printf("Hostname: %s\n", hostname)

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

	prefixString := fmt.Sprintf("[%s]: %s", hostname, *msgPrefix)

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		log := scanner.Text()
		fmt.Println(log)
		logToFile(log, config.LogFile, *msgPrefix)
		if match, pattern := searchLog(log, regexPatterns); match {
			if shouldSend, suppressionCount := alertManager.ShouldSendAlert(pattern); shouldSend {
				sendGoogleChatAlert(config.WebhookURL, prefixString, log, suppressionCount)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading standard input: %v\n", err)
	}
}
