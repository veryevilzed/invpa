package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/veryevilzed/invpa/invoice"

	"github.com/sashabaranov/go-openai"
	"github.com/schollz/progressbar/v3"
	"github.com/xuri/excelize/v2"
)

// Result структура для хранения результата обработки одного файла
type Result struct {
	SourceFile   string
	Invoice      *invoice.Invoice
	ErrorMessage string
}

// UniqueCounterparty структура для хранения уникального контрагента
type UniqueCounterparty struct {
	SourceFile   string // Файл, где контрагент был впервые обнаружен
	Counterparty invoice.Counterparty
}

func main() {
	// 1. Загрузка конфигурации
	config, err := loadConfig("config.json")
	if err != nil {
		log.Fatalf("FATAL: Could not load config.json. Make sure it exists and is configured. Error: %v", err)
	}
	if config.OpenAPIKey == "" {
		log.Fatalf("FATAL: 'openai_api_key' is not set in config.json.")
	}

	// 2. Сканирование файлов в текущей директории
	files, err := findInvoiceFiles(".")
	if err != nil {
		log.Fatalf("FATAL: Error scanning for files: %v", err)
	}

	if len(files) == 0 {
		fmt.Println("No invoice files (.pdf, .png, .jpg, .jpeg) found in the current directory.")
		return
	}

	fmt.Printf("Found %d files to process. Starting analysis...\n", len(files))

	// 3. Настройка OpenAI клиента и прогресс-бара
	client := openai.NewClient(config.OpenAPIKey)
	bar := progressbar.NewOptions(len(files),
		progressbar.OptionSetDescription("Processing invoices"),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)

	// 4. Параллельная обработка файлов
	resultsChan := make(chan Result, len(files))
	var wg sync.WaitGroup

	for _, file := range files {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()
			defer bar.Add(1)

			invoices, err := invoice.ProcessFile(f, config.OpenAPIKey, config.PopplerPathWindows, config.MyCompany)
			if err != nil {
				resultsChan <- Result{SourceFile: f, ErrorMessage: err.Error()}
				return
			}
			// Если в одном файле несколько инвойсов, берем первый (для упрощения отчета)
			if len(invoices) > 0 {
				resultsChan <- Result{SourceFile: f, Invoice: &invoices[0]}
			} else {
				resultsChan <- Result{SourceFile: f, ErrorMessage: "No invoices found in file"}
			}
		}(file)
	}

	wg.Wait()
	close(resultsChan)
	fmt.Println("\nAnalysis complete. Deduplicating counterparties and generating report...")

	// 5. Сбор и обработка результатов
	var allResults []Result
	var uniqueCounterparties []UniqueCounterparty
	var existingForSearch []invoice.Counterparty
	var successfulCount, errorCount int

	for res := range resultsChan {
		allResults = append(allResults, res)
		if res.ErrorMessage != "" {
			errorCount++
			continue // Пропускаем дедупликацию для ошибочных результатов
		}
		successfulCount++

		// Логика дедупликации только для успешных результатов
		matched, err := invoice.FindCounterparty(client, existingForSearch, res.Invoice.Counterparty)
		if err != nil {
			log.Printf("WARN: Could not match counterparty for %s: %v", res.SourceFile, err)
			// ID будет 0 (zero-value), что означает "новый"
			uniqueCounterparties = append(uniqueCounterparties, UniqueCounterparty{
				SourceFile:   res.SourceFile,
				Counterparty: res.Invoice.Counterparty,
			})
			existingForSearch = append(existingForSearch, res.Invoice.Counterparty)
		} else if matched != nil {
			// Нашли совпадение, используем его ID и обновленные данные
			res.Invoice.Counterparty = *matched
		} else {
			// ID будет 0 (zero-value), что означает "новый"
			uniqueCounterparties = append(uniqueCounterparties, UniqueCounterparty{
				SourceFile:   res.SourceFile,
				Counterparty: res.Invoice.Counterparty,
			})
			existingForSearch = append(existingForSearch, res.Invoice.Counterparty)
		}
	}

	// 6. Генерация Excel файла
	err = generateExcelReport(allResults, uniqueCounterparties)
	if err != nil {
		log.Fatalf("FATAL: Failed to generate Excel report: %v", err)
	}

	fmt.Printf("\nSuccessfully generated report '__RESULT.xlsx' with:\n")
	fmt.Printf("- %d successfully processed invoices\n", successfulCount)
	fmt.Printf("- %d unique counterparties found\n", len(uniqueCounterparties))
	fmt.Printf("- %d files with errors\n", errorCount)
}

