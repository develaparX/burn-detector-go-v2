package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Configuration constants
const (
	
)

// ERC20 ABI definitions
const ERC20_ABI = `[
	{
		"constant": true,
		"inputs": [],
		"name": "totalSupply",
		"outputs": [{"name": "", "type": "uint256"}],
		"type": "function"
	},
	{
		"constant": true,
		"inputs": [{"name": "_owner", "type": "address"}],
		"name": "balanceOf",
		"outputs": [{"name": "balance", "type": "uint256"}],
		"type": "function"
	},
	{
		"constant": true,
		"inputs": [],
		"name": "token0",
		"outputs": [{"name": "", "type": "address"}],
		"type": "function"
	},
	{
		"constant": true,
		"inputs": [],
		"name": "token1",
		"outputs": [{"name": "", "type": "address"}],
		"type": "function"
	},
	{
		"constant": true,
		"inputs": [],
		"name": "name",
		"outputs": [{"name": "", "type": "string"}],
		"type": "function"
	},
	{
		"constant": true,
		"inputs": [],
		"name": "decimals",
		"outputs": [{"name": "", "type": "uint8"}],
		"type": "function"
	},
	{
		"constant": true,
		"inputs": [],
		"name": "symbol",
		"outputs": [{"name": "", "type": "string"}],
		"type": "function"
	},
	{
		"constant": false,
		"inputs": [
			{"name": "_to", "type": "address"},
			{"name": "_value", "type": "uint256"}
		],
		"name": "transfer",
		"outputs": [{"name": "", "type": "bool"}],
		"type": "function"
	}
]`

// Struct definitions
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

type PriceData struct {
	Price       string      `json:"price"`
	Mcap        int64       `json:"mcap"`
	Swap24h     interface{} `json:"swap_24h"`
	PriceChange struct {
		Total  int64  `json:"total"`
		Last30 string `json:"last_30"`
		Last15 string `json:"last_15"`
		Last5  string `json:"last_5"`
	} `json:"price_change"`
	HighestPrice string `json:"highest_price"`
	LowestPrice  string `json:"lowest_price"`
}

type GeckoTerminalResponse struct {
	Data struct {
		Attributes struct {
			BasePriceInUsd              string `json:"base_price_in_usd"`
			BaseAddress                 string `json:"base_address"`
			SwapCount                   int    `json:"swap_count"`
			BasePriceInUsdPercentChange string `json:"base_price_in_usd_percent_change"`
			PriceChangeData             struct {
				Last300s struct {
					BaseTokenUsd string `json:"base_token_usd"`
				} `json:"last_300_s"`
				Last900s struct {
					BaseTokenUsd string `json:"base_token_usd"`
				} `json:"last_900_s"`
				Last1800s struct {
					BaseTokenUsd string `json:"base_token_usd"`
				} `json:"last_1800_s"`
				Last86400s struct {
					Prices struct {
						BaseTokenHighPriceInUsd string `json:"base_token_high_price_in_usd"`
						BaseTokenLowPriceInUsd  string `json:"base_token_low_price_in_usd"`
					} `json:"prices"`
				} `json:"last_86400_s"`
			} `json:"price_change_data"`
		} `json:"attributes"`
	} `json:"data"`
	Included []struct {
		Attributes struct {
			BasePriceInUsd              string `json:"base_price_in_usd"`
			BaseAddress                 string `json:"base_address"`
			SwapCount                   int    `json:"swap_count"`
			BasePriceInUsdPercentChange string `json:"base_price_in_usd_percent_change"`
			PriceChangeData             struct {
				Last300s struct {
					BaseTokenUsd string `json:"base_token_usd"`
				} `json:"last_300_s"`
				Last900s struct {
					BaseTokenUsd string `json:"base_token_usd"`
				} `json:"last_900_s"`
				Last1800s struct {
					BaseTokenUsd string `json:"base_token_usd"`
				} `json:"last_1800_s"`
				Last86400s struct {
					Prices struct {
						BaseTokenHighPriceInUsd string `json:"base_token_high_price_in_usd"`
						BaseTokenLowPriceInUsd  string `json:"base_token_low_price_in_usd"`
					} `json:"prices"`
				} `json:"last_86400_s"`
			} `json:"price_change_data"`
		} `json:"attributes"`
	} `json:"included"`
}

type GoPlusResponse struct {
	Result map[string]TokenDetails `json:"result"`
}

