package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	TRANSFER_TOPIC = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	DEAD_ADDRESS   = "0x000000000000000000000000000000000000dead"
	WETH_ADDRESS   = "0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2"
	TRANSFER_SIG   = "a9059cbb"
)

func main() {
	// Connect to Ethereum client
	client, err := ethclient.Dial(NODE_URL)
	if err != nil {
		log.Fatalf("Failed to connect to Ethereum client: %v", err)
	}
	defer client.Close()

	// Create Telegram bot
	bot, err := tgbotapi.NewBotAPI(BOT_TOKEN)
	if err != nil {
		log.Fatalf("Failed to create Telegram bot: %v", err)
	}

	log.Println("Bot started, checking for LP burns...")

	// Set up event filter for Transfer events to dead address
	query := ethereum.FilterQuery{
		Topics: [][]common.Hash{
			{common.HexToHash(TRANSFER_TOPIC)},
			nil,
			{common.HexToHash("0x000000000000000000000000000000000000000000000000000000000000dead")},
		},
	}

	// Subscribe to logs
	logs := make(chan types.Log)
	sub, err := client.SubscribeFilterLogs(context.Background(), query, logs)
	if err != nil {
		log.Fatalf("Failed to subscribe to logs: %v", err)
	}
	defer sub.Unsubscribe()

	// Process logs
	for {
		select {
		case err := <-sub.Err():
			log.Printf("Subscription error: %v", err)
			return
		case vLog := <-logs:
			err := processLog(vLog, client, bot)
			if err != nil {
				log.Printf("Error processing log: %v", err)
			}
		}
	}
}

