package invoice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/sashabaranov/go-openai"
)

// ProcessFile анализирует файл инвойса (PDF, PNG, JPG) и извлекает данные.
// Реализует двухэтапный анализ: сначала группировка страниц, затем детальный анализ.
func ProcessFile(filePath, apiKey, popplerPath string, myCompany Counterparty) ([]Invoice, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	var imageContents [][]byte
	var err error

	// 1. Получаем изображения страниц
	switch ext {
	case ".pdf":
		fmt.Println("Converting PDF to images...")
		imageContents, err = convertPDFToImages(filePath, popplerPath)
		if err != nil {
			return nil, fmt.Errorf("failed to convert PDF to images: %w", err)
		}
	case ".png", ".jpg", ".jpeg":
		content, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read image file: %w", err)
		}
		imageContents = append(imageContents, content)
	default:
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}

	if len(imageContents) == 0 {
		return nil, fmt.Errorf("no images found to process")
	}

	client := openai.NewClient(apiKey)
	var finalInvoices []Invoice

	// 2. Группируем страницы по инвойсам
	fmt.Printf("Grouping %d pages by invoice...\n", len(imageContents))
	pageGroups, err := groupPagesByInvoice(client, imageContents)
	if err != nil {
		// Если группировка не удалась, пробуем обработать как один большой инвойс
		fmt.Printf("Page grouping failed (%v), treating all pages as a single invoice.\n", err)
		pageGroups = map[string][]int{"single_invoice": {}}
		for i := range imageContents {
			pageGroups["single_invoice"] = append(pageGroups["single_invoice"], i)
		}
	}

	// 3. Детально анализируем каждую группу
	for invoiceID, pageIndices := range pageGroups {
		fmt.Printf("Analyzing invoice '%s' with %d pages...\n", invoiceID, len(pageIndices))

		// Оптимизация: берем первые 2 и последние 2 страницы
		pagesToAnalyze := selectPagesForAnalysis(pageIndices)
		imagesToAnalyze := make([][]byte, 0, len(pagesToAnalyze))
		for _, pageIndex := range pagesToAnalyze {
			imagesToAnalyze = append(imagesToAnalyze, imageContents[pageIndex])
		}

		fmt.Printf("-> Selected %d pages for detailed analysis.\n", len(imagesToAnalyze))
		invoice, err := analyzeInvoicePages(client, imagesToAnalyze, myCompany)
		if err != nil {
			fmt.Printf("Error analyzing invoice '%s': %v\n", invoiceID, err)
			continue
		}
		finalInvoices = append(finalInvoices, *invoice)
	}

	return finalInvoices, nil
}

// groupPagesByInvoice отправляет все страницы в OpenAI для определения, к какому инвойсу они относятся.
func groupPagesByInvoice(client *openai.Client, imageContents [][]byte) (map[string][]int, error) {
	prompt := buildGroupingPrompt()

	parts := []openai.ChatMessagePart{
		{
			Type: openai.ChatMessagePartTypeText,
			Text: prompt,
		},
	}

	for i, content := range imageContents {
		encodedImage := base64.StdEncoding.EncodeToString(content)
		imageURL := fmt.Sprintf("data:%s;base64,%s", http.DetectContentType(content), encodedImage)

		// Добавляем текстовый маркер для страницы
		parts = append(parts, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeText,
			Text: fmt.Sprintf("This is Page %d.", i),
		})

		// Добавляем саму страницу
		parts = append(parts, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{
				URL:    imageURL,
				Detail: openai.ImageURLDetailLow, // Используем низкое разрешение для скорости
			},
		})
	}

	resp, err := client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: openai.GPT4o,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:         openai.ChatMessageRoleUser,
					MultiContent: parts,
				},
			},
			ResponseFormat: &openai.ChatCompletionResponseFormat{
				Type: openai.ChatCompletionResponseFormatTypeJSONObject,
			},
		},
	)

	if err != nil {
		return nil, fmt.Errorf("grouping request to OpenAI failed: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI returned no choices for grouping")
	}

	var groups map[string][]int
	err = json.Unmarshal([]byte(resp.Choices[0].Message.Content), &groups)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal grouping response: %w. Response: %s", err, resp.Choices[0].Message.Content)
	}

	return groups, nil
}

// analyzeInvoicePages отправляет выбранные страницы инвойса для детального анализа.
func analyzeInvoicePages(client *openai.Client, imageContents [][]byte, myCompany Counterparty) (*Invoice, error) {
	prompt := buildDetailedPrompt(myCompany)

	parts := []openai.ChatMessagePart{
		{
			Type: openai.ChatMessagePartTypeText,
			Text: prompt,
		},
	}

	for _, content := range imageContents {
		encodedImage := base64.StdEncoding.EncodeToString(content)
		imageURL := fmt.Sprintf("data:%s;base64,%s", http.DetectContentType(content), encodedImage)
		parts = append(parts, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{
				URL: imageURL,
			},
		})
	}

	resp, err := client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: openai.GPT4o,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:         openai.ChatMessageRoleUser,
					MultiContent: parts,
				},
			},
			ResponseFormat: &openai.ChatCompletionResponseFormat{
				Type: openai.ChatCompletionResponseFormatTypeJSONObject,
			},
		},
	)

	if err != nil {
		return nil, fmt.Errorf("detailed analysis request to OpenAI failed: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI returned no choices for detailed analysis")
	}

	var invoice Invoice
	err = json.Unmarshal([]byte(resp.Choices[0].Message.Content), &invoice)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal detailed analysis response: %w. Response: %s", err, resp.Choices[0].Message.Content)
	}

	return &invoice, nil
}

