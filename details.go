package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type TokenDetails struct {
	TokenName   string   `json:"token_name"`
	TokenSymbol string   `json:"token_symbol"`
	IsHoneypot  string   `json:"is_honeypot"`
	BuyTax      string   `json:"buy_tax"`
	SellTax     string   `json:"sell_tax"`
	HolderCount string   `json:"holder_count"`
	Holders     []Holder `json:"holders"`
}

type Holder struct {
	Address string `json:"address"`
	Percent string `json:"percent"`
}

type APIResponse struct {
	Result map[string]TokenDetails `json:"result"`
}

func getDetails(address string) (*TokenDetails, error) {
	baseURL := "https://api.gopluslabs.io/api/v1/token_security/1"

	// Create URL with parameters
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	params := url.Values{}
	params.Add("contract_addresses", address)
	u.RawQuery = params.Encode()

	// Create HTTP request
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "*/*")

	// Make HTTP request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse JSON response
	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Get the first (and should be only) result
	for _, details := range apiResp.Result {
		return &details, nil
	}

	return nil, fmt.Errorf("no details found for address: %s", address)
}
