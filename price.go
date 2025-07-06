package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

type PriceSummary struct {
	Price        string      `json:"price"`
	Mcap         int64       `json:"mcap"`
	Swap24h      string      `json:"swap_24h"`
	PriceChange  PriceChange `json:"price_change"`
	HighestPrice string      `json:"highest_price"`
	LowestPrice  string      `json:"lowest_price"`
}

type PriceChange struct {
	Total  int64  `json:"total"`
	Last30 string `json:"last_30"`
	Last15 string `json:"last_15"`
	Last5  string `json:"last_5"`
}

type GeckoResponse struct {
	Included []GeckoIncluded `json:"included"`
}

type GeckoIncluded struct {
	Attributes GeckoAttributes `json:"attributes"`
}

type GeckoAttributes struct {
	BaseAddress                 string          `json:"base_address"`
	BasePriceInUsd              string          `json:"base_price_in_usd"`
	BasePriceInUsdPercentChange string          `json:"base_price_in_usd_percent_change"`
	SwapCount                   string          `json:"swap_count"`
	PriceChangeData             PriceChangeData `json:"price_change_data"`
}

type PriceChangeData struct {
	Last1800s  Last1800s  `json:"last_1800_s"`
	Last900s   Last900s   `json:"last_900_s"`
	Last300s   Last300s   `json:"last_300_s"`
	Last86400s Last86400s `json:"last_86400_s"`
}

type Last1800s struct {
	BaseTokenUsd string `json:"base_token_usd"`
}

type Last900s struct {
	BaseTokenUsd string `json:"base_token_usd"`
}

type Last300s struct {
	BaseTokenUsd string `json:"base_token_usd"`
}

type Last86400s struct {
	Prices Prices `json:"prices"`
}

type Prices struct {
	BaseTokenHighPriceInUsd string `json:"base_token_high_price_in_usd"`
	BaseTokenLowPriceInUsd  string `json:"base_token_low_price_in_usd"`
}

func getPrice(address string, client *ethclient.Client) (*PriceSummary, error) {
	url := fmt.Sprintf("https://app.geckoterminal.com/api/p1/eth/pools/%s?include=pairs&base_token=0", address)

	// Create HTTP request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referrer", "https://www.geckoterminal.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:124.0) Gecko/20100101 Firefox/124.0")

	// Make HTTP request
	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
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
	var geckoResp GeckoResponse
	if err := json.Unmarshal(body, &geckoResp); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	if len(geckoResp.Included) == 0 {
		return nil, fmt.Errorf("no price details found for pool: %s", address)
	}

	attributes := geckoResp.Included[0].Attributes

	if attributes.BaseAddress == "" {
		return nil, fmt.Errorf("no price details found for pool: %s", address)
	}

	// Get token contract info
	contractAddress := common.HexToAddress(attributes.BaseAddress)

	// Parse ABI
	parsedABI, err := abi.JSON(strings.NewReader(ERC20_ABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ABI: %w", err)
	}

	// Create contract instance
	contract := bind.NewBoundContract(contractAddress, parsedABI, client, client, client)

	// Call totalSupply and decimals
	var totalSupply *big.Int
	var decimals uint8

	tS := []interface{}{&totalSupply}
	err = contract.Call(&bind.CallOpts{}, &tS, "totalSupply")
	if err != nil {
		return nil, fmt.Errorf("failed to get total supply: %w", err)
	}

	dC := []interface{}{&decimals}
	err = contract.Call(&bind.CallOpts{}, &dC, "decimals")
	if err != nil {
		return nil, fmt.Errorf("failed to get decimals: %w", err)
	}

	// Calculate parsed supply
	divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	parsedSupplyFloat := new(big.Float).Quo(new(big.Float).SetInt(totalSupply), new(big.Float).SetInt(divisor))
	parsedSupply, _ := parsedSupplyFloat.Float64()

	// Parse price
	price, err := strconv.ParseFloat(attributes.BasePriceInUsd, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse price: %w", err)
	}

	// Calculate market cap
	mcap := int64(math.Floor(price * parsedSupply))

	// Parse price change percentage
	priceChangePercent, err := strconv.ParseFloat(attributes.BasePriceInUsdPercentChange, 64)
	if err != nil {
		priceChangePercent = 0
	}

	// Parse price changes
	last30, err := strconv.ParseFloat(attributes.PriceChangeData.Last1800s.BaseTokenUsd, 64)
	if err != nil {
		last30 = 0
	}

	last15, err := strconv.ParseFloat(attributes.PriceChangeData.Last900s.BaseTokenUsd, 64)
	if err != nil {
		last15 = 0
	}

	last5, err := strconv.ParseFloat(attributes.PriceChangeData.Last300s.BaseTokenUsd, 64)
	if err != nil {
		last5 = 0
	}

	// Parse highest and lowest prices
	highestPrice, err := strconv.ParseFloat(attributes.PriceChangeData.Last86400s.Prices.BaseTokenHighPriceInUsd, 64)
	if err != nil {
		highestPrice = 0
	}

	lowestPrice, err := strconv.ParseFloat(attributes.PriceChangeData.Last86400s.Prices.BaseTokenLowPriceInUsd, 64)
	if err != nil {
		lowestPrice = 0
	}

	summary := &PriceSummary{
		Price:   fmt.Sprintf("%.9f", price),
		Mcap:    mcap,
		Swap24h: attributes.SwapCount,
		PriceChange: PriceChange{
			Total:  int64(math.Floor(priceChangePercent)),
			Last30: fmt.Sprintf("%.9f", last30),
			Last15: fmt.Sprintf("%.9f", last15),
			Last5:  fmt.Sprintf("%.9f", last5),
		},
		HighestPrice: fmt.Sprintf("%.9f", highestPrice),
		LowestPrice:  fmt.Sprintf("%.9f", lowestPrice),
	}

	return summary, nil
}