func processLog(vLog types.Log, client *ethclient.Client, bot *tgbotapi.BotAPI) error {
	// Get transaction
	tx, _, err := client.TransactionByHash(context.Background(), vLog.TxHash)
	if err != nil {
		return fmt.Errorf("failed to get transaction: %w", err)
	}

	// Check if transaction data starts with transfer function signature
	if len(tx.Data()) < 10 {
		log.Printf("Transaction data too short: %s", vLog.TxHash.Hex())
		return nil
	}

	txDataSlice := fmt.Sprintf("%x", tx.Data()[0:4])
	matched, _ := regexp.MatchString(TRANSFER_SIG, txDataSlice)
	if !matched {
		log.Printf("Not a burn transaction: %s", vLog.TxHash.Hex())
		return nil
	}

	// Parse LP ABI
	lpABI, err := abi.JSON(strings.NewReader(LP_ABI))
	if err != nil {
		return fmt.Errorf("failed to parse LP ABI: %w", err)
	}

	// Create LP contract instance
	lpContract := bind.NewBoundContract(*tx.To(), lpABI, client, client, client)

	// Get LP contract info
	var totalSupply *big.Int
	var name string
	var token0 common.Address
	var token1 common.Address

	{
		out := []interface{}{&totalSupply}
		err = lpContract.Call(&bind.CallOpts{}, &out, "totalSupply")
		if err != nil {
			log.Printf("Invalid LP Address: %s", vLog.TxHash.Hex())
			return nil
		}
	}

	{
		out := []interface{}{&name}
		err = lpContract.Call(&bind.CallOpts{}, &out, "name")
		if err != nil {
			log.Printf("Failed to get LP name: %s", vLog.TxHash.Hex())
			return nil
		}
	}

	{
		out := []interface{}{&token0}
		err = lpContract.Call(&bind.CallOpts{}, &out, "token0")
		if err != nil {
			log.Printf("Failed to get token0: %s", vLog.TxHash.Hex())
			return nil
		}
	}

	{
		out := []interface{}{&token1}
		err = lpContract.Call(&bind.CallOpts{}, &out, "token1")
		if err != nil {
			log.Printf("Failed to get token1: %s", vLog.TxHash.Hex())
			return nil
		}
	}

	// Check if it's a Uniswap LP
	if !strings.Contains(name, "Uniswap") {
		log.Printf("Not an LP Burn: %s", vLog.TxHash.Hex())
		return nil
	}

	// Decode transfer function data
	method, err := lpABI.MethodById(tx.Data()[:4])
	if err != nil {
		return fmt.Errorf("failed to get method: %w", err)
	}

	inputs, err := method.Inputs.Unpack(tx.Data()[4:])
	if err != nil {
		return fmt.Errorf("failed to unpack inputs: %w", err)
	}

	if len(inputs) < 2 {
		return fmt.Errorf("insufficient inputs")
	}

	to := inputs[0].(common.Address)
	value := inputs[1].(*big.Int)

	// Check if receiver is dead address
	if !strings.EqualFold(to.Hex(), DEAD_ADDRESS) {
		log.Printf("Invalid Receiver: %s", vLog.TxHash.Hex())
		return nil
	}

	// Calculate burned LP percentage
	burnedLP := new(big.Float).Quo(new(big.Float).SetInt(value), new(big.Float).SetInt(big.NewInt(1e18)))
	parsedLPSupply := new(big.Float).Quo(new(big.Float).SetInt(totalSupply), new(big.Float).SetInt(big.NewInt(1e18)))

	percentage := new(big.Float).Quo(parsedLPSupply, burnedLP)
	percentage.Mul(percentage, big.NewFloat(100))
	percentageFloat, _ := percentage.Float64()

	// Determine token contract (not WETH)
	var tokenContract common.Address
	if strings.EqualFold(token0.Hex(), WETH_ADDRESS) {
		tokenContract = token1
	} else {
		tokenContract = token0
	}

	// Get token contract info
	tokenABI, err := abi.JSON(strings.NewReader(ERC20_ABI))
	if err != nil {
		return fmt.Errorf("failed to parse token ABI: %w", err)
	}

	tokenContractInstance := bind.NewBoundContract(tokenContract, tokenABI, client, client, client)

	var tokenSupply *big.Int
	var decimals uint8
	var balanceOf *big.Int

	tSupply := []interface{}{&tokenSupply}
	err = tokenContractInstance.Call(&bind.CallOpts{}, &tSupply, "totalSupply")
	if err != nil {
		return fmt.Errorf("failed to get token supply: %w", err)
	}

	dec := []interface{}{&decimals}
	err = tokenContractInstance.Call(&bind.CallOpts{}, &dec, "decimals")
	if err != nil {
		return fmt.Errorf("failed to get decimals: %w", err)
	}

	baOf := []interface{}{&balanceOf}
	err = tokenContractInstance.Call(&bind.CallOpts{
		From: tokenContract,
	}, &baOf, "balanceOf", tokenContract)
	if err != nil {
		return fmt.Errorf("failed to get balance: %w", err)
	}

	// Calculate token metrics
	divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	parsedTokenSupply := new(big.Float).Quo(new(big.Float).SetInt(tokenSupply), new(big.Float).SetInt(divisor))
	tokenHolding := new(big.Float).Quo(new(big.Float).SetInt(balanceOf), new(big.Float).SetInt(divisor))

	cloggedPercentage := new(big.Float).Quo(tokenHolding, parsedTokenSupply)
	cloggedPercentage.Mul(cloggedPercentage, big.NewFloat(100))

	// Get price and token details
	price, err := getPrice(tx.To().Hex(), client)
	if err != nil {
		log.Printf("Failed to get price: %v", err)
		price = &PriceSummary{Price: "0", Mcap: 0}
	}

	details, err := getDetails(tokenContract.Hex())
	if err != nil {
		log.Printf("Failed to get token details: %v", err)
		return err
	}

	// Format values
	burnedLPFloat, _ := burnedLP.Float64()
	cloggedFloat, _ := tokenHolding.Float64()
	cloggedPercentageFloat, _ := cloggedPercentage.Float64()

	// Build holder links
	var holderLinks []string
	if len(details.Holders) >= 2 {
		for i := 0; i < 2 && i < len(details.Holders); i++ {
			holder := details.Holders[i]
			percent, _ := strconv.ParseFloat(holder.Percent, 64)
			link := fmt.Sprintf(`<a href="https://etherscan.io/address/%s">%.4f%%</a>`, holder.Address, percent)
			holderLinks = append(holderLinks, link)
		}
	}

	// Format buy/sell tax
	buyTax := "Unknown 🟨"
	if details.BuyTax != "" {
		if tax, err := strconv.ParseFloat(details.BuyTax, 64); err == nil {
			buyTax = fmt.Sprintf("%.1f%%", tax*100)
		}
	}

	sellTax := "Unknown 🟨"
	if details.SellTax != "" {
		if tax, err := strconv.ParseFloat(details.SellTax, 64); err == nil {
			sellTax = fmt.Sprintf("%.1f%%", tax*100)
		}
	}

	// Format honeypot status
	honeypot := "Unknown 🟨"
	switch details.IsHoneypot {
	case "0":
		honeypot = "False 🟩"
	case "1":
		honeypot = "True 🟥"
	}

	// Format market cap
	mcapStr := "0"
	if price.Mcap > 0 {
		mcapStr = formatNumber(price.Mcap)
	}

	// Create message
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
		tokenContract.Hex(),
		details.TokenName,
		details.TokenSymbol,
		tokenContract.Hex(),
		mcapStr,
		vLog.TxHash.Hex(),
		burnedLPFloat,
		percentageFloat,
		honeypot,
		buyTax,
		sellTax,
		formatNumber(int64(cloggedFloat)),
		cloggedPercentageFloat,
		details.HolderCount,
		strings.Join(holderLinks, "|"),
		tokenContract.Hex(),
		tokenContract.Hex(),
		tokenContract.Hex(),
		tokenContract.Hex(),
		tokenContract.Hex(),
		tokenContract.Hex(),
	)

	// Send message
	msg := tgbotapi.NewMessage(CHAT_ID, message)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.DisableWebPagePreview = true

	_, err = bot.Send(msg)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	log.Printf("LP Burn detected and message sent for tx: %s", vLog.TxHash.Hex())
	return nil
}

func formatNumber(n int64) string {
	str := strconv.FormatInt(n, 10)
	if len(str) <= 3 {
		return str
	}

	var result strings.Builder
	for i, digit := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result.WriteString(",")
		}
		result.WriteRune(digit)
	}
	return result.String()
}
