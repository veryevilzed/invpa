# invpa - Библиотека для анализа инвойсов

`invpa` - это Go-библиотека, предназначенная для извлечения структурированных данных из файлов инвойсов (PDF, PNG, JPG) с использованием OpenAI GPT-4o.

## Особенности

-   **Анализ различных форматов:** Поддерживает PDF, PNG, и JPG/JPEG.
-   **Обработка многостраничных PDF:** Автоматически конвертирует страницы PDF в изображения для анализа.
-   **Умная группировка:** Способна определять несколько отдельных инвойсов в одном PDF-файле.
-   **Оптимизация:** Для анализа многостраничных документов используются только первые и последние страницы, что экономит токены и ускоряет обработку.
-   **Структурированный вывод:** Возвращает типизированные Go-структуры с данными инвойса и контрагента.

## Требования

-   Go 1.18+
-   **Poppler:** Библиотека требует утилиту `pdftoppm` для обработки PDF-файлов.

### Установка Poppler

-   **macOS (Homebrew):**
    ```bash
    brew install poppler
    ```
-   **Debian/Ubuntu:**
    ```bash
    sudo apt-get update && sudo apt-get install -y poppler-utils
    ```
-   **Windows:**
    -   Скачайте архив с [официального сайта](https://poppler.freedesktop.org/) или используйте `winget` или `choco`.
    -   Убедитесь, что путь к `bin` директории Poppler добавлен в системную переменную `PATH`.

## Установка библиотеки

```bash
go get github.com/your-username/invpa
```
*(Примечание: Замените `your-username` на актуальный путь к репозиторию, когда он будет опубликован.)*

## Быстрый старт

Ниже приведен пример использования библиотеки для анализа файла инвойса.

```go
package main

import (
	"fmt"
	"log"

	"github.com/your-username/invpa/invoice"
)

func main() {
	apiKey := "sk-..." // Ваш OpenAI API ключ
	filePath := "path/to/your/invoice.pdf"

	// Данные вашей компании (для исключения из анализа)
	myCompany := invoice.Counterparty{
		Name:    "My Awesome Company LLC",
		VAT:     "123456789",
		Country: "USA",
		Address: "123 Main St, Anytown, USA",
	}

	// Обработка файла
	invoices, err := invoice.ProcessFile(filePath, apiKey, myCompany)
	if err != nil {
		log.Fatalf("Ошибка анализа файла: %v", err)
	}

	// Вывод результата
	for _, inv := range invoices {
		fmt.Printf("Найден инвойс №%s от %s\n", inv.Number, inv.Date)
		fmt.Printf("  - Тип: %d\n", inv.Type)
		fmt.Printf("  - Сумма: %.2f\n", inv.TotalAmount)
		fmt.Printf("  - Контрагент: %s (VAT: %s)\n", inv.Counterparty.Name, inv.Counterparty.VAT)
		fmt.Println("---")
	}
}
```

## Структуры данных

Основные структуры, возвращаемые библиотекой, определены в `invoice/invoice.go`:

-   `invoice.Invoice`: Содержит основные данные счета (номер, дата, сумма, тип и т.д.).
-   `invoice.Counterparty`: Содержит данные о контрагенте (наименование, VAT, адрес и другие контактные данные).