type LPBurnDetector struct {
	client      *ethclient.Client
	contractABI abi.ABI
	httpClient  *http.Client
}

func NewLPBurnDetector() (*LPBurnDetector, error) {
	client, err := ethclient.Dial(NODE_URL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Ethereum client: %v", err)
	}

	contractABI, err := abi.JSON(strings.NewReader(ERC20_ABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ABI: %v", err)
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	return &LPBurnDetector{
		client:      client,
		contractABI: contractABI,
		httpClient:  httpClient,
	}, nil
}

func (d *LPBurnDetector) getTokenDetails(address string) (*TokenDetails, error) {
	reqURL := fmt.Sprintf("https://api.gopluslabs.io/api/v1/token_security/1?contract_addresses=%s", address)

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "*/*")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result GoPlusResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	for _, details := range result.Result {
		return &details, nil
	}

	return nil, fmt.Errorf("no token details found")
}

func (d *LPBurnDetector) getPriceData(address string) (*PriceData, error) {
	reqURL := fmt.Sprintf("https://app.geckoterminal.com/api/p1/eth/pools/%s?include=pairs&base_token=0", address)

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	// Add headers similar to the original
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referrer", "https://www.geckoterminal.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:124.0) Gecko/20100101 Firefox/124.0")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result GeckoTerminalResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	if len(result.Included) == 0 {
		return nil, fmt.Errorf("no price data found")
	}

	attr := result.Included[0].Attributes

	// Get token contract and supply for mcap calculation
	tokenContract := common.HexToAddress(attr.BaseAddress)
	supply, err := d.getTokenSupply(tokenContract)
	if err != nil {
		return nil, err
	}

	decimals, err := d.getTokenDecimals(tokenContract)
	if err != nil {
		return nil, err
	}

	// Calculate market cap
	priceFloat, _ := strconv.ParseFloat(attr.BasePriceInUsd, 64)
	supplyFloat := new(big.Float).SetInt(supply)
	decimalsInt := big.NewInt(int64(decimals))
	tenInt := big.NewInt(10)
	divisorInt := new(big.Int).Exp(tenInt, decimalsInt, nil)
	divisor := new(big.Float).SetInt(divisorInt)
	parsedSupply := new(big.Float).Quo(supplyFloat, divisor)

	mcapFloat := new(big.Float).Mul(big.NewFloat(priceFloat), parsedSupply)
	mcap, _ := mcapFloat.Int64()

	// Parse price change
	priceChange, _ := strconv.ParseFloat(attr.BasePriceInUsdPercentChange, 64)

	return &PriceData{
		Price:   fmt.Sprintf("%.9f", priceFloat),
		Mcap:    mcap,
		Swap24h: attr.SwapCount,
		PriceChange: struct {
			Total  int64  `json:"total"`
			Last30 string `json:"last_30"`
			Last15 string `json:"last_15"`
			Last5  string `json:"last_5"`
		}{
			Total:  int64(priceChange),
			Last30: attr.PriceChangeData.Last1800s.BaseTokenUsd,
			Last15: attr.PriceChangeData.Last900s.BaseTokenUsd,
			Last5:  attr.PriceChangeData.Last300s.BaseTokenUsd,
		},
		HighestPrice: attr.PriceChangeData.Last86400s.Prices.BaseTokenHighPriceInUsd,
		LowestPrice:  attr.PriceChangeData.Last86400s.Prices.BaseTokenLowPriceInUsd,
	}, nil
}

func (d *LPBurnDetector) getTokenSupply(tokenAddress common.Address) (*big.Int, error) {
	data, err := d.contractABI.Pack("totalSupply")
	if err != nil {
		return nil, err
	}

	result, err := d.client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &tokenAddress,
		Data: data,
	}, nil)
	if err != nil {
		return nil, err
	}

	var supply *big.Int
	err = d.contractABI.UnpackIntoInterface(&supply, "totalSupply", result)
	if err != nil {
		return nil, err
	}

	return supply, nil
}

func (d *LPBurnDetector) getTokenDecimals(tokenAddress common.Address) (uint8, error) {
	data, err := d.contractABI.Pack("decimals")
	if err != nil {
		return 0, err
	}

	result, err := d.client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &tokenAddress,
		Data: data,
	}, nil)
	if err != nil {
		return 0, err
	}

	var decimals uint8
	err = d.contractABI.UnpackIntoInterface(&decimals, "decimals", result)
	if err != nil {
		return 0, err
	}

	return decimals, nil
}

