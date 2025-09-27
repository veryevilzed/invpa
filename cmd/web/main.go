package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/sashabaranov/go-openai"
	"github.com/veryevilzed/invpa/invoice"
	"github.com/xuri/excelize/v2"
)

// Global job store
var jobs = make(map[string]*Job)
var jobsMutex = &sync.Mutex{}

// Job holds all information about a processing task
type Job struct {
	ID                   string
	Status               string // "Uploading", "Processing", "Completed", "Error"
	Log                  []string
	Error                string
	ResultPath           string
	DownloadURL          string
	TotalFiles           int
	ProcessedFiles       int
	AllResults           []Result             `json:"-"` // Exclude from default status response
	UniqueCounterparties []UniqueCounterparty `json:"-"` // Exclude from default status response
}

// JobResultData holds the data to be returned for the result tables
type JobResultData struct {
	AllResults           []Result
	UniqueCounterparties []UniqueCounterparty
}

// Structs for processing logic
type Result struct {
	SourceFile   string
	Invoice      *invoice.Invoice
	ErrorMessage string
}

type UniqueCounterparty struct {
	SourceFile   string
	Counterparty invoice.Counterparty
}

var templates *template.Template

func main() {
	if err := os.MkdirAll("temp", os.ModePerm); err != nil {
		log.Fatalf("Could not create temp directory: %v", err)
	}
	if err := os.MkdirAll("public", os.ModePerm); err != nil {
		log.Fatalf("Could not create public directory: %v", err)
	}

	var err error
	templates, err = template.ParseGlob("cmd/web/templates/*.html")
	if err != nil {
		log.Fatalf("Error parsing templates: %v", err)
	}

	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("cmd/web/static"))))
	http.Handle("/public/", http.StripPrefix("/public/", http.FileServer(http.Dir("public"))))

	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/upload", handleUpload)
	http.HandleFunc("/result/", handleResultPage)
	http.HandleFunc("/status/", handleStatus)
	http.HandleFunc("/api/results/", handleJobResultData)

	fmt.Println("Starting server on :8031")
	if err := http.ListenAndServe(":8031", nil); err != nil {
		log.Fatal(err)
	}
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	err := templates.ExecuteTemplate(w, "index.html", nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	file, header, err := r.FormFile("zipfile")
	if err != nil {
		jsonError(w, "Could not get uploaded file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	jobID := uuid.New().String()
	jobDir := filepath.Join("temp", jobID)
	if err := os.MkdirAll(jobDir, os.ModePerm); err != nil {
		jsonError(w, "Could not create job directory", http.StatusInternalServerError)
		return
	}

	zipPath := filepath.Join(jobDir, header.Filename)
	dst, err := os.Create(zipPath)
	if err != nil {
		jsonError(w, "Could not save zip file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		jsonError(w, "Could not save zip file content", http.StatusInternalServerError)
		return
	}

	jobsMutex.Lock()
	jobs[jobID] = &Job{ID: jobID, Status: "Processing", Log: []string{"File uploaded successfully."}}
	jobsMutex.Unlock()

	go processInvoices(jobID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"job_id": jobID})
}

func handleResultPage(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimPrefix(r.URL.Path, "/result/")
	jobsMutex.Lock()
	_, ok := jobs[jobID]
	jobsMutex.Unlock()

	if !ok {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	err := templates.ExecuteTemplate(w, "result.html", map[string]string{"JobId": jobID})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimPrefix(r.URL.Path, "/status/")
	jobsMutex.Lock()
	job, ok := jobs[jobID]
	jobsMutex.Unlock()

	if !ok {
		jsonError(w, "Job not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(job)
}

func handleJobResultData(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimPrefix(r.URL.Path, "/api/results/")
	jobsMutex.Lock()
	job, ok := jobs[jobID]
	jobsMutex.Unlock()

	if !ok || job.Status != "Completed" {
		jsonError(w, "Job not found or not completed", http.StatusNotFound)
		return
	}

	data := JobResultData{
		AllResults:           job.AllResults,
		UniqueCounterparties: job.UniqueCounterparties,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func addLog(jobID, message string) {
	jobsMutex.Lock()
	defer jobsMutex.Unlock()
	if job, ok := jobs[jobID]; ok {
		job.Log = append(job.Log, message)
	}
}

func incrementProcessedCount(jobID string) {
	jobsMutex.Lock()
	defer jobsMutex.Unlock()
	if job, ok := jobs[jobID]; ok {
		job.ProcessedFiles++
	}
}

func setJobError(jobID, errorMsg string) {
	jobsMutex.Lock()
	defer jobsMutex.Unlock()
	if job, ok := jobs[jobID]; ok {
		job.Status = "Error"
		job.Error = errorMsg
		job.Log = append(job.Log, fmt.Sprintf("[ERROR] %s", errorMsg))
	}
}

func processInvoices(jobID string) {
	jobDir := filepath.Join("temp", jobID)
	defer os.RemoveAll(jobDir)

	addLog(jobID, "Unzipping uploaded file...")
	zipPath := ""
	dirEntries, err := os.ReadDir(jobDir)
	if err != nil {
		setJobError(jobID, fmt.Sprintf("Error reading job directory: %v", err))
		return
	}
	for _, entry := range dirEntries {
		if strings.HasSuffix(entry.Name(), ".zip") {
			zipPath = filepath.Join(jobDir, entry.Name())
			break
		}
	}
	if zipPath == "" {
		setJobError(jobID, "No zip file found in job directory.")
		return
	}
	if err := unzip(zipPath, jobDir); err != nil {
		setJobError(jobID, fmt.Sprintf("Failed to unzip file: %v", err))
		return
	}

	addLog(jobID, "Scanning for invoice files...")
	invoiceFiles, err := findInvoiceFiles(jobDir)
	if err != nil {
		setJobError(jobID, fmt.Sprintf("Error scanning for files: %v", err))
		return
	}
	if len(invoiceFiles) == 0 {
		setJobError(jobID, "No invoice files (.pdf, .png, .jpg, .jpeg) found in the zip archive.")
		return
	}

	jobsMutex.Lock()
	jobs[jobID].TotalFiles = len(invoiceFiles)
	jobsMutex.Unlock()
	addLog(jobID, fmt.Sprintf("Found %d files to process. Starting analysis...", len(invoiceFiles)))

	config, err := loadConfig("config.json")
	if err != nil {
		setJobError(jobID, fmt.Sprintf("Could not load config.json: %v", err))
		return
	}
	if config.OpenAPIKey == "" {
		setJobError(jobID, "'openai_api_key' is not set in config.json.")
		return
	}

	client := openai.NewClient(config.OpenAPIKey)
	resultsChan := make(chan Result, len(invoiceFiles))
	var wg sync.WaitGroup

	for _, file := range invoiceFiles {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()
			addLog(jobID, fmt.Sprintf("Processing %s...", filepath.Base(f)))
			invoices, err := invoice.ProcessFile(f, config.OpenAPIKey, config.PopplerPathWindows, config.MyCompany)
			incrementProcessedCount(jobID)
			if err != nil {
				resultsChan <- Result{SourceFile: filepath.Base(f), ErrorMessage: err.Error()}
				return
			}
			if len(invoices) > 0 {
				resultsChan <- Result{SourceFile: filepath.Base(f), Invoice: &invoices[0]}
			} else {
				resultsChan <- Result{SourceFile: filepath.Base(f), ErrorMessage: "No invoices found in file"}
			}
		}(file)
	}

	wg.Wait()
	close(resultsChan)
	addLog(jobID, "Analysis complete. Deduplicating counterparties and generating report...")

	var allResults []Result
	var uniqueCounterparties []UniqueCounterparty
	var existingForSearch []invoice.Counterparty
	var successfulCount, errorCount int

	for res := range resultsChan {
		allResults = append(allResults, res)
		if res.ErrorMessage != "" {
			errorCount++
			addLog(jobID, fmt.Sprintf("Error in %s: %s", res.SourceFile, res.ErrorMessage))
			continue
		}
		successfulCount++

		matched, err := invoice.FindCounterparty(client, existingForSearch, res.Invoice.Counterparty)
		if err != nil {
			addLog(jobID, fmt.Sprintf("WARN: Could not match counterparty for %s: %v", res.SourceFile, err))
			newID := uuid.New().String()
			res.Invoice.Counterparty.ID = newID
			uniqueCounterparties = append(uniqueCounterparties, UniqueCounterparty{SourceFile: res.SourceFile, Counterparty: res.Invoice.Counterparty})
			existingForSearch = append(existingForSearch, res.Invoice.Counterparty)
		} else if matched != nil {
			res.Invoice.Counterparty = *matched
		} else {
			newID := uuid.New().String()
			res.Invoice.Counterparty.ID = newID
			uniqueCounterparties = append(uniqueCounterparties, UniqueCounterparty{SourceFile: res.SourceFile, Counterparty: res.Invoice.Counterparty})
			existingForSearch = append(existingForSearch, res.Invoice.Counterparty)
		}
	}

	resultFileName := fmt.Sprintf("%s.xlsx", jobID)
	resultPath := filepath.Join("public", resultFileName)
	err = generateExcelReport(resultPath, allResults, uniqueCounterparties)
	if err != nil {
		setJobError(jobID, fmt.Sprintf("Failed to generate Excel report: %v", err))
		return
	}

	jobsMutex.Lock()
	if job, ok := jobs[jobID]; ok {
		job.Status = "Completed"
		job.ResultPath = resultPath
		job.DownloadURL = "/public/" + resultFileName
		job.AllResults = allResults
		job.UniqueCounterparties = uniqueCounterparties
		job.Log = append(job.Log, fmt.Sprintf("Successfully generated report with %d processed invoices.", successfulCount))
	}
	jobsMutex.Unlock()
}

// --- Helper Functions ---

func jsonError(w http.ResponseWriter, error string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": error})
}

func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}
		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}
		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func findInvoiceFiles(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Ignore dot-underscore files created by macOS
		if !info.IsDir() && !strings.HasPrefix(info.Name(), "._") {
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".pdf" || ext == ".png" || ext == ".jpg" || ext == ".jpeg" {
				files = append(files, path)
			}
		}
		return nil
	})
	return files, err
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

func generateExcelReport(path string, allResults []Result, counterparties []UniqueCounterparty) error {
	f := excelize.NewFile()
	defer f.Close()
	f.NewSheet("Invoices")
	f.DeleteSheet("Sheet1")
	headers := []string{"Source File", "Status", "Counterparty ID", "Counterparty Name", "Counterparty VAT", "Counterparty Country", "Invoice Number", "Date", "Total Amount", "Tax Amount", "Purpose"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("Invoices", cell, h)
	}
	for i, res := range allResults {
		row := i + 2
		f.SetCellValue("Invoices", fmt.Sprintf("A%d", row), res.SourceFile)
		if res.ErrorMessage != "" {
			f.SetCellValue("Invoices", fmt.Sprintf("B%d", row), res.ErrorMessage)
			style, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Color: "9A0511"}})
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
	return f.SaveAs(path)
}
