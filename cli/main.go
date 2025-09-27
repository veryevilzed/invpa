package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/veryevilzed/invpa/invoice"
)

// Config определяет структуру файла конфигурации.
type Config struct {
	OpenAIAPIKey       string               `json:"openai_api_key"`
	MyCompany          invoice.Counterparty `json:"my_company"`
	PopplerPathWindows string               `json:"poppler_path_windows,omitempty"`
}

func main() {
	// 1. Проверка аргументов командной строки
	if len(os.Args) < 2 {
		log.Fatalf("Usage: %s <path_to_invoice_file>", os.Args[0])
	}
	filePath := os.Args[1]

	// 2. Загрузка конфигурации
	config, err := loadConfig("config.json")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 3. Вызов анализатора
	fmt.Printf("Analyzing file: %s\n", filePath)
	invoices, err := invoice.ProcessFile(filePath, config.OpenAIAPIKey, config.PopplerPathWindows, config.MyCompany)
	if err != nil {
		log.Fatalf("Failed to process invoice: %v", err)
	}

	// 4. Вывод результата
	if len(invoices) == 0 {
		fmt.Println("No invoices found.")
		return
	}

	resultJSON, err := json.MarshalIndent(invoices, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal result to JSON: %v", err)
	}
	fmt.Println(string(resultJSON))
}

// loadConfig загружает конфигурацию из файла.
func loadConfig(path string) (*Config, error) {
	file, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read config file: %w", err)
	}

	var config Config
	if err := json.Unmarshal(file, &config); err != nil {
		return nil, fmt.Errorf("could not unmarshal config json: %w", err)
	}

	if config.OpenAIAPIKey == "" || config.OpenAIAPIKey == "YOUR_OPENAI_API_KEY" {
		return nil, fmt.Errorf("OpenAI API key is not set in %s", path)
	}

	return &config, nil
}
