package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	clientID      = "t-q_gNf12aZbaIAZDtfIwg"
	clientSecret  = "UBc5eTLPQh7n6ZIStrQRWA"
	authURL       = "https://api.freeagent.com/v2/token_endpoint"
	baseURL       = "https://api.freeagent.com/v2"
	tokenFilePath = "tokens.json"
)

type TokenResponse struct {
	AccessToken           string `json:"access_token"`
	TokenType             string `json:"token_type"`
	ExpiresIn             int    `json:"expires_in"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
}

type Timeslip struct {
	Date   string `json:"dated_on"`
	Hours  string `json:"hours"`
	UserID string `json:"user_id"`
}

type TimeslipsResponse struct {
	Timeslips []Timeslip `json:"timeslips"`
}

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	URL   string `json:"url"`
}

type UsersResponse struct {
	Users []User `json:"users"`
}

var tokens TokenResponse

func refreshToken(refreshToken string) (TokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)

	req, err := http.NewRequest("POST", authURL, strings.NewReader(data.Encode()))
	if err != nil {
		return TokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return TokenResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		bodyString := string(bodyBytes)
		return TokenResponse{}, fmt.Errorf("failed to refresh token: %s, body: %s", resp.Status, bodyString)
	}

	var tokenResponse TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResponse); err != nil {
		return TokenResponse{}, err
	}

	tokenResponse.ExpiresIn = int(time.Now().Unix()) + tokenResponse.ExpiresIn

	return tokenResponse, nil
}

func saveTokens(tokens TokenResponse) error {
	file, err := os.Create(tokenFilePath)
	if err != nil {
		return err
	}
	defer file.Close()

	return json.NewEncoder(file).Encode(tokens)
}

func loadTokens() (TokenResponse, error) {
	file, err := os.Open(tokenFilePath)
	if err != nil {
		return TokenResponse{}, err
	}
	defer file.Close()

	var tokenResponse TokenResponse
	if err := json.NewDecoder(file).Decode(&tokenResponse); err != nil {
		return TokenResponse{}, err
	}

	return tokenResponse, nil
}

func getAccessToken() (string, error) {
	if tokens.ExpiresIn <= int(time.Now().Unix()) {
		fmt.Println("Access token expired, refreshing...")
		var err error
		tokens, err = refreshToken(tokens.RefreshToken)
		if err != nil {
			return "", fmt.Errorf("error refreshing token: %w", err)
		}
		if err := saveTokens(tokens); err != nil {
			return "", fmt.Errorf("error saving tokens: %w", err)
		}
	}
	return tokens.AccessToken, nil
}

func getTimeslips(userURL, startDate, endDate string) ([]Timeslip, error) {
	accessToken, err := getAccessToken()
	if err != nil {
		return nil, err
	}

	client := &http.Client{}
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/timeslips?user=%s&from_date=%s&to_date=%s", baseURL, userURL, startDate, endDate), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch timeslips: %s", resp.Status)
	}

	var timeslipsResponse TimeslipsResponse
	if err := json.NewDecoder(resp.Body).Decode(&timeslipsResponse); err != nil {
		return nil, err
	}

	return timeslipsResponse.Timeslips, nil
}

func lastFullWeek() (string, string) {
	now := time.Now()
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	startOfWeek := now.AddDate(0, 0, -weekday-7) // Last week's Monday
	endOfWeek := startOfWeek.AddDate(0, 0, 6)    // Last week's Sunday
	return startOfWeek.Format("2006-01-02"), endOfWeek.Format("2006-01-02")
}

func checkTimesheet(timeslips []Timeslip, startDate, endDate string, expectedHoursPerDay float64, daysPerWeek int) []string {
	totalHours := 0.0
	hoursPerDay := make(map[string]float64)
	var issues []string

	for _, timeslip := range timeslips {
		hours, err := strconv.ParseFloat(timeslip.Hours, 64)
		if err != nil {
			issues = append(issues, fmt.Sprintf("Error parsing hours for timeslip on date %s: %s", timeslip.Date, err))
			continue
		}
		if timeslip.Date >= startDate && timeslip.Date <= endDate {
			totalHours += hours
			hoursPerDay[timeslip.Date] += hours
		}
	}

	expectedTotalHours := expectedHoursPerDay * float64(daysPerWeek)

	if totalHours < expectedTotalHours {
		issues = append(issues, fmt.Sprintf("Total hours %.2f is less than expected %.2f", totalHours, expectedTotalHours))
	}

	for date, hours := range hoursPerDay {
		if hours < 6 {
			issues = append(issues, fmt.Sprintf("Date: %s has less than 6 hours: %.2f hours", date, hours))
		} else if hours > 8 {
			issues = append(issues, fmt.Sprintf("Date: %s has more than 8 hours: %.2f hours", date, hours))
		}
	}

	return issues
}

func main() {
	var err error

	tokens, err = loadTokens()
	if err != nil {
		fmt.Println("Error loading tokens:", err)
		return
	}

	accessToken, err := getAccessToken()
	if err != nil {
		fmt.Println("Error getting access token:", err)
		return
	}

	// Fetch users
	client := &http.Client{}
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/users", baseURL), nil)
	if err != nil {
		fmt.Println("Error creating request:", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Error making request:", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Println("Failed to fetch users:", resp.Status)
		return
	}

	var usersResponse UsersResponse
	if err := json.NewDecoder(resp.Body).Decode(&usersResponse); err != nil {
		fmt.Println("Error decoding response:", err)
		return
	}

	// Define exclusion list by email
	exclusionList := []string{
		"scott@revitt.consulting",
		"jake@revitt.consulting",
		"leon@revitt.consulting",
		"peter@revitt.consulting",
		"zac@biaccountancy.com",
	}

	// Define override list for part-time users
	overrideList := map[string]struct {
		DaysPerWeek         int
		ExpectedHoursPerDay float64
	}{
		"max.bb@revitt.consulting": {DaysPerWeek: 4, ExpectedHoursPerDay: 7.5},
	}

	startDate, endDate := lastFullWeek()

	for _, user := range usersResponse.Users {
		if contains(exclusionList, user.Email) {
			continue
		}

		expectedHoursPerDay := 7.5
		daysPerWeek := 5

		if override, found := overrideList[user.Email]; found {
			expectedHoursPerDay = override.ExpectedHoursPerDay
			daysPerWeek = override.DaysPerWeek
		}

		fmt.Printf("\nChecking timesheet for user: %s (ID: %s)\n", user.Email, user.ID)
		timeslips, err := getTimeslips(user.URL, startDate, endDate)
		if err != nil {
			fmt.Printf("  Error fetching timesheet: %s\n", err)
			continue
		}

		issues := checkTimesheet(timeslips, startDate, endDate, expectedHoursPerDay, daysPerWeek)
		if len(issues) > 0 {
			fmt.Printf("  Issues found:\n")
			for _, issue := range issues {
				fmt.Printf("    - %s\n", issue)
			}
		} else {
			fmt.Printf("  Status: OK\n")
		}
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