func loadConfig(path string) (*invoice.Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var config invoice.Config
	decoder := json.NewDecoder(file)
	err = decoder.Decode(&config)
	return &config, err
}

func findInvoiceFiles(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".pdf" || ext == ".png" || ext == ".jpg" || ext == ".jpeg" {
				// Игнорируем вложенные директории, кроме текущей
				if filepath.Dir(path) == "." {
					files = append(files, path)
				}
			}
		}
		return nil
	})
	return files, err
}

func generateExcelReport(allResults []Result, counterparties []UniqueCounterparty) error {
	f := excelize.NewFile()
	defer f.Close()

	// --- Лист "Invoices" ---
	f.NewSheet("Invoices")
	f.DeleteSheet("Sheet1") // Удаляем лист по умолчанию
	headers := []string{
		"Source File", "Status", "Counterparty ID", "Counterparty Name", "Counterparty VAT", "Counterparty Country",
		"Invoice Number", "Date", "Total Amount", "Tax Amount", "Purpose",
	}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("Invoices", cell, h)
	}
	for i, res := range allResults {
		row := i + 2
		f.SetCellValue("Invoices", fmt.Sprintf("A%d", row), res.SourceFile)

		if res.ErrorMessage != "" {
			f.SetCellValue("Invoices", fmt.Sprintf("B%d", row), res.ErrorMessage)
			// Устанавливаем красный цвет для ячейки со статусом ошибки
			style, _ := f.NewStyle(&excelize.Style{
				Font: &excelize.Font{Color: "9A0511"},
			})
			f.SetCellStyle("Invoices", fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), style)
		} else {
			f.SetCellValue("Invoices", fmt.Sprintf("B%d", row), "OK")
			cp := res.Invoice.Counterparty
			f.SetCellValue("Invoices", fmt.Sprintf("C%d", row), cp.ID)
			f.SetCellValue("Invoices", fmt.Sprintf("D%d", row), cp.Name)
			f.SetCellValue("Invoices", fmt.Sprintf("E%d", row), cp.VAT)
			f.SetCellValue("Invoices", fmt.Sprintf("F%d", row), cp.Country)
			f.SetCellValue("Invoices", fmt.Sprintf("G%d", row), res.Invoice.Number)
			f.SetCellValue("Invoices", fmt.Sprintf("H%d", row), res.Invoice.Date)
			f.SetCellValue("Invoices", fmt.Sprintf("I%d", row), res.Invoice.TotalAmount)
			f.SetCellValue("Invoices", fmt.Sprintf("J%d", row), res.Invoice.TaxAmount)
			f.SetCellValue("Invoices", fmt.Sprintf("K%d", row), res.Invoice.Purpose)
		}
	}

	// --- Лист "Counterparties" ---
	f.NewSheet("Counterparties")
	cpHeaders := []string{"Source File", "ID", "Name", "VAT", "Country", "Address", "IBAN", "SWIFT", "Phone", "Email", "Website"}
	for i, h := range cpHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("Counterparties", cell, h)
	}
	for i, ucp := range counterparties {
		row := i + 2
		cp := ucp.Counterparty
		f.SetCellValue("Counterparties", fmt.Sprintf("A%d", row), ucp.SourceFile)
		f.SetCellValue("Counterparties", fmt.Sprintf("B%d", row), cp.ID)
		f.SetCellValue("Counterparties", fmt.Sprintf("C%d", row), cp.Name)
		f.SetCellValue("Counterparties", fmt.Sprintf("D%d", row), cp.VAT)
		f.SetCellValue("Counterparties", fmt.Sprintf("E%d", row), cp.Country)
		f.SetCellValue("Counterparties", fmt.Sprintf("F%d", row), cp.Address)
		f.SetCellValue("Counterparties", fmt.Sprintf("G%d", row), cp.IBAN)
		f.SetCellValue("Counterparties", fmt.Sprintf("H%d", row), cp.SWIFT)
		f.SetCellValue("Counterparties", fmt.Sprintf("I%d", row), cp.Phone)
		f.SetCellValue("Counterparties", fmt.Sprintf("J%d", row), cp.Email)
		f.SetCellValue("Counterparties", fmt.Sprintf("K%d", row), cp.Website)
	}

	return f.SaveAs("__RESULT.xlsx")
}