func (d *LPBurnDetector) getTokenName(tokenAddress common.Address) (string, error) {
	data, err := d.contractABI.Pack("name")
	if err != nil {
		return "", err
	}

	result, err := d.client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &tokenAddress,
		Data: data,
	}, nil)
	if err != nil {
		return "", err
	}

	var name string
	err = d.contractABI.UnpackIntoInterface(&name, "name", result)
	if err != nil {
		return "", err
	}

	return name, nil
}

func (d *LPBurnDetector) getToken0(lpAddress common.Address) (common.Address, error) {
	data, err := d.contractABI.Pack("token0")
	if err != nil {
		return common.Address{}, err
	}

	result, err := d.client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &lpAddress,
		Data: data,
	}, nil)
	if err != nil {
		return common.Address{}, err
	}

	var token0 common.Address
	err = d.contractABI.UnpackIntoInterface(&token0, "token0", result)
	if err != nil {
		return common.Address{}, err
	}

	return token0, nil
}

func (d *LPBurnDetector) getToken1(lpAddress common.Address) (common.Address, error) {
	data, err := d.contractABI.Pack("token1")
	if err != nil {
		return common.Address{}, err
	}

	result, err := d.client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &lpAddress,
		Data: data,
	}, nil)
	if err != nil {
		return common.Address{}, err
	}

	var token1 common.Address
	err = d.contractABI.UnpackIntoInterface(&token1, "token1", result)
	if err != nil {
		return common.Address{}, err
	}

	return token1, nil
}

func (d *LPBurnDetector) getTokenBalance(tokenAddress, holderAddress common.Address) (*big.Int, error) {
	data, err := d.contractABI.Pack("balanceOf", holderAddress)
	if err != nil {
		return nil, err
	}

	result, err := d.client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &tokenAddress,
		Data: data,
	}, nil)
	if err != nil {
		return nil, err
	}

	var balance *big.Int
	err = d.contractABI.UnpackIntoInterface(&balance, "balanceOf", result)
	if err != nil {
		return nil, err
	}

	return balance, nil
}

func (d *LPBurnDetector) sendTelegramMessage(message string) error {
	telegramURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", BOT_TOKEN)

	data := url.Values{}
	data.Set("chat_id", CHAT_ID)
	data.Set("text", message)
	data.Set("parse_mode", "HTML")
	data.Set("disable_web_page_preview", "true")

	resp, err := d.httpClient.PostForm(telegramURL, data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram API error: %s", string(body))
	}

	return nil
}