// selectPagesForAnalysis выбирает до 4 страниц для анализа: 2 первые и 2 последние.
func selectPagesForAnalysis(pageIndices []int) []int {
	if len(pageIndices) <= 4 {
		return pageIndices
	}

	sort.Ints(pageIndices)

	selected := make(map[int]bool)
	result := []int{}

	// Добавляем первые 2
	for _, idx := range pageIndices[:2] {
		if !selected[idx] {
			selected[idx] = true
			result = append(result, idx)
		}
	}
	// Добавляем последние 2
	for _, idx := range pageIndices[len(pageIndices)-2:] {
		if !selected[idx] {
			selected[idx] = true
			result = append(result, idx)
		}
	}

	sort.Ints(result)
	return result
}

// --- Функции для создания промптов ---

func buildGroupingPrompt() string {
	return `You are a document sorting assistant. I will provide a series of pages, each preceded by a text marker like "This is Page X.".
Your task is to analyze these pages and find an invoice number and date to use as a unique identifier for the document each page belongs to.
Group the page numbers (the 'X' from the text marker) by this identifier.
Return ONLY a valid JSON object where keys are the invoice identifiers (e.g., "INV-123_2023-10-27") and values are arrays of the corresponding page numbers (as integers).

Example response for 5 pages belonging to 2 invoices:
{
  "INV-2023-01_2023-01-15": [0, 1, 2],
  "PO-5567_2023-01-16": [3, 4]
}`
}

func buildDetailedPrompt(myCompany Counterparty) string {
	return fmt.Sprintf(`
You are an expert accountant. The following images are pages from a SINGLE invoice. Analyze them together to extract information into a single JSON object.

**Important Rules:**
1.  **Find the overall total:** Look for the final, grand total amount across all pages. This is the most important value.
2.  **Summarize the purpose:** For the 'purpose' field, provide a very short, 2-3 word summary (e.g., "продукты питания", "услуги сотовой связи", "мебель").
3.  **Extract invoice details:**
    *   "type": Use '1' for "Платежное поручение" (Invoice/Bill) or '2' for "Кассовый чек" (Receipt). This is an integer.
    *   "number": The invoice or receipt number.
    *   "date": The invoice date in YYYY-MM-DD format.
    *   "total_amount": The final, total amount as a float.
    *   "tax_amount": The total tax amount (e.g., VAT, НДС). If not present, use 0.
4.  **Identify the Counterparty (the *other* company, not ours):**
    *   **Required fields:** "name", "vat", "country", "address".
    *   **Optional fields:** If present, also extract "swift", "iban", "phone", "fax", "email", "website".
5.  **My company's details are for context only.** Do NOT extract them. My company is:
    *   Name: %s, VAT: %s, Country: %s, Address: %s
6.  **Output format:** Respond ONLY with a single, valid JSON object.

Example JSON:
{
  "type": 1,
  "number": "INV-12345",
  "date": "2023-10-27",
  "total_amount": 1500.75,
  "tax_amount": 75.25,
  "purpose": "Лицензия на ПО",
  "counterparty": {
    "name": "ООО 'ТехноСофт'",
    "vat": "7701234567",
    "country": "Россия",
    "address": "г. Москва, ул. Программистов, д. 1",
    "swift": "SABRRUMM",
    "iban": "RU40802810100000000001",
    "email": "contact@technosoft.com"
  }
}
`, myCompany.Name, myCompany.VAT, myCompany.Country, myCompany.Address)
}

// convertPDFToImages использует утилиту `pdftoppm` (из пакета poppler) для конвертации PDF в изображения.
// **Требование:** Утилита `poppler` должна быть установлена в системе или указана в конфиге.
func convertPDFToImages(pdfPath, popplerBinPath string) ([][]byte, error) {
	// 1. Создаем временную директорию для изображений
	tempDir, err := os.MkdirTemp("", "invpa-pages-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// 2. Определяем путь к pdftoppm
	cmdName := "pdftoppm"
	if popplerBinPath != "" && runtime.GOOS == "windows" {
		// В Windows используем путь из конфига, если он есть
		cmdName = filepath.Join(popplerBinPath, cmdName)
	}

	// 3. Выполняем команду `pdftoppm`
	cmd := exec.Command(cmdName, "-png", pdfPath, filepath.Join(tempDir, "page"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("pdftoppm command failed. Is poppler installed and in PATH, or configured in config.json? Error: %w. Output: %s", err, string(output))
	}

	// 4. Читаем созданные файлы
	files, err := os.ReadDir(tempDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read temp dir: %w", err)
	}

	var imageContents [][]byte
	// Сортируем файлы, чтобы страницы шли по порядку
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})

	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".png") {
			content, err := os.ReadFile(filepath.Join(tempDir, file.Name()))
			if err != nil {
				return nil, fmt.Errorf("failed to read generated image %s: %w", file.Name(), err)
			}
			imageContents = append(imageContents, content)
		}
	}

	if len(imageContents) == 0 {
		return nil, fmt.Errorf("pdftoppm did not generate any images")
	}

	return imageContents, nil
}

