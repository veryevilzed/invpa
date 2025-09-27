package invoice

// Invoice представляет данные, извлеченные из одного счета.
type Invoice struct {
	Type         int          `json:"type"`         // Тип документа: 1 для "Платежное поручение", 2 для "Кассовый чек"
	Number       string       `json:"number"`       // Номер инвоиса
	Date         string       `json:"date"`         // Дата инвоиса (YYYY-MM-DD)
	TotalAmount  float64      `json:"total_amount"` // Общая сумма
	TaxAmount    float64      `json:"tax_amount"`   // Сумма налога
	Purpose      string       `json:"purpose"`      // Краткое назначение платежа
	Counterparty Counterparty `json:"counterparty"` // Данные контрагента
}

// Counterparty представляет данные о контрагенте.
type Counterparty struct {
	ID      string `json:"id,omitempty"`      // ID из внешней системы (базы данных)
	Name    string `json:"name"`              // Наименование компании
	VAT     string `json:"vat"`               // VAT номер
	Country string `json:"country"`           // Страна
	Address string `json:"address"`           // Адрес
	SWIFT   string `json:"swift,omitempty"`   // SWIFT/BIC (необязательно)
	IBAN    string `json:"iban,omitempty"`    // IBAN (необязательно)
	Phone   string `json:"phone,omitempty"`   // Телефон (необязательно)
	Fax     string `json:"fax,omitempty"`     // Факс (необязательно)
	Email   string `json:"email,omitempty"`   // Email (необязательно)
	Website string `json:"website,omitempty"` // Веб-сайт (необязательно)
}

// Config структура для загрузки конфигурации
type Config struct {
	OpenAPIKey         string       `json:"openai_api_key"`
	MyCompany          Counterparty `json:"my_company"`
	PopplerPathWindows string       `json:"poppler_path_windows,omitempty"`
}