func (d *LPBurnDetector) processLPBurn(txHash common.Hash) error {
	// Get transaction details
	tx, isPending, err := d.client.TransactionByHash(context.Background(), txHash)
	if err != nil {
		return fmt.Errorf("failed to get transaction: %v", err)
	}

	if isPending {
		return fmt.Errorf("transaction is still pending")
	}

	// Check if it's a transfer function call (a9059cbb)
	if len(tx.Data()) < 4 {
		return fmt.Errorf("transaction data too short")
	}

	functionSelector := hex.EncodeToString(tx.Data()[:4])
	if functionSelector != "a9059cbb" {
		return fmt.Errorf("not a transfer function call: %s", functionSelector)
	}

	lpAddress := *tx.To()

	// Get LP token name to verify it's a Uniswap LP
	lpName, err := d.getTokenName(lpAddress)
	if err != nil {
		return fmt.Errorf("failed to get LP name: %v", err)
	}

	if !strings.Contains(lpName, "Uniswap") {
		return fmt.Errorf("not a Uniswap LP: %s", lpName)
	}

	// Decode transfer function data
	var to common.Address
	var value *big.Int

	err = d.contractABI.UnpackIntoInterface(&[]interface{}{&to, &value}, "transfer", tx.Data()[4:])
	if err != nil {
		return fmt.Errorf("failed to decode transfer data: %v", err)
	}

	// Check if tokens are being sent to dead address
	if strings.ToLower(to.Hex()) != DEAD_ADDR {
		return fmt.Errorf("tokens not sent to dead address: %s", to.Hex())
	}

	// Get LP token supply
	lpSupply, err := d.getTokenSupply(lpAddress)
	if err != nil {
		return fmt.Errorf("failed to get LP supply: %v", err)
	}

	// Calculate burn percentage
	burnedFloat := new(big.Float).SetInt(value)
	supplyFloat := new(big.Float).SetInt(lpSupply)
	eighteenDecimals := new(big.Float).SetInt(big.NewInt(1000000000000000000)) // 10^18

	burnedLP := new(big.Float).Quo(burnedFloat, eighteenDecimals)
	parsedSupply := new(big.Float).Quo(supplyFloat, eighteenDecimals)

	percentage := new(big.Float).Quo(parsedSupply, burnedLP)
	percentage.Mul(percentage, big.NewFloat(100))

	// Get token addresses from LP
	token0, err := d.getToken0(lpAddress)
	if err != nil {
		return fmt.Errorf("failed to get token0: %v", err)
	}

	token1, err := d.getToken1(lpAddress)
	if err != nil {
		return fmt.Errorf("failed to get token1: %v", err)
	}

	// Determine which token is not WETH
	var tokenContract common.Address
	if strings.ToLower(token0.Hex()) == WETH_ADDR {
		tokenContract = token1
	} else {
		tokenContract = token0
	}

	// Get token details
	details, err := d.getTokenDetails(tokenContract.Hex())
	if err != nil {
		log.Printf("Failed to get token details: %v", err)
		details = &TokenDetails{
			TokenName:   "Unknown",
			TokenSymbol: "UNK",
			IsHoneypot:  "undefined",
			BuyTax:      "0",
			SellTax:     "0",
			HolderCount: "0",
			Holders:     []Holder{},
		}
	}

	// Get price data
	priceData, err := d.getPriceData(lpAddress.Hex())
	if err != nil {
		log.Printf("Failed to get price data: %v", err)
		priceData = &PriceData{
			Price: "0",
			Mcap:  0,
		}
	}

	// Get token supply and balance
	tokenSupply, err := d.getTokenSupply(tokenContract)
	if err != nil {
		log.Printf("Failed to get token supply: %v", err)
		tokenSupply = big.NewInt(0)
	}

	tokenDecimals, err := d.getTokenDecimals(tokenContract)
	if err != nil {
		log.Printf("Failed to get token decimals: %v", err)
		tokenDecimals = 18
	}

	tokenBalance, err := d.getTokenBalance(tokenContract, tokenContract)
	if err != nil {
		log.Printf("Failed to get token balance: %v", err)
		tokenBalance = big.NewInt(0)
	}

	// Calculate clogged percentage
	decimalsInt := big.NewInt(int64(tokenDecimals))
	tenInt := big.NewInt(10)
	divisorInt := new(big.Int).Exp(tenInt, decimalsInt, nil)
	divisor := new(big.Float).SetInt(divisorInt)

	parsedTokenSupply := new(big.Float).Quo(new(big.Float).SetInt(tokenSupply), divisor)
	tokenHolding := new(big.Float).Quo(new(big.Float).SetInt(tokenBalance), divisor)

	cloggedPercentage := new(big.Float).Quo(tokenHolding, parsedTokenSupply)
	cloggedPercentage.Mul(cloggedPercentage, big.NewFloat(100))

	// Format burned LP value
	burnedFormatted, _ := burnedLP.Float64()
	percentageFormatted, _ := percentage.Float64()
	cloggedFormatted, _ := tokenHolding.Float64()
	cloggedPercentageFormatted, _ := cloggedPercentage.Float64()

	// Create message
	honeypotStatus := "Unknown 🟨"
	if details.IsHoneypot == "0" {
		honeypotStatus = "False 🟩"
	} else if details.IsHoneypot == "1" {
		honeypotStatus = "True 🟥"
	}

	// Format buy/sell tax
	buyTax := "Unknown 🟨"
	if details.BuyTax != "" && details.BuyTax != "0" {
		if tax, err := strconv.ParseFloat(details.BuyTax, 64); err == nil {
			buyTax = fmt.Sprintf("%.1f%%", tax*100)
		}
	}

	sellTax := "Unknown 🟨"
	if details.SellTax != "" && details.SellTax != "0" {
		if tax, err := strconv.ParseFloat(details.SellTax, 64); err == nil {
			sellTax = fmt.Sprintf("%.1f%%", tax*100)
		}
	}

	// Format top holders
	topHolders := "N/A"
	if len(details.Holders) > 0 {
		var holderStrings []string
		for i, holder := range details.Holders {
			if i >= 2 { // Only show top 2
				break
			}
			percent, _ := strconv.ParseFloat(holder.Percent, 64)
			holderStrings = append(holderStrings, fmt.Sprintf("<a href=\"https://etherscan.io/address/%s\">%.4f%%</a>", holder.Address, percent))
		}
		topHolders = strings.Join(holderStrings, "|")
	}

	message := fmt.Sprintf(`🔥🔥New LP Burn Detected🔥🔥
<a href="https://etherscan.io/address/%s">%s</a><b>(%s)</b>
<code>%s</code>

💰<b>Mcap:</b> $%s
        <b>⎿ Hash:</b> <a href="https://etherscan.io/tx/%s">Click Here</a>
        <b>⎿ Burned:</b> %.1f(%.2f%%)

🔵 Honeypot : %s
        <b>⎿ Buy Tax:</b> %s
        <b>⎿ Sell Tax:</b> %s
        <b>⎿ Clogged:</b> %s (%.1f%%)

👤 Current Holders Count: %s
        <b>⎿ Top Holders:</b> %s

<b>Chart:</b> <a href="https://www.dextools.io/app/en/ether/pair-explorer/%s">DexTools</a> | <a href="https://dexscreener.com/ethereum/%s">DexScreener</a> | <a href="https://dexspy.io/eth/token/%s">DexSpy</a>
<b>Snipe:</b> <a href="https://t.me/MaestroSniperBot?start=%s">Maestro</a> (<a href="https://t.me/MaestroProBot?start=%s">Pro</a>) | <a href="https://t.me/BananaGunSniper_bot?start=snp_Atasya_%s">Banana</a>
<b>More Tools:</b> <a href="https://t.me/GenApes">100x at GenApes</a>`,
		tokenContract.Hex(), details.TokenName, details.TokenSymbol, tokenContract.Hex(),
		formatNumber(priceData.Mcap), txHash.Hex(), burnedFormatted, percentageFormatted,
		honeypotStatus, buyTax, sellTax, formatNumber(int64(cloggedFormatted)), cloggedPercentageFormatted,
		details.HolderCount, topHolders,
		tokenContract.Hex(), tokenContract.Hex(), tokenContract.Hex(),
		tokenContract.Hex(), tokenContract.Hex(), tokenContract.Hex())

	return d.sendTelegramMessage(message)
}