// --- Новые функции для сопоставления контрагентов ---

// FindCounterparty находит существующего контрагента, соответствующего новому,
// используя OpenAI для "умного" сопоставления.
// Возвращает обновленного контрагента или nil, если совпадение не найдено.
func FindCounterparty(client *openai.Client, existingCounterparties []Counterparty, newCounterparty Counterparty) (*Counterparty, error) {
	if len(existingCounterparties) == 0 {
		return nil, nil
	}

	// 1. Подготовить данные в виде JSON
	existingJSON, err := json.Marshal(existingCounterparties)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal existing counterparties: %w", err)
	}
	newJSON, err := json.Marshal(newCounterparty)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal new counterparty: %w", err)
	}

	// 2. Создать промпт
	prompt := buildMatchingPrompt(string(existingJSON), string(newJSON))

	// 3. Отправить запрос в OpenAI
	resp, err := client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: openai.GPT4o,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: prompt,
				},
			},
			ResponseFormat: &openai.ChatCompletionResponseFormat{
				Type: openai.ChatCompletionResponseFormatTypeJSONObject,
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("matching request to OpenAI failed: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI returned no choices for matching")
	}

	// 4. Распарсить ответ
	type MatchResponse struct {
		MatchFound bool   `json:"match_found"`
		MatchedID  string `json:"matched_id"`
	}
	var match MatchResponse
	err = json.Unmarshal([]byte(resp.Choices[0].Message.Content), &match)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal matching response: %w. Response: %s", err, resp.Choices[0].Message.Content)
	}

	// 5. Если совпадение найдено
	if match.MatchFound {
		for _, existing := range existingCounterparties {
			if existing.ID == match.MatchedID {
				// a. Нашли контрагента, дополняем его данные
				updatedCounterparty := mergeCounterparties(existing, newCounterparty)
				return &updatedCounterparty, nil
			}
		}
		return nil, fmt.Errorf("AI found a match with ID '%s' but this ID does not exist in the provided list", match.MatchedID)
	}

	// 6. Если совпадение не найдено
	return nil, nil
}

func buildMatchingPrompt(existingJSON, newJSON string) string {
	return fmt.Sprintf(`
You are a data deduplication system. Your task is to find the most likely candidate from a list of existing counterparties ('existing_list') that matches a new counterparty entry ('new_entry').

**Matching Rules:**
1.  **High-confidence identifiers:** A match in 'vat', 'iban', 'website', or 'phone' is a very strong signal that it's the same entity.
2.  **Name field:** The 'name' field is also important, but be aware of abbreviations, missing legal forms (like LLC, Inc.), or minor variations.
3.  **ID is key:** The 'id' field in the 'existing_list' is the unique identifier from our database.

**Your Task:**
Analyze the 'new_entry' and compare it against every item in the 'existing_list'.
If you find a confident match, respond with the ID of that match.
If no confident match is found, indicate that.

**Input Data:**
- existing_list: %s
- new_entry: %s

**Output Format:**
Respond ONLY with a single, valid JSON object with the following structure:
{
  "match_found": true,  // boolean: true if a match was found, otherwise false
  "matched_id": "some-uuid-123" // string: the 'id' of the matched counterparty from 'existing_list'. Omit or leave empty if no match.
}
`, existingJSON, newJSON)
}

// mergeCounterparties объединяет данные двух контрагентов.
// Данные из 'newData' имеют приоритет, если поле в 'existing' пустое.
func mergeCounterparties(existing, newData Counterparty) Counterparty {
	merged := existing

	// Мы не обновляем Name, Country, Address, так как они могут быть более точными в базе
	// Но если в базе чего-то нет, а в новом инвойсе есть - добавляем.
	if merged.VAT == "" && newData.VAT != "" {
		merged.VAT = newData.VAT
	}
	if merged.SWIFT == "" && newData.SWIFT != "" {
		merged.SWIFT = newData.SWIFT
	}
	if merged.IBAN == "" && newData.IBAN != "" {
		merged.IBAN = newData.IBAN
	}
	if merged.Phone == "" && newData.Phone != "" {
		merged.Phone = newData.Phone
	}
	if merged.Fax == "" && newData.Fax != "" {
		merged.Fax = newData.Fax
	}
	if merged.Email == "" && newData.Email != "" {
		merged.Email = newData.Email
	}
	if merged.Website == "" && newData.Website != "" {
		merged.Website = newData.Website
	}

	return merged
}