func formatNumber(num int64) string {
	str := strconv.FormatInt(num, 10)
	n := len(str)
	if n <= 3 {
		return str
	}

	var result strings.Builder
	for i, digit := range str {
		if i > 0 && (n-i)%3 == 0 {
			result.WriteString(",")
		}
		result.WriteRune(digit)
	}
	return result.String()
}

func (d *LPBurnDetector) watchLogs() {
	// Create transfer event filter for dead address
	transferTopic := crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	deadAddress := common.HexToAddress(DEAD_ADDR)

	query := ethereum.FilterQuery{
		Topics: [][]common.Hash{
			{transferTopic},
			{}, // from (any address)
			{common.BytesToHash(deadAddress.Bytes())}, // to (dead address)
		},
	}

	logs := make(chan types.Log)
	sub, err := d.client.SubscribeFilterLogs(context.Background(), query, logs)
	if err != nil {
		log.Fatalf("Failed to subscribe to logs: %v", err)
	}

	log.Println("🔍 Starting LP burn detector...")
	log.Println("📡 Listening for transfer events to dead address...")

	for {
		select {
		case err := <-sub.Err():
			log.Printf("❌ Subscription error: %v", err)
			return
		case vLog := <-logs:
			// Log the current block being scanned
			log.Printf("🔍 Scanning block %d for LP burns...", vLog.BlockNumber)

			log.Printf("📝 Found transfer to dead address in tx: %s", vLog.TxHash.Hex())

			err := d.processLPBurn(vLog.TxHash)
			if err != nil {
				log.Printf("❌ Not an LP burn: %v", err)
			} else {
				log.Printf("🔥 LP burn detected and message sent!")
			}
		}
	}
}

func main() {
	detector, err := NewLPBurnDetector()
	if err != nil {
		log.Fatalf("Failed to create LP burn detector: %v", err)
	}

	log.Println("🚀 LP Burn Detector started")
	log.Println("🔗 Connected to Ethereum node")
	log.Println("📱 Telegram bot configured")

	detector.watchLogs()
}
